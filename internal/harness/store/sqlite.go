// Package store persists harness-owned conversations and configuration. It is
// intentionally separate from the Token Logger usage database.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

const StateVersion = 1
const maxStateBytes = 64 << 20

type Store struct {
	db *sql.DB
}

func EnsurePrivateDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("data directory path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return fmt.Errorf("create harness data directory: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("harness data path must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("harness data directory is accessible by group or others; restrict it to the current user")
	}
	return nil
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := EnsurePrivateDir(filepath.Dir(abs)); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(abs); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("harness database must be a regular file")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("harness database is accessible by group or others; restrict it to the current user")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		f, createErr := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if createErr != nil && !os.IsExist(createErr) {
			return nil, fmt.Errorf("create harness database: %w", createErr)
		}
		if createErr == nil {
			if closeErr := f.Close(); closeErr != nil {
				return nil, closeErr
			}
		}
	}
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	dsn := fileURL + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open harness database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect harness database: %w", err)
	}
	s := &Store{db: db}
	if err := s.initialize(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS harness_metadata (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  schema_version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS runtime_state (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  state_version INTEGER NOT NULL,
  payload BLOB NOT NULL,
  updated_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("initialize harness schema: %w", err)
	}
	var version int
	err := s.db.QueryRow("SELECT schema_version FROM harness_metadata WHERE singleton=1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.Exec("INSERT INTO harness_metadata(singleton, schema_version) VALUES(1, ?)", StateVersion)
		return err
	}
	if err != nil {
		return err
	}
	if version != StateVersion {
		return fmt.Errorf("unsupported harness storage schema %d (this build supports %d)", version, StateVersion)
	}
	return nil
}

func (s *Store) Load() ([]byte, error) {
	var version int
	var payload []byte
	err := s.db.QueryRow("SELECT state_version, payload FROM runtime_state WHERE singleton=1").Scan(&version, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load harness state: %w", err)
	}
	if version != StateVersion {
		return nil, fmt.Errorf("unsupported persisted state version %d", version)
	}
	if len(payload) > maxStateBytes {
		return nil, errors.New("persisted harness state exceeds the supported size limit")
	}
	return append([]byte(nil), payload...), nil
}

func (s *Store) Save(payload []byte) error {
	if len(payload) > maxStateBytes {
		return errors.New("harness state exceeds the supported size limit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin state transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO runtime_state(singleton, state_version, payload, updated_at)
VALUES(1, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
ON CONFLICT(singleton) DO UPDATE SET state_version=excluded.state_version, payload=excluded.payload, updated_at=excluded.updated_at`, StateVersion, payload); err != nil {
		return fmt.Errorf("save harness state: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }
