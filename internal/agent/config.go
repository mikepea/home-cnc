// Package agent is the device-side daemon: it long-polls the control plane,
// verifies command signatures, and runs the halt/lock action.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the agent's on-disk configuration (default /etc/home-cnc/agent.json).
type Config struct {
	ServerURL string `json:"server_url"` // e.g. https://cnc.m53.org
	Token     string `json:"token"`      // per-device bearer token
	PublicKey string `json:"public_key"` // server signing key, base64(std)
}

// LoadConfig reads config from path, then applies any environment overrides
// (CNC_SERVER_URL, CNC_TOKEN, CNC_PUBLIC_KEY). Env wins so systemd/launchd can
// inject secrets without a file on disk.
func LoadConfig(path string) (Config, error) {
	var c Config
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}

	if v := os.Getenv("CNC_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("CNC_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("CNC_PUBLIC_KEY"); v != "" {
		c.PublicKey = v
	}

	if c.ServerURL == "" || c.Token == "" || c.PublicKey == "" {
		return c, fmt.Errorf("config incomplete: server_url, token and public_key are all required")
	}
	return c, nil
}
