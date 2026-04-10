// Package store provides PostgreSQL-backed persistence for users and rooms.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Config holds PostgreSQL connection settings.
type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	SSLMode  string `json:"ssl_mode"`
}

// Store wraps a pgxpool for user and room operations.
type Store struct {
	pool *pgxpool.Pool
}

// User represents a stored user.
type User struct {
	ID           int64
	Username     string
	PasswordHash string // bcrypt hash; empty string means passwordless
	CreatedAt    time.Time
}

// Room represents a stored room.
type Room struct {
	ID           int64
	Name         string
	PasswordHash string // bcrypt hash; rooms MUST have a password
	Subnet       string
	CreatedBy    string
	CreatedAt    time.Time
}

// New connects to PostgreSQL and ensures the schema exists.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database, cfg.SSLMode,
	)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// migrate creates tables if they do not exist.
func (s *Store) migrate(ctx context.Context) error {
	ddl := `
CREATE TABLE IF NOT EXISTS users (
	id            BIGSERIAL PRIMARY KEY,
	username      TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL DEFAULT '',
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rooms (
	id            BIGSERIAL PRIMARY KEY,
	name          TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	subnet        TEXT NOT NULL,
	created_by    TEXT NOT NULL DEFAULT '',
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
`
	_, err := s.pool.Exec(ctx, ddl)
	return err
}

// ---------- User operations ----------

// CreateUser registers a new user. password may be empty for passwordless accounts.
func (s *Store) CreateUser(ctx context.Context, username, password string) error {
	var hash string
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		hash = string(h)
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2)`,
		username, hash,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// AuthenticateUser verifies credentials. For passwordless accounts the provided
// password is ignored. Returns true if the user exists and the password matches.
func (s *Store) AuthenticateUser(ctx context.Context, username, password string) (bool, error) {
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE username = $1`, username,
	).Scan(&hash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query user: %w", err)
	}

	// Passwordless account – any password is accepted.
	if hash == "" {
		return true, nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return false, nil
	}
	return true, nil
}

// UserExists checks whether a username is registered.
func (s *Store) UserExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, username,
	).Scan(&exists)
	return exists, err
}

// ---------- Room operations ----------

// CreateRoom persists a new room. password must not be empty.
func (s *Store) CreateRoom(ctx context.Context, name, password, subnet, createdBy string) error {
	if password == "" {
		return fmt.Errorf("room password is required")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash room password: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO rooms (name, password_hash, subnet, created_by) VALUES ($1, $2, $3, $4)`,
		name, string(h), subnet, createdBy,
	)
	if err != nil {
		return fmt.Errorf("insert room: %w", err)
	}
	return nil
}

// AuthenticateRoom checks a room password. Returns the room if valid.
func (s *Store) AuthenticateRoom(ctx context.Context, name, password string) (*Room, error) {
	room := &Room{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, password_hash, subnet, created_by, created_at FROM rooms WHERE name = $1`,
		name,
	).Scan(&room.ID, &room.Name, &room.PasswordHash, &room.Subnet, &room.CreatedBy, &room.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query room: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(room.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("room password mismatch")
	}
	return room, nil
}

// GetRoom fetches a room by name (without verifying password).
func (s *Store) GetRoom(ctx context.Context, name string) (*Room, error) {
	room := &Room{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, password_hash, subnet, created_by, created_at FROM rooms WHERE name = $1`,
		name,
	).Scan(&room.ID, &room.Name, &room.PasswordHash, &room.Subnet, &room.CreatedBy, &room.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query room: %w", err)
	}
	return room, nil
}

// ListRooms returns all rooms (name + subnet only, no passwords).
func (s *Store) ListRooms(ctx context.Context) ([]Room, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, subnet, created_by, created_at FROM rooms ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.ID, &r.Name, &r.Subnet, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

// RoomExists checks whether a room name is taken.
func (s *Store) RoomExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM rooms WHERE name = $1)`, name,
	).Scan(&exists)
	return exists, err
}
