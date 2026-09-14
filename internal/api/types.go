// Package api holds types shared between the server and the agent.
package api

import "fmt"

// CommandType is the tightly-restricted set of things an agent can be told to
// do. There is deliberately no generic "run this" command.
type CommandType string

const (
	CmdHalt CommandType = "halt"
	CmdLock CommandType = "lock"
)

// Valid reports whether t is a command the agent knows how to run.
func (t CommandType) Valid() bool {
	return t == CmdHalt || t == CmdLock
}

// Command is what the server hands an agent in a poll response. The signature
// is an ed25519 signature (base64, std encoding) over SigningBytes, produced by
// the server's private key and verified by the agent against a baked-in public
// key. DeviceID and ExpiresAt are inside the signed payload so a command cannot
// be replayed to a different device or long after it was issued.
type Command struct {
	ID        string      `json:"id"`
	DeviceID  string      `json:"device_id"`
	Type      CommandType `json:"type"`
	ExpiresAt int64       `json:"expires_at"` // unix seconds
	Signature string      `json:"signature"`  // base64(std) ed25519 sig
}

// SigningBytes is the canonical byte string that is signed and verified. Any
// change here must be made in lockstep on server and agent.
func (c Command) SigningBytes() []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%d", c.ID, c.DeviceID, c.Type, c.ExpiresAt))
}

// AckRequest is what the agent POSTs back after handling a command.
type AckRequest struct {
	Status string `json:"status"` // "done" | "failed"
	Result string `json:"result"` // free-form detail / error message
}
