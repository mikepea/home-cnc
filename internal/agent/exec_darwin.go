//go:build darwin

package agent

import (
	"fmt"
	"strings"

	"github.com/mikepea/home-cnc/internal/api"
)

// cgSession is the private tool that suspends (locks) the GUI session. Locking
// must happen inside the console user's context, so a root LaunchDaemon reaches
// it via `launchctl asuser <uid>`.
const cgSession = "/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession"

// execute runs the requested action. The agent runs as a root LaunchDaemon:
// halt is direct; lock is dispatched into the logged-in user's GUI session.
func execute(cmdType api.CommandType) (string, error) {
	switch cmdType {
	case api.CmdHalt:
		return run("shutdown", "-h", "now")
	case api.CmdLock:
		uid, err := consoleUID()
		if err != nil {
			return "", err
		}
		return run("launchctl", "asuser", uid, cgSession, "-suspend")
	default:
		return "", fmt.Errorf("unsupported command %q", cmdType)
	}
}

// consoleUID returns the uid of the user currently at the GUI console.
func consoleUID() (string, error) {
	out, err := run("stat", "-f", "%u", "/dev/console")
	if err != nil {
		return "", fmt.Errorf("determine console user: %w", err)
	}
	uid := strings.TrimSpace(out)
	if uid == "" || uid == "0" {
		return "", fmt.Errorf("no user logged in at console (uid=%q)", uid)
	}
	return uid, nil
}
