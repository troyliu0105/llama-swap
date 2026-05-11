package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureStore_AppendAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captures.bin")
	store := newCaptureStore(path, 5120, testLogger)

	entries := []captureEntry{
		{ID: 1, Data: []byte("first")},
		{ID: 2, Data: []byte("second")},
		{ID: 42, Data: []byte{0, 1, 2, 3}},
	}
	for _, entry := range entries {
		if err := store.Append(entry.ID, entry.Data); err != nil {
			t.Fatalf("Append(%d) failed: %v", entry.ID, err)
		}
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("expected %d entries, got %d", len(entries), len(loaded))
	}
	for i := range entries {
		if loaded[i].ID != entries[i].ID {
			t.Errorf("entry %d ID = %d, want %d", i, loaded[i].ID, entries[i].ID)
		}
		if !bytes.Equal(loaded[i].Data, entries[i].Data) {
			t.Errorf("entry %d data = %v, want %v", i, loaded[i].Data, entries[i].Data)
		}
	}
}

func TestCaptureStore_LoadNonExistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.bin")
	store := newCaptureStore(path, 5120, testLogger)

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected no entries, got %d", len(loaded))
	}
}

func TestCaptureStore_Compact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captures.bin")
	store := newCaptureStore(path, 3, testLogger)

	for i := 1; i <= 5; i++ {
		if err := store.Append(i, []byte{byte(i)}); err != nil {
			t.Fatalf("Append(%d) failed: %v", i, err)
		}
	}
	if err := store.Compact(); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(loaded))
	}
	for i, entry := range loaded {
		wantID := i + 3
		if entry.ID != wantID {
			t.Errorf("entry %d ID = %d, want %d", i, entry.ID, wantID)
		}
		if !bytes.Equal(entry.Data, []byte{byte(wantID)}) {
			t.Errorf("entry %d data = %v, want %v", i, entry.Data, []byte{byte(wantID)})
		}
	}
}

func TestCaptureStore_AppendCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captures.bin")
	store := newCaptureStore(path, 5120, testLogger)

	if err := store.Append(7, []byte("capture")); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected capture file to exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected capture file to be non-empty")
	}
}
