package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type store struct {
	db *sql.DB
	mu sync.Mutex
}

func openStore(path string) (*store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS profiles(id TEXT PRIMARY KEY, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY, connection TEXT, at TEXT, data BLOB);
 CREATE INDEX IF NOT EXISTS messages_connection ON messages(connection,id);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}
func (s *store) profiles() ([]profile, error) {
	rows, err := s.db.Query("SELECT data FROM profiles ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []profile
	for rows.Next() {
		var data string
		var p profile
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		p.Password, _ = keyring.Get("mqtt-desktop", p.ID)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *store) save(p profile) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if p.Password != "" {
		if err = keyring.Set("mqtt-desktop", p.ID, p.Password); err != nil {
			return fmt.Errorf("系统凭据保存失败：%w", err)
		}
	} else {
		err = keyring.Delete("mqtt-desktop", p.ID)
		if err != nil && err != keyring.ErrNotFound {
			return err
		}
	}
	_, err = s.db.Exec("INSERT INTO profiles(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", p.ID, string(data))
	return err
}
func (s *store) remove(id string) error {
	_, err := s.db.Exec("DELETE FROM profiles WHERE id=?", id)
	if err == nil {
		_ = keyring.Delete("mqtt-desktop", id)
	}
	return err
}
func (s *store) appendMessages(items []message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range items {
		data, e := json.Marshal(m)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO messages(connection,at,data) VALUES(?,?,?)", m.Connection, m.At.UTC().Format(time.RFC3339Nano), data); e != nil {
			return e
		}
	}
	// Retain at most 10,000 messages globally; the database does not grow without bound.
	if _, err = tx.Exec("DELETE FROM messages WHERE id < (SELECT id FROM messages ORDER BY id DESC LIMIT 1 OFFSET 9999)"); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM messages WHERE id IN (
	 SELECT id FROM (SELECT id, SUM(length(data)) OVER (ORDER BY id DESC) AS total FROM messages) WHERE total > 134217728
	)`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *store) history(id string, before int64) ([]message, int64, error) {
	if before == 0 {
		before = 1 << 62
	}
	rows, err := s.db.Query("SELECT id,data FROM messages WHERE connection=? AND id<? ORDER BY id DESC LIMIT 100", id, before)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []message
	var cursor int64
	bytes := 0
	for rows.Next() {
		var data []byte
		var m message
		var rowID int64
		if err = rows.Scan(&rowID, &data); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(data, &m); err != nil {
			return nil, 0, err
		}
		if bytes+messageBytes(m) > maxCacheBytes {
			break
		}
		bytes += messageBytes(m)
		cursor = rowID
		m.ID = cursor
		items = append(items, m)
	}
	return items, cursor, rows.Err()
}
