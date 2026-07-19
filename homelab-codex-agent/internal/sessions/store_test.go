package sessions

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCreatesAndReusesSession(t *testing.T) {
	t.Parallel()
	store := NewStore(filepath.Join(t.TempDir(), "sessions.json"))
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	if _, ok, _, err := store.GetActive("thread-1", "projectego-decompose", "hash", 10, time.Hour, now); err != nil || ok {
		t.Fatalf("GetActive() ok=%v err=%v", ok, err)
	}
	created, err := store.Create("thread-1", "projectego-decompose", "hash", "", 10, now)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, _, err := store.GetActive("thread-1", "projectego-decompose", "hash", 10, time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.ID != created.ID {
		t.Fatalf("session reuse = %#v ok=%v, want %s", got, ok, created.ID)
	}
}

func TestStoreRotatesAfterMaxTurns(t *testing.T) {
	t.Parallel()
	store := NewStore(filepath.Join(t.TempDir(), "sessions.json"))
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	created, err := store.Create("thread-1", "projectego-decompose", "hash", "", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"source_summary":"first turn"}`)
	if _, _, err := store.RecordTurn(created.ID, "", "structured_breakdown", "test", "input", result, nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, ok, summary, err := store.GetActive("thread-1", "projectego-decompose", "hash", 1, time.Hour, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected rotation after max turns")
	}
	if summary == "" {
		t.Fatal("expected rotated session summary")
	}
}
