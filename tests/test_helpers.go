package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected path to exist: %s: %v", path, err)
	}
}

func mustReadJSONSchema(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read schema %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(content, &out); err != nil {
		t.Fatalf("failed to parse schema %s: %v", path, err)
	}
	return out
}

func migrationSQL(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "db/migrations/0001_foundations.up.sql"))
	if err != nil {
		t.Fatalf("failed to read migration: %v", err)
	}
	return strings.ToLower(string(content))
}
