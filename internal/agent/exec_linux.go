//go:build linux

package agent

import (
	"fmt"

	"github.com/mikepea/home-cnc/internal/api"
)

// execute runs the requested action. The agent runs as root (systemd service),
// so both poweroff and session locking work directly.
func execute(cmdType api.CommandType) (string, error) {
	switch cmdType {
	case api.CmdHalt:
		return run("systemctl", "poweroff")
	case api.CmdLock:
		return run("loginctl", "lock-sessions")
	default:
		return "", fmt.Errorf("unsupported command %q", cmdType)
	}
}
