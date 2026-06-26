// Package episodic implements the Tier 1 episodic memory store backed by SQLite.
// Every conversation lifecycle is recorded as a causal event log in the episodes
// and events tables, with an FTS5 index over episode titles and summaries.
package episodic

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Episode status values.
const (
	StatusOpen   = "open"
	StatusClosed = "closed"
	StatusFailed = "failed"
)

// Episode outcome values.
const (
	OutcomeSuccess   = "success"
	OutcomeFailure   = "failure"
	OutcomePartial   = "partial"
	OutcomeEscalated = "escalated"
)

// Event type constants emitted by orchestrator, providers, and governance.
const (
	TypeEpisodeOpen       = "episode_open"
	TypeEpisodeClose      = "episode_close"
	TypeOrchestratorRoute = "orchestrator_route"
	TypeLLMCall           = "llm_call"
	TypeLLMResponse       = "llm_response"
	TypeGovernancePass    = "governance_pass"
	TypeGovernanceReject  = "governance_reject"
)

const ddl = `
CREATE TABLE IF NOT EXISTS episodes (
  id          TEXT    PRIMARY KEY,
  title       TEXT    NOT NULL DEFAULT '',
  status      TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  closed_at   INTEGER,
  session_ids TEXT    NOT NULL DEFAULT '[]',
  tags        TEXT    NOT NULL DEFAULT '[]',
  outcome     TEXT    NOT NULL DEFAULT '',
  summary     TEXT    NOT NULL DEFAULT '',
  embedding   BLOB
);

CREATE TABLE IF NOT EXISTS events (
  id          TEXT    PRIMARY KEY,
  episode_id  TEXT    NOT NULL REFERENCES episodes(id),
  parent_id   TEXT,
  timestamp   INTEGER NOT NULL,
  type        TEXT    NOT NULL,
  layer       INTEGER NOT NULL DEFAULT 0,
  model       TEXT    NOT NULL DEFAULT '',
  latency_ms  INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  payload     TEXT    NOT NULL DEFAULT '{}'
);

CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts
  USING fts5(title, summary, content=episodes);
`

// Episode is a recorded conversation episode stored in SQLite.
type Episode struct {
	ID         string
	Title      string
	Status     string
	CreatedAt  int64
	ClosedAt   *int64
	SessionIDs []string
	Tags       []string
	Outcome    string
	Summary    string
}

// Event is a single atomic action within an episode.
type Event struct {
	// ID is auto-generated if empty.
	ID string
	// EpisodeID must be set by the caller.
	EpisodeID string
	ParentID  string
	// Timestamp is set to now (ms) if zero.
	Timestamp int64
	Type      string
	Layer     int
	Model     string
	LatencyMs int64
	TokensIn  int
	TokensOut int
	Payload   map[string]any
}

// Store is the episodic memory backend.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the episodic SQLite database at path.
func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("episodic: create dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("episodic: open db %q: %w", path, err)
	}
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("episodic: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// DefaultDBPath returns the default episodic DB path: ~/.nexus/episodic.db.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nexus", "episodic.db")
}

// OpenEpisode creates a new episode linked to sessionID, writes an episode_open
// event, and returns the new episode ID.
func (s *Store) OpenEpisode(sessionID string) (string, error) {
	id := uuid.New().String()
	sessionIDs, _ := json.Marshal([]string{sessionID})
	now := time.Now().Unix()

	_, err := s.db.Exec(
		`INSERT INTO episodes (id, status, created_at, session_ids) VALUES (?, ?, ?, ?)`,
		id, StatusOpen, now, string(sessionIDs),
	)
	if err != nil {
		return "", fmt.Errorf("episodic: insert episode: %w", err)
	}

	if err := s.AppendEvent(id, Event{
		Type:    TypeEpisodeOpen,
		Payload: map[string]any{"session_id": sessionID},
	}); err != nil {
		return "", fmt.Errorf("episodic: write episode_open event: %w", err)
	}
	return id, nil
}

// FindOpenEpisode returns the episode ID of the open episode associated with
// sessionID. Returns ("", false, nil) when none exists.
func (s *Store) FindOpenEpisode(sessionID string) (string, bool, error) {
	// session_ids is a JSON array; LIKE-based match is sufficient for v1 (UUIDs
	// won't produce false positives).
	row := s.db.QueryRow(
		`SELECT id FROM episodes WHERE status = ? AND session_ids LIKE ? LIMIT 1`,
		StatusOpen, "%"+sessionID+"%",
	)
	var id string
	if err := row.Scan(&id); err == sql.ErrNoRows {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("episodic: find open episode: %w", err)
	}
	return id, true, nil
}

// AppendEvent inserts one event row. ID and Timestamp are auto-populated if zero.
func (s *Store) AppendEvent(episodeID string, ev Event) error {
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	payload, _ := json.Marshal(ev.Payload)

	_, err := s.db.Exec(
		`INSERT INTO events
		   (id, episode_id, parent_id, timestamp, type, layer, model, latency_ms, tokens_in, tokens_out, payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, episodeID, nullableString(ev.ParentID), ev.Timestamp,
		ev.Type, ev.Layer, ev.Model, ev.LatencyMs,
		ev.TokensIn, ev.TokensOut, string(payload),
	)
	if err != nil {
		return fmt.Errorf("episodic: insert event (type=%s): %w", ev.Type, err)
	}
	return nil
}

// CloseEpisode marks the episode closed, writes a keyword summary, and appends
// an episode_close event.
func (s *Store) CloseEpisode(episodeID, outcome string) error {
	summary := s.extractSummary(episodeID)
	now := time.Now().Unix()

	_, err := s.db.Exec(
		`UPDATE episodes SET status = ?, closed_at = ?, outcome = ?, summary = ? WHERE id = ?`,
		StatusClosed, now, outcome, summary, episodeID,
	)
	if err != nil {
		return fmt.Errorf("episodic: close episode: %w", err)
	}
	return s.AppendEvent(episodeID, Event{
		Type:    TypeEpisodeClose,
		Payload: map[string]any{"outcome": outcome, "summary": summary},
	})
}

// QueryByTime returns episodes whose created_at falls within [from, to] (inclusive).
func (s *Store) QueryByTime(from, to time.Time) ([]Episode, error) {
	rows, err := s.db.Query(
		`SELECT id, title, status, created_at, closed_at, session_ids, tags, outcome, summary
		   FROM episodes
		  WHERE created_at >= ? AND created_at <= ?
		  ORDER BY created_at ASC`,
		from.Unix(), to.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("episodic: query by time: %w", err)
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

// QueryByFilter returns episodes matching the given status and/or outcome.
// Empty strings act as wildcards.
func (s *Store) QueryByFilter(status, outcome string) ([]Episode, error) {
	q := `SELECT id, title, status, created_at, closed_at, session_ids, tags, outcome, summary
	        FROM episodes WHERE 1=1`
	var args []any
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	if outcome != "" {
		q += " AND outcome = ?"
		args = append(args, outcome)
	}
	q += " ORDER BY created_at DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("episodic: query by filter: %w", err)
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

// StartTimeoutWorker starts a background goroutine that marks open episodes
// older than timeout as failed. It stops when ctx is cancelled.
// If timeout is zero the worker does nothing.
func (s *Store) StartTimeoutWorker(ctx context.Context, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	interval := max(timeout/2, time.Minute)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.expireStaleEpisodes(timeout)
			}
		}
	}()
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- internal helpers ---

func (s *Store) expireStaleEpisodes(timeout time.Duration) {
	cutoff := time.Now().Add(-timeout).Unix()
	if _, err := s.db.Exec(
		`UPDATE episodes SET status = ?, outcome = ? WHERE status = ? AND created_at < ?`,
		StatusFailed, OutcomeFailure, StatusOpen, cutoff,
	); err != nil {
		slog.Default().Warn("episodic: expire stale episodes", "err", err)
	}
}

// extractSummary builds a simple keyword summary from llm_response payloads.
// In v2 this is replaced with an LLM-based extractor.
func (s *Store) extractSummary(episodeID string) string {
	rows, err := s.db.Query(
		`SELECT payload FROM events WHERE episode_id = ? AND type = ? ORDER BY timestamp ASC`,
		episodeID, TypeLLMResponse,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		if content, ok := m["content"].(string); ok && content != "" {
			snippet := content
			if len(snippet) > 100 {
				snippet = snippet[:100]
			}
			parts = append(parts, strings.TrimSpace(snippet))
		}
	}
	return strings.Join(parts, " | ")
}

func scanEpisodes(rows *sql.Rows) ([]Episode, error) {
	var out []Episode
	for rows.Next() {
		var ep Episode
		var closedAt sql.NullInt64
		var sessionIDsJSON, tagsJSON string

		if err := rows.Scan(
			&ep.ID, &ep.Title, &ep.Status, &ep.CreatedAt, &closedAt,
			&sessionIDsJSON, &tagsJSON, &ep.Outcome, &ep.Summary,
		); err != nil {
			return nil, fmt.Errorf("episodic: scan row: %w", err)
		}
		if closedAt.Valid {
			ep.ClosedAt = &closedAt.Int64
		}
		_ = json.Unmarshal([]byte(sessionIDsJSON), &ep.SessionIDs)
		_ = json.Unmarshal([]byte(tagsJSON), &ep.Tags)
		out = append(out, ep)
	}
	return out, rows.Err()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
