package working_test

import (
	"path/filepath"
	"testing"

	"github.com/vishnuvardhansankoti/ai-nexus/memory/working"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

func TestBuffer_appendAndClear(t *testing.T) {
	var buf working.Buffer

	buf.Append(sdk.Message{Role: "user", Content: "hello"})
	buf.Append(sdk.Message{Role: "assistant", Content: "hi"})

	if len(buf.All()) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(buf.All()))
	}
	if buf.All()[0].Role != "user" {
		t.Errorf("expected first message role=user, got %q", buf.All()[0].Role)
	}

	buf.Clear()
	if len(buf.All()) != 0 {
		t.Errorf("expected 0 messages after Clear, got %d", len(buf.All()))
	}
}

func TestSessionStore_appendAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-session.db")

	store, err := working.NewSessionStoreAt("test-session", path)
	if err != nil {
		t.Fatalf("NewSessionStoreAt: %v", err)
	}
	defer store.Close()

	msgs := []sdk.Message{
		{Role: "user", Content: "what is 2+2"},
		{Role: "assistant", Content: "4"},
	}
	for _, m := range msgs {
		if err := store.Append(m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	for i, want := range msgs {
		if loaded[i].Role != want.Role || loaded[i].Content != want.Content {
			t.Errorf("message[%d]: want {%q %q}, got {%q %q}",
				i, want.Role, want.Content, loaded[i].Role, loaded[i].Content)
		}
	}
}

func TestSessionStore_emptyLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-session.db")
	store, err := working.NewSessionStoreAt("empty", path)
	if err != nil {
		t.Fatalf("NewSessionStoreAt: %v", err)
	}
	defer store.Close()

	msgs, err := store.Load()
	if err != nil {
		t.Fatalf("Load on empty store: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}
