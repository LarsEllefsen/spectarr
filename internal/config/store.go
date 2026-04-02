package config

import (
	"database/sql"
	"fmt"
	"strconv"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS config (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS run_log (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	ran_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
	movies_added INTEGER NOT NULL DEFAULT 0,
	error        TEXT
);
`

var defaults = map[string]string{
	"specto_email":              "",
	"specto_password":           "",
	"rating_threshold":          "7.0",
	"radarr_url":                "http://localhost:7878",
	"radarr_api_key":            "",
	"radarr_quality_profile_id": "1",
	"radarr_root_folder_path":   "/movies",
	"poll_interval_minutes":     "60",
}

// sensitiveKeys are encrypted at rest in SQLite.
var sensitiveKeys = map[string]bool{
	"specto_password": true,
	"radarr_api_key":  true,
}

type Store struct {
	db  *sql.DB
	key []byte
}

// Open opens (or creates) the SQLite database at path and loads the
// encryption key from dataDir/secret.key, creating it if absent.
func Open(path, dataDir string) (*Store, error) {
	key, err := loadOrCreateKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("load encryption key: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	s := &Store{db: db, key: key}
	if err := s.seedDefaults(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) seedDefaults() error {
	for k, v := range defaults {
		stored := v
		if sensitiveKeys[k] && v != "" {
			var err error
			stored, err = encrypt(s.key, v)
			if err != nil {
				return fmt.Errorf("encrypt default %q: %w", k, err)
			}
		}
		_, err := s.db.Exec(
			`INSERT INTO config (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
			k, stored,
		)
		if err != nil {
			return fmt.Errorf("seed default %q: %w", k, err)
		}
	}
	return nil
}

// Get returns a config value, decrypting sensitive fields transparently.
func (s *Store) Get(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&v)
	if sensitiveKeys[key] && v != "" {
		plain, err := decrypt(s.key, v)
		if err != nil {
			return ""
		}
		return plain
	}
	return v
}

// Set stores a config value, encrypting sensitive fields transparently.
func (s *Store) Set(key, value string) error {
	stored := value
	if sensitiveKeys[key] && value != "" {
		var err error
		stored, err = encrypt(s.key, value)
		if err != nil {
			return fmt.Errorf("encrypt %q: %w", key, err)
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, stored,
	)
	return err
}

// GetAll returns all config values with sensitive fields decrypted.
func (s *Store) GetAll() map[string]string {
	rows, err := s.db.Query(`SELECT key, value FROM config`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		if sensitiveKeys[k] && v != "" {
			plain, err := decrypt(s.key, v)
			if err == nil {
				v = plain
			}
		}
		m[k] = v
	}
	return m
}

func (s *Store) GetFloat(key string) float64 {
	f, _ := strconv.ParseFloat(s.Get(key), 64)
	return f
}

func (s *Store) GetInt(key string) int {
	i, _ := strconv.Atoi(s.Get(key))
	return i
}

type RunLog struct {
	ID          int64
	RanAt       string
	MoviesAdded int
	Error       string
}

func (s *Store) WriteRunLog(moviesAdded int, runErr error) error {
	var errStr *string
	if runErr != nil {
		s := runErr.Error()
		errStr = &s
	}
	_, err := s.db.Exec(
		`INSERT INTO run_log (movies_added, error) VALUES (?, ?)`,
		moviesAdded, errStr,
	)
	return err
}

func (s *Store) RecentRunLogs(limit int) ([]RunLog, error) {
	rows, err := s.db.Query(
		`SELECT id, datetime(ran_at, 'localtime'), movies_added, COALESCE(error, '')
		 FROM run_log ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []RunLog
	for rows.Next() {
		var l RunLog
		rows.Scan(&l.ID, &l.RanAt, &l.MoviesAdded, &l.Error)
		logs = append(logs, l)
	}
	return logs, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
