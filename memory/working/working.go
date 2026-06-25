// Package working implements the in-process message buffer and SQLite-backed
// session store for Nexus working memory (Tier 0).
package working

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // SQLite driver registered as "sqlite"

	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// Buffer is a lightweight in-process message store for the active session.
type Buffer struct {
	Messages []sdk.Message
}

// Append adds a message to the buffer.
func (b *Buffer) Append(msg sdk.Message) {
	b.Messages = append(b.Messages, msg)
}

// All returns all buffered messages in order.
func (b *Buffer) All() []sdk.Message {
	return b.Messages
}

// Clear removes all messages from the buffer.
func (b *Buffer) Clear() {
	b.Messages = nil
}

// SessionStore persists messages to a per-session SQLite database file.
type SessionStore struct {
	db        *sql.DB
	sessionID string
}

const createTable = `
CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	role       TEXT    NOT NULL,
	content    TEXT    NOT NULL,
	created_at INTEGER NOT NULL DEFAULT (unixepoch())
);`

// NewSessionStore opens (or creates) the SQLite database for the given session ID.
// The database is stored at ~/.nexus/sessions/<sessionID>.db.
func NewSessionStore(sessionID string) (*SessionStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("working: resolve home dir: %w", err)
	}

	dir := filepath.Join(home, ".nexus", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("working: create sessions dir: %w", err)
	}

	path := filepath.Join(dir, sessionID+".db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("working: open session db %q: %w", path, err)
	}

	if _, err := db.Exec(createTable); err != nil {
		db.Close()
		return nil, fmt.Errorf("working: create messages table: %w", err)
	}

	return &SessionStore{db: db, sessionID: sessionID}, nil
}

// NewSessionStoreAt opens a session store at an explicit path (used in tests).
func NewSessionStoreAt(sessionID, path string) (*SessionStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("working: open session db %q: %w", path, err)
	}
	if _, err := db.Exec(createTable); err != nil {
		db.Close()
		return nil, fmt.Errorf("working: create messages table: %w", err)
	}
	return &SessionStore{db: db, sessionID: sessionID}, nil
}

// Append writes a message to the session database.
func (s *SessionStore) Append(msg sdk.Message) error {
	_, err := s.db.Exec(
		`INSERT INTO messages (role, content, created_at) VALUES (?, ?, unixepoch())`,
		msg.Role, msg.Content,
	)
	if err != nil {
		return fmt.Errorf("working: append message: %w", err)
	}
	return nil
}

// Load reads all messages from the session database in insertion order.
func (s *SessionStore) Load() ([]sdk.Message, error) {
	rows, err := s.db.Query(`SELECT role, content FROM messages ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("working: load messages: %w", err)
	}
	defer rows.Close()

	var msgs []sdk.Message
	for rows.Next() {
		var m sdk.Message
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, fmt.Errorf("working: scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// Close releases the database connection.
func (s *SessionStore) Close() error {
	return s.db.Close()
}
