package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	// 使用 mode=rwc 确保以读写创建模式打开数据库，解决只读错误
	db, err := sql.Open("sqlite", dbPath+"?mode=rwc")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// 设置 PRAGMA 选项
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL;`); err != nil {
		return nil, fmt.Errorf("set synchronous: %w", err)
	}
	if _, err := db.Exec(`PRAGMA temp_store=MEMORY;`); err != nil {
		return nil, fmt.Errorf("set temp_store: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	schema := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			count INTEGER NOT NULL,
			received_ts INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);`,
		`CREATE INDEX IF NOT EXISTS idx_events_count ON events(count);`,
		`CREATE TABLE IF NOT EXISTS settings (
			k TEXT PRIMARY KEY,
			v TEXT NOT NULL
		);`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			return nil, fmt.Errorf("init schema: %w", err)
		}
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v;`, key, value)
	return err
}

func (s *Store) GetSetting(key string, def string) (string, error) {
	row := s.db.QueryRow(`SELECT v FROM settings WHERE k=?;`, key)
	var v string
	switch err := row.Scan(&v); err {
	case nil:
		return v, nil
	case sql.ErrNoRows:
		return def, nil
	default:
		return def, err
	}
}

func (s *Store) InsertEvent(ts int64, count int64) error {
	_, err := s.db.Exec(`INSERT INTO events(ts, count, received_ts) VALUES(?, ?, ?);`, ts, count, time.Now().Unix())
	return err
}

func (s *Store) FetchLatestEvent() (int64, int64, error) {
	row := s.db.QueryRow(`SELECT ts, count FROM events ORDER BY ts DESC, id DESC LIMIT 1;`)
	var ts int64
	var count int64
	if err := row.Scan(&ts, &count); err != nil {
		return 0, 0, err
	}
	return ts, count, nil
}

func (s *Store) FetchPrevCountBefore(tsExclusive int64) (*int64, error) {
	row := s.db.QueryRow(`SELECT count FROM events WHERE ts < ? ORDER BY ts DESC, id DESC LIMIT 1;`, tsExclusive)
	var count int64
	if err := row.Scan(&count); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &count, nil
}

func (s *Store) FetchEventsInRange(startTS, endTS int64) ([]Event, error) {
	rows, err := s.db.Query(`SELECT ts, count FROM events WHERE ts >= ? AND ts < ? ORDER BY ts ASC, id ASC;`, startTS, endTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Timestamp, &ev.Count); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *Store) FetchAllEvents() ([]Event, error) {
	rows, err := s.db.Query(`SELECT ts, count FROM events ORDER BY ts ASC, id ASC;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Timestamp, &ev.Count); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *Store) FetchRecentEvents(limit int) ([]Event, error) {
	rows, err := s.db.Query(`SELECT ts, count FROM events ORDER BY ts DESC, id DESC LIMIT ?;`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Timestamp, &ev.Count); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}