package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mikepea/home-cnc/internal/api"
)

// Agent long-polls the control plane and executes verified commands.
type Agent struct {
	cfg    Config
	pub    ed25519.PublicKey
	client *http.Client
	log    *slog.Logger

	mu   sync.Mutex
	seen map[string]struct{} // command IDs handled this process lifetime
}

// New constructs an Agent, validating the configured public key.
func New(cfg Config, log *slog.Logger) (*Agent, error) {
	pub, err := base64.StdEncoding.DecodeString(cfg.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public_key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public_key has wrong size %d", len(pub))
	}
	return &Agent{
		cfg:    cfg,
		pub:    ed25519.PublicKey(pub),
		client: &http.Client{Timeout: 60 * time.Second},
		log:    log,
		seen:   make(map[string]struct{}),
	}, nil
}

// Run polls until ctx is cancelled, backing off on transient errors.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("agent started", "server", a.cfg.ServerURL)
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return nil
		}
		cmd, err := a.pollOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			a.log.Warn("poll failed", "err", err, "retry_in", backoff)
			if !sleepCtx(ctx, backoff) {
				return nil
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
		if cmd == nil {
			continue // 204: no work, poll again
		}
		a.handle(ctx, *cmd)
	}
}

// pollOnce performs one long-poll. It returns (nil, nil) on 204.
func (a *Agent) pollOnce(ctx context.Context) (*api.Command, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.ServerURL+"/api/v1/poll", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var cmd api.Command
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&cmd); err != nil {
			return nil, fmt.Errorf("decode command: %w", err)
		}
		return &cmd, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("poll status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// handle verifies and runs a command. Halt is acknowledged *before* execution
// so a machine that powers off mid-cycle does not re-run the halt on next boot
// (belt and braces alongside the command's short expiry).
func (a *Agent) handle(ctx context.Context, cmd api.Command) {
	if err := a.verify(cmd); err != nil {
		a.log.Warn("rejected command", "id", cmd.ID, "type", cmd.Type, "err", err)
		return
	}
	if a.alreadyHandled(cmd.ID) {
		return
	}
	a.markHandled(cmd.ID)
	a.log.Info("command accepted", "id", cmd.ID, "type", cmd.Type)

	if cmd.Type == api.CmdHalt {
		a.ack(ctx, cmd.ID, "done", "halting")
		if out, err := execute(cmd.Type); err != nil {
			a.log.Error("halt failed", "err", err, "out", out)
		}
		return
	}

	out, err := execute(cmd.Type)
	if err != nil {
		a.log.Error("command failed", "type", cmd.Type, "err", err, "out", out)
		a.ack(ctx, cmd.ID, "failed", trim(err.Error()+" "+out))
		return
	}
	a.ack(ctx, cmd.ID, "done", trim(out))
}

// verify checks the ed25519 signature, expiry, and command type.
func (a *Agent) verify(cmd api.Command) error {
	if !cmd.Type.Valid() {
		return fmt.Errorf("unknown type %q", cmd.Type)
	}
	sig, err := base64.StdEncoding.DecodeString(cmd.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(a.pub, cmd.SigningBytes(), sig) {
		return fmt.Errorf("bad signature")
	}
	if time.Now().Unix() >= cmd.ExpiresAt {
		return fmt.Errorf("command expired")
	}
	return nil
}

func (a *Agent) ack(ctx context.Context, id, status, result string) {
	body, _ := json.Marshal(api.AckRequest{Status: status, Result: result})
	// Use a fresh short context so ack still fires even as Run's ctx winds down.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, a.cfg.ServerURL+"/api/v1/ack/"+id, bytes.NewReader(body))
	if err != nil {
		a.log.Warn("build ack", "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		a.log.Warn("send ack", "err", err)
		return
	}
	resp.Body.Close()
}

func (a *Agent) alreadyHandled(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.seen[id]
	return ok
}

func (a *Agent) markHandled(id string) {
	a.mu.Lock()
	a.seen[id] = struct{}{}
	a.mu.Unlock()
}

// run executes a system command and returns its combined output.
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
