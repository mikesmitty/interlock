// Package store is Interlock's sqlite database: runtime state such as the
// node cache, event log, entity history and automation traces. Declarative
// configuration lives in YAML files instead; see package config.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	// Pure Go sqlite driver, registered as "sqlite".
	_ "modernc.org/sqlite"
)

// Store wraps the database connection pool.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and brings its schema up to
// date.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("open database %s: %w", path, err), db.Close())
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Snapshot writes a consistent copy of the database to a new file at
// path, for backups, without blocking other readers for long.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	return nil
}

// Setting returns the stored value for key and whether one exists.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting stores value under key, replacing any previous value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}
