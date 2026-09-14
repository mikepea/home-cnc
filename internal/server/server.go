// Package server implements the home-cnc control plane: the agent-facing REST
// API (long-poll + ack), the phone-friendly web UI, and /healthz.
package server

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/mikepea/home-cnc/internal/sign"
	"github.com/mikepea/home-cnc/internal/store"
)

//go:embed web/*.html
var templateFS embed.FS

//go:embed web/static/*
var staticFS embed.FS

// Config holds server tunables.
type Config struct {
	Addr         string        // listen address, e.g. ":8080" (behind Caddy)
	DataDir      string        // directory for db + signing key
	PollHold     time.Duration // how long a poll request blocks waiting for work
	SessionTTL   time.Duration // web session lifetime
	CommandTTL   time.Duration // how long an enqueued command stays valid
	SecureCookie bool          // set Secure flag on cookies (true in prod)
}

// Server is the HTTP application.
type Server struct {
	cfg    Config
	store  store.Store
	signer *sign.Signer
	notif  *notifier
	tmpl   *template.Template
	log    *slog.Logger
	mux    *http.ServeMux
}

// New builds a Server. Templates are parsed once at startup.
func New(cfg Config, st store.Store, signer *sign.Signer, log *slog.Logger) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "web/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:    cfg,
		store:  st,
		signer: signer,
		notif:  newNotifier(),
		tmpl:   tmpl,
		log:    log,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	// Agent-facing API (bearer-authenticated).
	s.mux.Handle("GET /api/v1/poll", s.deviceAuth(s.handlePoll))
	s.mux.Handle("POST /api/v1/ack/{id}", s.deviceAuth(s.handleAck))
	s.mux.HandleFunc("GET /api/v1/pubkey", s.handlePubkey)

	// Ops.
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Web UI (session-authenticated).
	s.mux.HandleFunc("GET /login", s.handleLoginForm)
	s.mux.HandleFunc("POST /login", s.handleLogin)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
	s.mux.Handle("GET /{$}", s.webAuth(s.handleDashboard))
	s.mux.Handle("POST /devices", s.webAuth(s.handleCreateDevice))
	s.mux.Handle("POST /devices/{id}/delete", s.webAuth(s.handleDeleteDevice))
	s.mux.Handle("POST /devices/{id}/command", s.webAuth(s.handleIssueCommand))

	// Static assets + PWA files. The embed root is web/static, so re-root it.
	staticSub, _ := fs.Sub(staticFS, "web/static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub)))
	s.mux.HandleFunc("GET /manifest.webmanifest", s.serveStatic("web/static/manifest.webmanifest", "application/manifest+json"))
	s.mux.HandleFunc("GET /sw.js", s.serveStatic("web/static/sw.js", "application/javascript"))
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Run starts background maintenance and the HTTP server, shutting down when ctx
// is cancelled.
func (s *Server) Run(ctx context.Context) error {
	go s.maintenanceLoop(ctx)

	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: long-poll holds the response open on purpose.
		IdleTimeout: 90 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	s.log.Info("listening", "addr", s.cfg.Addr, "poll_hold", s.cfg.PollHold)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// maintenanceLoop periodically expires stale commands and sessions.
func (s *Server) maintenanceLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.store.ExpireStaleCommands(ctx); err != nil {
				s.log.Warn("expire commands", "err", err)
			} else if n > 0 {
				s.log.Info("expired stale commands", "count", n)
			}
			if err := s.store.DeleteExpiredSessions(ctx); err != nil {
				s.log.Warn("expire sessions", "err", err)
			}
		}
	}
}

func (s *Server) serveStatic(path, contentType string) http.HandlerFunc {
	b, err := staticFS.ReadFile(path)
	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(b)
	}
}
