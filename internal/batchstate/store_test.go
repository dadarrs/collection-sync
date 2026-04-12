package batchstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), ".collection-sync-state.json"))

	completed, err := store.Completed("tv")
	if err != nil {
		t.Fatalf("Completed() error = %v", err)
	}
	if len(completed) != 0 {
		t.Fatalf("Completed() = %v, want empty", completed)
	}

	if err := store.SetCompleted("tv", []string{"tvdb:1"}); err != nil {
		t.Fatalf("SetCompleted() error = %v", err)
	}
	completed, err = store.Completed("tv")
	if err != nil {
		t.Fatalf("Completed() after set error = %v", err)
	}
	if len(completed) != 1 || completed[0] != "tvdb:1" {
		t.Fatalf("Completed() after set = %v, want [tvdb:1]", completed)
	}

	if err := store.SetCompleted("movies", []string{"tmdb:2", "tmdb:3"}); err != nil {
		t.Fatalf("SetCompleted(movies) error = %v", err)
	}
	completed, err = store.Completed("movies")
	if err != nil {
		t.Fatalf("Completed(movies) error = %v", err)
	}
	if len(completed) != 2 || completed[0] != "tmdb:2" || completed[1] != "tmdb:3" {
		t.Fatalf("Completed(movies) = %v, want [tmdb:2 tmdb:3]", completed)
	}

	if err := store.ClearScope("tv"); err != nil {
		t.Fatalf("ClearScope() error = %v", err)
	}
	completed, err = store.Completed("tv")
	if err != nil {
		t.Fatalf("Completed() after clear error = %v", err)
	}
	if len(completed) != 0 {
		t.Fatalf("Completed() after clear = %v, want empty", completed)
	}
}

func TestStoreRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".collection-sync-state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := New(path).Completed("tv")
	if err == nil {
		t.Fatal("Completed() error = nil, want invalid JSON error")
	}
}
