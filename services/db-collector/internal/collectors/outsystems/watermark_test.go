package outsystems

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreSavesAndLoadsWatermarks(t *testing.T) {
	dir := t.TempDir()
	key := WatermarkKey{Collector: "oslogs", Target: "logging-db", LogType: "error"}
	saved := Watermark{Instant: time.Date(2026, 10, 10, 12, 0, 0, 3_000_000, time.UTC), InstantType: "DATETIME", Source: "db:1433/OSLogging"}
	if err := NewStore(dir).Save(key, saved, time.Now()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A new store, as after a restart, reads the file.
	got, found, err := NewStore(dir).Load(key)
	if err != nil || !found || got != saved {
		t.Fatalf("Load = %+v, %v, %v; want %+v", got, found, err, saved)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "oslogs.logging-db.error.json" {
		t.Fatalf("state dir holds %v; want only the watermark file, no temporary files", entries)
	}
	content, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(content), `"instant": "2026-10-10T12:00:00.003"`) {
		t.Fatalf("file content:\n%s", content)
	}
	if _, found, err := NewStore(dir).Load(WatermarkKey{Collector: "oslogs", Target: "logging-db", LogType: "general"}); found || err != nil {
		t.Fatalf("missing watermark: found %v, err %v", found, err)
	}
}

func TestStoreReportsCorruptFiles(t *testing.T) {
	key := WatermarkKey{Collector: "c", Target: "t", LogType: "error"}
	for name, content := range map[string]string{
		"not JSON":      "{",
		"truncated":     `{"version": 1, "instant": "2026-10-`,
		"bad instant":   `{"version": 1, "instant": "yesterday"}`,
		"other version": `{"version": 2, "instant": "2026-10-10T12:00:00"}`,
	} {
		t.Run(name, func(t *testing.T) {
			store := NewStore(t.TempDir())
			if err := os.WriteFile(store.path(key), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.Load(key); err == nil || found {
				t.Fatalf("Load: found %v, err %v; want an error", found, err)
			}
		})
	}
}

// A failed write keeps the new watermark in memory, so delivered rows are
// not sent again, and reports the error.
func TestStoreKeepsTheWatermarkInMemoryWhenTheFileCannotBeWritten(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "state")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(blocker, "nested"))
	key := WatermarkKey{Collector: "c", Target: "t", LogType: "error"}
	saved := Watermark{Instant: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := store.Save(key, saved, time.Now()); err == nil {
		t.Fatal("Save into a file path succeeded")
	}
	if got, found, err := store.Load(key); err != nil || !found || got != saved {
		t.Fatalf("Load = %+v, %v, %v; want the unsaved watermark", got, found, err)
	}
}

func TestStoreRememberIsMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	key := WatermarkKey{Collector: "c", Target: "t", LogType: "error"}
	store := NewStore(dir)
	store.Remember(key, Watermark{Instant: time.Unix(0, 0).UTC()})
	if _, found, _ := store.Load(key); !found {
		t.Fatal("remembered watermark not found")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("Remember wrote %v", entries)
	}
}

// Names come from the config and may hold any character; the file must stay
// in the store's directory and distinct names must not share a file.
func TestStorePathEscapesNames(t *testing.T) {
	store := NewStore("/state")
	paths := map[string]WatermarkKey{}
	for _, key := range []WatermarkKey{
		{Collector: "../..", Target: "t", LogType: "error"},
		{Collector: "a.b", Target: "c", LogType: "error"},
		{Collector: "a", Target: "b.c", LogType: "error"},
		{Collector: `a\b`, Target: "c/d", LogType: "error"},
		{Collector: "a%2Eb", Target: "c", LogType: "error"},
	} {
		path := store.path(key)
		if filepath.Dir(path) != "/state" {
			t.Errorf("%+v maps to %s, outside the store", key, path)
		}
		if previous, ok := paths[path]; ok {
			t.Errorf("%+v and %+v share %s", key, previous, path)
		}
		paths[path] = key
	}
}
