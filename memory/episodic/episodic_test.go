package episodic

import (
	"context"
	"os"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "episodic_*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err := New(f.Name())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenEpisode(t *testing.T) {
	s := newTestStore(t)

	id, err := s.OpenEpisode("sess-1")
	if err != nil {
		t.Fatalf("OpenEpisode: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty episode ID")
	}

	episodes, err := s.QueryByFilter(StatusOpen, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0].ID != id {
		t.Fatalf("expected 1 open episode with id %s, got %+v", id, episodes)
	}
}

func TestAppendEvent(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.OpenEpisode("sess-2")

	err := s.AppendEvent(id, Event{
		Type:      TypeLLMCall,
		Layer:     1,
		Model:     "llama3",
		TokensIn:  10,
		TokensOut: 20,
		Payload:   map[string]any{"prompt": "hello"},
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	// Verify event was written.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE episode_id = ? AND type = ?`, id, TypeLLMCall).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 llm_call event, got %d", count)
	}
}

func TestCloseEpisode(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.OpenEpisode("sess-3")

	// Add a response event so summary extraction has something to work with.
	_ = s.AppendEvent(id, Event{
		Type:    TypeLLMResponse,
		Payload: map[string]any{"content": "The answer is 4."},
	})

	if err := s.CloseEpisode(id, OutcomeSuccess); err != nil {
		t.Fatalf("CloseEpisode: %v", err)
	}

	episodes, err := s.QueryByFilter(StatusClosed, OutcomeSuccess)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 {
		t.Fatalf("expected 1 closed episode, got %d", len(episodes))
	}
	if episodes[0].Summary == "" {
		t.Fatal("expected non-empty summary after close")
	}
}

func TestFindOpenEpisode_Resume(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.OpenEpisode("sess-resume")

	found, ok, err := s.FindOpenEpisode("sess-resume")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected to find open episode")
	}
	if found != id {
		t.Fatalf("expected %s, got %s", id, found)
	}
}

func TestFindOpenEpisode_NoneAfterClose(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.OpenEpisode("sess-4")
	_ = s.CloseEpisode(id, OutcomeSuccess)

	_, ok, err := s.FindOpenEpisode("sess-4")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no open episode after close")
	}
}

func TestQueryByTime(t *testing.T) {
	s := newTestStore(t)

	before := time.Now().Add(-time.Second)
	id, _ := s.OpenEpisode("sess-time")
	after := time.Now().Add(time.Second)

	eps, err := s.QueryByTime(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].ID != id {
		t.Fatalf("expected episode %s in time range, got %+v", id, eps)
	}
}

func TestQueryByFilter_StatusAndOutcome(t *testing.T) {
	s := newTestStore(t)

	id1, _ := s.OpenEpisode("sess-f1")
	id2, _ := s.OpenEpisode("sess-f2")
	_ = s.CloseEpisode(id1, OutcomeSuccess)
	_ = s.CloseEpisode(id2, OutcomeFailure)

	closed, _ := s.QueryByFilter(StatusClosed, "")
	if len(closed) != 2 {
		t.Fatalf("expected 2 closed, got %d", len(closed))
	}

	success, _ := s.QueryByFilter(StatusClosed, OutcomeSuccess)
	if len(success) != 1 {
		t.Fatalf("expected 1 success, got %d", len(success))
	}
}

func TestTimeoutWorker_MarksStaleEpisodes(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.OpenEpisode("sess-stale")

	// Backdate the episode to appear old.
	s.db.Exec(`UPDATE episodes SET created_at = ? WHERE id = ?`, time.Now().Add(-2*time.Hour).Unix(), id)

	// Expire directly (worker is just a ticker wrapper around this).
	s.expireStaleEpisodes(time.Hour)

	eps, _ := s.QueryByFilter(StatusFailed, "")
	if len(eps) != 1 || eps[0].ID != id {
		t.Fatalf("expected stale episode to be marked failed, got %+v", eps)
	}
}

func TestContextEpisodeID(t *testing.T) {
	ctx := context.Background()
	id, ok := EpisodeIDFromContext(ctx)
	if ok || id != "" {
		t.Fatal("expected no episode ID in bare context")
	}

	ctx = WithEpisodeID(ctx, "ep-abc")
	id, ok = EpisodeIDFromContext(ctx)
	if !ok || id != "ep-abc" {
		t.Fatalf("expected ep-abc, got %q ok=%v", id, ok)
	}
}
