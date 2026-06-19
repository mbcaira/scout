package store

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Job struct {
	ID          string
	Company     string
	Title       string
	URL         string
	Description string
	Location    string
	Score       int
	Status      string // "pending", "applied", "skipped"
	AutoApply   bool
	SeenAt      time.Time
	AppliedAt   *time.Time
}

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id          TEXT PRIMARY KEY,
			company     TEXT NOT NULL,
			title       TEXT NOT NULL,
			url         TEXT NOT NULL,
			description TEXT,
			location    TEXT,
			score       INTEGER DEFAULT 0,
			status      TEXT DEFAULT 'pending',
			seen_at     DATETIME NOT NULL,
			applied_at  DATETIME
		)
	`)
	return err
}

func (s *Store) Seen(id string) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE id = ?", id).Scan(&count)
	return count > 0, err
}

func (s *Store) Save(j Job) error {
	_, err := s.db.Exec(`
		INSERT INTO jobs (id, company, title, url, description, location, score, status, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, j.ID, j.Company, j.Title, j.URL, j.Description, j.Location, j.Score, j.Status, j.SeenAt)
	return err
}

func (s *Store) Upsert(j Job) error {
	_, err := s.db.Exec(`
		INSERT INTO jobs (id, company, title, url, description, location, score, status, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			description = excluded.description,
			score       = excluded.score,
			status      = excluded.status
	`, j.ID, j.Company, j.Title, j.URL, j.Description, j.Location, j.Score, j.Status, j.SeenAt)
	return err
}

func (s *Store) Get(id string) (Job, error) {
	var j Job
	err := s.db.QueryRow(`
		SELECT id, company, title, url, description, location, score, status, seen_at
		FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Company, &j.Title, &j.URL, &j.Description, &j.Location, &j.Score, &j.Status, &j.SeenAt)
	return j, err
}

func (s *Store) Pending() ([]Job, error) {
	return s.ByStatus("pending")
}

func (s *Store) ByStatus(status string) ([]Job, error) {
	var rows *sql.Rows
	var err error
	if status == "all" {
		rows, err = s.db.Query(`SELECT id, company, title, url, description, location, score, status, seen_at FROM jobs ORDER BY seen_at DESC`)
	} else {
		rows, err = s.db.Query(`SELECT id, company, title, url, description, location, score, status, seen_at FROM jobs WHERE status = ? ORDER BY seen_at DESC`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Company, &j.Title, &j.URL, &j.Description, &j.Location, &j.Score, &j.Status, &j.SeenAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (s *Store) UpdateStatus(id, status string) error {
	if status == "applied" {
		now := time.Now()
		_, err := s.db.Exec("UPDATE jobs SET status = ?, applied_at = ? WHERE id = ?", status, now, id)
		return err
	}
	_, err := s.db.Exec("UPDATE jobs SET status = ? WHERE id = ?", status, id)
	return err
}

func (s *Store) Close() error {
	return s.db.Close()
}
