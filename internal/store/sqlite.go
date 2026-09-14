package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mikepea/home-cnc/internal/api"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// SQLite is the v1 Store implementation backed by a single file.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) the database at path and applies the
// schema.
func OpenSQLite(path string) (*SQLite, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite writes serialize; one connection avoids "database is locked" churn.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

// --- helpers ---

func randToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// HashToken is exported so callers can hash an incoming bearer token the same
// way the store stores it.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func timePtr(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0)
	return &t
}

// --- users ---

func (s *SQLite) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`,
		username, passwordHash, now.Unix())
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now}, nil
}

func (s *SQLite) UserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.Unix(created, 0)
	return u, nil
}

func (s *SQLite) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// --- sessions ---

func (s *SQLite) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, csrf, created_at, expires_at) VALUES(?,?,?,?,?)`,
		sess.Token, sess.UserID, sess.CSRF, time.Now().Unix(), sess.ExpiresAt.Unix())
	return err
}

func (s *SQLite) Session(ctx context.Context, token string) (Session, error) {
	var sess Session
	var exp int64
	err := s.db.QueryRowContext(ctx,
		`SELECT token, user_id, csrf, expires_at FROM sessions WHERE token=?`, token).
		Scan(&sess.Token, &sess.UserID, &sess.CSRF, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	sess.ExpiresAt = time.Unix(exp, 0)
	return sess, nil
}

func (s *SQLite) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (s *SQLite) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

// --- devices ---

func (s *SQLite) CreateDevice(ctx context.Context, name, os string) (Device, string, error) {
	token := randToken(32)
	d := Device{ID: randID(), Name: name, OS: os, CreatedAt: time.Now()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devices(id, name, os, token_hash, created_at) VALUES(?,?,?,?,?)`,
		d.ID, d.Name, d.OS, HashToken(token), d.CreatedAt.Unix())
	if err != nil {
		return Device{}, "", err
	}
	return d, token, nil
}

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var created int64
	var lastSeen sql.NullInt64
	var lastIP sql.NullString
	if err := row.Scan(&d.ID, &d.Name, &d.OS, &created, &lastSeen, &lastIP); err != nil {
		return Device{}, err
	}
	d.CreatedAt = time.Unix(created, 0)
	d.LastSeen = timePtr(lastSeen)
	d.LastIP = lastIP.String
	return d, nil
}

const deviceCols = `id, name, os, created_at, last_seen, last_ip`

func (s *SQLite) DeviceByToken(ctx context.Context, token string) (Device, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE token_hash=?`, HashToken(token))
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

func (s *SQLite) GetDevice(ctx context.Context, id string) (Device, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id=?`, id)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

func (s *SQLite) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *SQLite) TouchDevice(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devices SET last_seen=?, last_ip=? WHERE id=?`, time.Now().Unix(), ip, id)
	return err
}

func (s *SQLite) DeleteDevice(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id=?`, id)
	return err
}

// --- commands ---

func (s *SQLite) EnqueueCommand(ctx context.Context, c Command) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO commands(id, device_id, type, status, signature, created_at, expires_at)
		 VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.DeviceID, string(c.Type), c.Status, c.Signature,
		c.CreatedAt.Unix(), c.ExpiresAt.Unix())
	return err
}

func (s *SQLite) NextPendingCommand(ctx context.Context, deviceID string) (Command, error) {
	now := time.Now().Unix()
	var c Command
	var typ string
	var created, expires int64
	var acked sql.NullInt64
	var result sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, device_id, type, status, signature, created_at, expires_at, acked_at, result
		 FROM commands
		 WHERE device_id=? AND status='pending' AND expires_at > ?
		 ORDER BY created_at ASC LIMIT 1`, deviceID, now).
		Scan(&c.ID, &c.DeviceID, &typ, &c.Status, &c.Signature, &created, &expires, &acked, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return Command{}, ErrNotFound
	}
	if err != nil {
		return Command{}, err
	}
	c.Type = api.CommandType(typ)
	c.CreatedAt = time.Unix(created, 0)
	c.ExpiresAt = time.Unix(expires, 0)
	c.AckedAt = timePtr(acked)
	c.Result = result.String
	return c, nil
}

func (s *SQLite) AckCommand(ctx context.Context, deviceID, cmdID, status, result string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status=?, result=?, acked_at=?
		 WHERE id=? AND device_id=? AND status='pending'`,
		status, result, time.Now().Unix(), cmdID, deviceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) ExpireStaleCommands(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='expired'
		 WHERE status='pending' AND expires_at <= ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLite) RecentCommands(ctx context.Context, deviceID string, limit int) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, device_id, type, status, signature, created_at, expires_at, acked_at, result
		 FROM commands WHERE device_id=? ORDER BY created_at DESC LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		var c Command
		var typ string
		var created, expires int64
		var acked sql.NullInt64
		var result sql.NullString
		if err := rows.Scan(&c.ID, &c.DeviceID, &typ, &c.Status, &c.Signature,
			&created, &expires, &acked, &result); err != nil {
			return nil, err
		}
		c.Type = api.CommandType(typ)
		c.CreatedAt = time.Unix(created, 0)
		c.ExpiresAt = time.Unix(expires, 0)
		c.AckedAt = timePtr(acked)
		c.Result = result.String
		out = append(out, c)
	}
	return out, rows.Err()
}
