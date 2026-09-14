package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/mikepea/home-cnc/internal/api"
	"github.com/mikepea/home-cnc/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type deviceView struct {
	store.Device
	LastSeenHuman string
	Online        bool
	Recent        []store.Command
}

type dashboardData struct {
	CSRF      string
	Devices   []deviceView
	NewToken  string // shown exactly once, right after creating a device
	NewDevice string
	ServerURL string
	PublicKey string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login.html", map[string]any{"Error": ""})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")

	fail := func() {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login.html", map[string]any{"Error": "Invalid username or password."})
	}

	u, err := s.store.UserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		// Compare against a dummy hash to blunt username enumeration by timing.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva"), []byte(password))
		fail()
		return
	}
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		fail()
		return
	}

	expires := time.Now().Add(s.cfg.SessionTTL)
	sess := store.Session{
		Token:     randSessionToken(),
		UserID:    u.ID,
		CSRF:      randSessionToken(),
		ExpiresAt: expires,
	}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, sess.Token, expires)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, sess store.Session) {
	s.renderDashboard(w, r, sess, dashboardData{})
}

func (s *Server) renderDashboard(w http.ResponseWriter, r *http.Request, sess store.Session, extra dashboardData) {
	devices, err := s.store.ListDevices(r.Context())
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	views := make([]deviceView, 0, len(devices))
	for _, d := range devices {
		recent, _ := s.store.RecentCommands(r.Context(), d.ID, 5)
		v := deviceView{Device: d, LastSeenHuman: "never", Recent: recent}
		if d.LastSeen != nil {
			v.LastSeenHuman = humanizeSince(*d.LastSeen)
			// "online" if seen within ~2 poll holds.
			v.Online = time.Since(*d.LastSeen) < 2*s.cfg.PollHold+5*time.Second
		}
		views = append(views, v)
	}

	data := extra
	data.CSRF = sess.CSRF
	data.Devices = views
	data.ServerURL = externalURL(r)
	data.PublicKey = s.signer.PubBase64()
	s.render(w, "index.html", data)
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request, sess store.Session) {
	if !s.checkCSRF(r, sess) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	name := r.PostFormValue("name")
	osName := r.PostFormValue("os")
	if name == "" || (osName != "linux" && osName != "darwin") {
		http.Error(w, "name required; os must be linux or darwin", http.StatusBadRequest)
		return
	}
	dev, token, err := s.store.CreateDevice(r.Context(), name, osName)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	s.log.Info("device created", "name", dev.Name, "os", dev.OS, "id", dev.ID)
	s.renderDashboard(w, r, sess, dashboardData{NewToken: token, NewDevice: dev.Name})
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request, sess store.Session) {
	if !s.checkCSRF(r, sess) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteDevice(r.Context(), id); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleIssueCommand(w http.ResponseWriter, r *http.Request, sess store.Session) {
	if !s.checkCSRF(r, sess) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	cmdType := api.CommandType(r.PostFormValue("type"))
	if !cmdType.Valid() {
		http.Error(w, "unknown command", http.StatusBadRequest)
		return
	}
	dev, err := s.store.GetDevice(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such device", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	cmd := store.Command{
		ID:        randCommandID(),
		DeviceID:  dev.ID,
		Type:      cmdType,
		Status:    "pending",
		CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.CommandTTL),
	}
	// Sign the canonical payload so the agent can verify authenticity.
	sig := api.Command{ID: cmd.ID, DeviceID: cmd.DeviceID, Type: cmd.Type, ExpiresAt: cmd.ExpiresAt.Unix()}
	cmd.Signature = s.signer.Sign(sig.SigningBytes())

	if err := s.store.EnqueueCommand(r.Context(), cmd); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	s.notif.notify(dev.ID) // wake any waiting long-poll immediately
	s.log.Info("command issued", "device", dev.Name, "type", cmdType, "cmd", cmd.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) checkCSRF(r *http.Request, sess store.Session) bool {
	got := r.PostFormValue("csrf")
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRF)) == 1
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render", "template", name, "err", err)
	}
}
