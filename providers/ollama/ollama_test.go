package ollama_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vishnuvardhansankoti/ai-nexus/providers/ollama"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

func TestChat_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": "4"},
			"done":              true,
			"prompt_eval_count": 5,
			"eval_count":        1,
		})
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, "llama3.2:3b", 5*time.Second)
	resp, err := c.Chat(context.Background(), sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "what is 2+2"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Content != "4" {
		t.Errorf("expected content %q, got %q", "4", resp.Content)
	}
	if resp.TokensIn != 5 {
		t.Errorf("expected TokensIn=5, got %d", resp.TokensIn)
	}
	if resp.TokensOut != 1 {
		t.Errorf("expected TokensOut=1, got %d", resp.TokensOut)
	}
}

func TestChat_timeout(t *testing.T) {
	// unblock is closed to signal the handler to exit before srv.Close() is called.
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-unblock:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(unblock)
		srv.Close()
	}()

	c := ollama.New(srv.URL, "llama3.2:3b", 50*time.Millisecond)
	_, err := c.Chat(context.Background(), sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
}

func TestChat_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, "missing-model", 5*time.Second)
	_, err := c.Chat(context.Background(), sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error on non-200 status, got nil")
	}
}

func TestPing_up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"models":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, "llama3.2:3b", 5*time.Second)
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("expected nil error from Ping, got: %v", err)
	}
}

func TestPing_down(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, "llama3.2:3b", 5*time.Second)
	if err := c.Ping(context.Background()); err == nil {
		t.Error("expected error from Ping on 503, got nil")
	}
}

func TestChatStream_success(t *testing.T) {
	chunks := []map[string]any{
		{"message": map[string]string{"role": "assistant", "content": "Hello"}, "done": false},
		{"message": map[string]string{"role": "assistant", "content": " world"}, "done": true},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, chunk := range chunks {
			_ = json.NewEncoder(w).Encode(chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, "llama3.2:3b", 5*time.Second)
	out := make(chan string, 10)
	err := c.ChatStream(context.Background(), sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "hi"}},
	}, out)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var got string
	for token := range out {
		got += token
	}
	if got != "Hello world" {
		t.Errorf("expected %q, got %q", "Hello world", got)
	}
}
