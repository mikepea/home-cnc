// Command server is the home-cnc control plane.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mikepea/home-cnc/internal/server"
	"github.com/mikepea/home-cnc/internal/sign"
	"github.com/mikepea/home-cnc/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	addr := flag.String("addr", envOr("CNC_ADDR", ":8080"), "listen address")
	dataDir := flag.String("data", envOr("CNC_DATA_DIR", "./data"), "data directory (db + signing key)")
	pollHold := flag.Duration("poll-hold", 30*time.Second, "how long a poll blocks waiting for a command")
	sessionTTL := flag.Duration("session-ttl", 30*24*time.Hour, "web session lifetime")
	commandTTL := flag.Duration("command-ttl", 60*time.Second, "how long an issued command stays valid")
	insecure := flag.Bool("insecure-cookie", false, "do not set the Secure cookie flag (local http testing only)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Error("create data dir", "err", err)
		os.Exit(1)
	}

	st, err := store.OpenSQLite(filepath.Join(*dataDir, "cnc.db"))
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	signer, err := sign.LoadOrCreate(*dataDir)
	if err != nil {
		log.Error("signing key", "err", err)
		os.Exit(1)
	}
	log.Info("command signing public key", "pubkey", signer.PubBase64())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrapAdmin(ctx, st, log); err != nil {
		log.Error("bootstrap admin", "err", err)
		os.Exit(1)
	}

	srv, err := server.New(server.Config{
		Addr:         *addr,
		DataDir:      *dataDir,
		PollHold:     *pollHold,
		SessionTTL:   *sessionTTL,
		CommandTTL:   *commandTTL,
		SecureCookie: !*insecure,
	}, st, signer, log)
	if err != nil {
		log.Error("build server", "err", err)
		os.Exit(1)
	}

	if err := srv.Run(ctx); err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}
	log.Info("shut down cleanly")
}

// bootstrapAdmin creates the first user from CNC_ADMIN_USER / CNC_ADMIN_PASSWORD
// when the users table is empty. On an already-provisioned instance it is a
// no-op, so the env vars can be left set.
func bootstrapAdmin(ctx context.Context, st store.Store, log *slog.Logger) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	user := os.Getenv("CNC_ADMIN_USER")
	pass := os.Getenv("CNC_ADMIN_PASSWORD")
	if user == "" || pass == "" {
		log.Warn("no users yet; set CNC_ADMIN_USER and CNC_ADMIN_PASSWORD to create the first login")
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, user, string(hash)); err != nil {
		return err
	}
	log.Info("created initial admin user", "username", user)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
