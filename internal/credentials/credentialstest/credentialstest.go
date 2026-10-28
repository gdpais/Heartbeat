// Package credentialstest helps tests lay out credential files the way
// Kubernetes mounts a Secret.
package credentialstest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteSecretVolume lays out dir as the kubelet lays out a Secret volume:
// the keys as files in a hidden version directory, a ..data symlink to it,
// and one symlink per key through ..data. Called again, it writes a new
// version, swaps ..data with a rename, links new keys, unlinks removed ones
// and deletes the old version, as the kubelet does on a Secret update.
func WriteSecretVolume(t testing.TB, dir string, data map[string]string) {
	t.Helper()
	version, err := os.MkdirTemp(dir, "..version_")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range data {
		if err := os.WriteFile(filepath.Join(version, key), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dataLink := filepath.Join(dir, "..data")
	previous, _ := os.Readlink(dataLink)
	tmp := filepath.Join(dir, "..data_tmp")
	if err := os.Symlink(filepath.Base(version), tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, dataLink); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, keep := data[entry.Name()]; !keep && !strings.HasPrefix(entry.Name(), "..") {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	for key := range data {
		link := filepath.Join(dir, key)
		if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) {
			if err := os.Symlink(filepath.Join("..data", key), link); err != nil {
				t.Fatal(err)
			}
		}
	}
	if previous != "" {
		if err := os.RemoveAll(filepath.Join(dir, previous)); err != nil {
			t.Fatal(err)
		}
	}
}
