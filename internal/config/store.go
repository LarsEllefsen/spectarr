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

CREATE TABLE IF NOT EXISTS pending_movies (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	tmdb_id  INTEGER NOT NULL UNIQUE,
	title    TEXT NOT NULL,
	year     INTEGER NOT NULL DEFAULT 0,
	rating   REAL NOT NULL,
	added_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS rejected_movies (
	tmdb_id     INTEGER PRIMARY KEY,
	rejected_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
	"sync_mode":                 "all_friends",
	"selected_friend_ids":       "",
	"download_mode":             "automatic",
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

// PendingMovie is a movie queued for manual review before adding to Radarr.
type PendingMovie struct {
	ID      int64
	TmdbID  int
	Title   string
	Year    int
	Rating  float64
	AddedAt string
}

func (s *Store) AddPendingMovie(tmdbID int, title string, year int, rating float64) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO pending_movies (tmdb_id, title, year, rating) VALUES (?, ?, ?, ?)`,
		tmdbID, title, year, rating,
	)
	return err
}

func (s *Store) GetPendingMovies() ([]PendingMovie, error) {
	rows, err := s.db.Query(
		`SELECT id, tmdb_id, title, year, rating, datetime(added_at, 'localtime')
		 FROM pending_movies ORDER BY added_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var movies []PendingMovie
	for rows.Next() {
		var m PendingMovie
		rows.Scan(&m.ID, &m.TmdbID, &m.Title, &m.Year, &m.Rating, &m.AddedAt)
		movies = append(movies, m)
	}
	return movies, nil
}

func (s *Store) GetPendingMovie(id int64) (PendingMovie, error) {
	var m PendingMovie
	err := s.db.QueryRow(
		`SELECT id, tmdb_id, title, year, rating, datetime(added_at, 'localtime')
		 FROM pending_movies WHERE id = ?`, id,
	).Scan(&m.ID, &m.TmdbID, &m.Title, &m.Year, &m.Rating, &m.AddedAt)
	return m, err
}

func (s *Store) RemovePendingMovie(id int64) error {
	_, err := s.db.Exec(`DELETE FROM pending_movies WHERE id = ?`, id)
	return err
}

func (s *Store) RejectMovie(id int64) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO rejected_movies (tmdb_id)
		SELECT tmdb_id FROM pending_movies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return s.RemovePendingMovie(id)
}

func (s *Store) GetRejectedTmdbIDs() (map[int]struct{}, error) {
	rows, err := s.db.Query(`SELECT tmdb_id FROM rejected_movies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[int]struct{})
	for rows.Next() {
		var id int
		rows.Scan(&id)
		ids[id] = struct{}{}
	}
	return ids, nil
}
