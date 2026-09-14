// Package store is the persistence layer. The Store interface is deliberately
// database-agnostic so the SQLite implementation (v1) can be swapped for
// PostgreSQL later (issue #3) without touching the server logic.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/mikepea/home-cnc/internal/api"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// User is a control-panel login.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// Session is a browser session for the web UI.
type Session struct {
	Token     string
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
}

// Device is a managed machine.
type Device struct {
	ID        string
	Name      string
	OS        string
	CreatedAt time.Time
	LastSeen  *time.Time
	LastIP    string
}

// Command is a stored, signed instruction for a device.
type Command struct {
	ID        string
	DeviceID  string
	Type      api.CommandType
	Status    string
	Signature string
	CreatedAt time.Time
	ExpiresAt time.Time
	AckedAt   *time.Time
	Result    string
}

// Store is the full persistence contract.
type Store interface {
	// Users
	CreateUser(ctx context.Context, username, passwordHash string) (User, error)
	UserByUsername(ctx context.Context, username string) (User, error)
	CountUsers(ctx context.Context) (int, error)

	// Sessions
	CreateSession(ctx context.Context, s Session) error
	Session(ctx context.Context, token string) (Session, error)
	DeleteSession(ctx context.Context, token string) error
	DeleteExpiredSessions(ctx context.Context) error

	// Devices. CreateDevice returns the one-time plaintext token.
	CreateDevice(ctx context.Context, name, os string) (Device, string, error)
	DeviceByToken(ctx context.Context, token string) (Device, error)
	GetDevice(ctx context.Context, id string) (Device, error)
	ListDevices(ctx context.Context) ([]Device, error)
	TouchDevice(ctx context.Context, id, ip string) error
	DeleteDevice(ctx context.Context, id string) error

	// Commands
	EnqueueCommand(ctx context.Context, c Command) error
	NextPendingCommand(ctx context.Context, deviceID string) (Command, error)
	AckCommand(ctx context.Context, deviceID, cmdID, status, result string) error
	ExpireStaleCommands(ctx context.Context) (int, error)
	RecentCommands(ctx context.Context, deviceID string, limit int) ([]Command, error)

	Close() error
}
