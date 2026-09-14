package server

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mikepea/home-cnc/internal/store"
)

const sessionCookie = "cnc_session"

// deviceAuth wraps an agent-facing handler with bearer-token authentication.
func (s *Server) deviceAuth(next func(http.ResponseWriter, *http.Request, store.Device)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		dev, err := s.store.DeviceByToken(r.Context(), token)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if err != nil {
			s.log.Error("device lookup", "err", err)
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		_ = s.store.TouchDevice(r.Context(), dev.ID, clientIP(r))
		next(w, r, dev)
	})
}

// webAuth wraps a browser-facing handler, redirecting to /login when there is
// no valid session.
func (s *Server) webAuth(next func(http.ResponseWriter, *http.Request, store.Session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.currentSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, sess)
	})
}

func (s *Server) currentSession(r *http.Request) (store.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return store.Session{}, false
	}
	sess, err := s.store.Session(r.Context(), c.Value)
	if err != nil {
		return store.Session{}, false
	}
	if time.Now().After(sess.ExpiresAt) {
		return store.Session{}, false
	}
	return sess, true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if strings.HasPrefix(h, prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// clientIP prefers the first X-Forwarded-For entry (set by the Caddy reverse
// proxy) and falls back to the socket peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
