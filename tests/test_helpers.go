package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const smokeDBName = "heartbeat_smoke"

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

func runCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %s %s\n%s\nERR: %v", name, strings.Join(args, " "), string(output), err)
	}
	return string(output)
}

type postgresStack struct {
	Root    string
	Compose string
	DBName  string
}

func newPostgresStack(t *testing.T, root string) *postgresStack {
	t.Helper()
	stack := &postgresStack{
		Root:    root,
		Compose: filepath.Join(root, "infra/docker-compose.yml"),
		DBName:  smokeDBName,
	}
	stack.ensureDockerComposeConfig(t)
	stack.start(t)
	t.Cleanup(func() {
		stack.stop(t)
	})
	return stack
}

func (s *postgresStack) ensureDockerComposeConfig(t *testing.T) {
	t.Helper()
	runCommand(t, s.Root, "docker", "compose", "-f", s.Compose, "config")
}

func (s *postgresStack) start(t *testing.T) {
	t.Helper()
	runCommand(t, s.Root, "docker", "compose", "-f", s.Compose, "up", "-d", "postgres")
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.Command("docker", "compose", "-f", s.Compose, "exec", "-T", "postgres", "pg_isready", "-U", "heartbeat", "-d", "heartbeat")
		cmd.Dir = s.Root
		if err := cmd.Run(); err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("postgres did not become ready before timeout")
}

func (s *postgresStack) stop(t *testing.T) {
	t.Helper()
	cmd := exec.Command("docker", "compose", "-f", s.Compose, "down", "-v")
	cmd.Dir = s.Root
	_ = cmd.Run()
}

func (s *postgresStack) ResetDatabase(t *testing.T) {
	t.Helper()
	s.PSQL(t, "postgres", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE);", s.DBName))
	s.PSQL(t, "postgres", fmt.Sprintf("CREATE DATABASE %s;", s.DBName))
}

func (s *postgresStack) ApplyMigration(t *testing.T, migrationPath string) string {
	t.Helper()
	return s.psqlFile(t, s.DBName, migrationPath)
}

func (s *postgresStack) PSQL(t *testing.T, database string, sql string) string {
	t.Helper()
	return runCommand(t, s.Root,
		"docker", "compose", "-f", s.Compose,
		"exec", "-T", "postgres",
		"psql", "-U", "heartbeat", "-d", database,
		"-v", "ON_ERROR_STOP=1",
		"-c", sql,
	)
}

func (s *postgresStack) psqlFile(t *testing.T, database string, filePath string) string {
	t.Helper()
	return runCommand(t, s.Root,
		"docker", "compose", "-f", s.Compose,
		"exec", "-T", "postgres",
		"psql", "-U", "heartbeat", "-d", database,
		"-v", "ON_ERROR_STOP=1",
		"-f", filePath,
	)
}
