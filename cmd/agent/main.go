// Command agent is the home-cnc device daemon (Linux systemd / macOS LaunchDaemon).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mikepea/home-cnc/internal/agent"
)

func main() {
	configPath := flag.String("config", envOr("CNC_CONFIG", "/etc/home-cnc/agent.json"), "path to agent config")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	a, err := agent.New(cfg, log)
	if err != nil {
		log.Error("init agent", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.Run(ctx); err != nil {
		log.Error("agent", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
