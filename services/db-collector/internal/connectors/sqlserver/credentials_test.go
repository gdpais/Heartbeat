package sqlserver

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSecretVolume lays out dir as the kubelet lays out a Secret volume:
// the keys as files in a hidden version directory, a ..data symlink to it,
// and one symlink per key through ..data.  Called again, it writes a new
// version, swaps ..data with a rename, links new keys, unlinks removed ones
// and deletes the old version, as the kubelet does on a Secret update.
func writeSecretVolume(t *testing.T, dir string, data map[string]string) {
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

func mustResolve(t *testing.T, r CredentialResolver, ref string) Credential {
	t.Helper()
	creds, err := r.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", ref, err)
	}
	return creds
}

func TestCredentialKey(t *testing.T) {
	for ref, want := range map[string]string{
		"env/sqlserver-b":         "HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_B",
		"kv/sqlserver-default":    "HEARTBEAT_CREDENTIAL_KV_SQLSERVER_DEFAULT",
		"secret/finance.prod-ro":  "HEARTBEAT_CREDENTIAL_SECRET_FINANCE_PROD_RO",
		"env/Mixed/Case_name-1.2": "HEARTBEAT_CREDENTIAL_ENV_MIXED_CASE_NAME_1_2",
	} {
		if got := credentialKey(ref); got != want {
			t.Errorf("credentialKey(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestEnvCredentialResolver(t *testing.T) {
	t.Setenv("HEARTBEAT_CREDENTIAL_KV_SQLSERVER_DEFAULT", "collector:pass:with:colons")
	t.Setenv("HEARTBEAT_CREDENTIAL_ENV_MALFORMED", "no-separator-"+testPassword)

	got := mustResolve(t, EnvCredentialResolver{}, "kv/sqlserver-default")
	if want := (Credential{Username: "collector", Password: "pass:with:colons"}); got != want {
		t.Fatalf("Resolve = %+v, want %+v (split at the first colon)", got, want)
	}
	if _, err := (EnvCredentialResolver{}).Resolve(context.Background(), "env/absent"); err == nil || !strings.Contains(err.Error(), "missing credential for ref env/absent") {
		t.Fatalf("absent variable: err = %v", err)
	}
	_, err := (EnvCredentialResolver{}).Resolve(context.Background(), "env/malformed")
	if err == nil || !strings.Contains(err.Error(), "must be username:password") {
		t.Fatalf("malformed variable: err = %v", err)
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatalf("error quotes the value: %v", err)
	}
}

// A rotated, added or removed Secret key is seen by the next Resolve of the
// same resolver: nothing is cached at startup.
func TestFileCredentialResolverFollowsSecretUpdates(t *testing.T) {
	dir := t.TempDir()
	writeSecretVolume(t, dir, map[string]string{
		"HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_A": "collector:old-password\n",
	})
	r := FileCredentialResolver{Dir: dir}

	if got := mustResolve(t, r, "env/sqlserver-a"); got != (Credential{Username: "collector", Password: "old-password"}) {
		t.Fatalf("before rotation: %+v", got)
	}

	writeSecretVolume(t, dir, map[string]string{
		"HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_A": "collector:new-password\n",
		"HEARTBEAT_CREDENTIAL_KV_SQLSERVER_B":  "reader:b-password",
	})
	if got := mustResolve(t, r, "env/sqlserver-a"); got != (Credential{Username: "collector", Password: "new-password"}) {
		t.Fatalf("after rotation: %+v", got)
	}
	if got := mustResolve(t, r, "kv/sqlserver-b"); got != (Credential{Username: "reader", Password: "b-password"}) {
		t.Fatalf("added key: %+v", got)
	}

	writeSecretVolume(t, dir, map[string]string{
		"HEARTBEAT_CREDENTIAL_KV_SQLSERVER_B": "reader:b-password",
	})
	if _, err := r.Resolve(context.Background(), "env/sqlserver-a"); err == nil || !strings.Contains(err.Error(), "missing credential for ref env/sqlserver-a") {
		t.Fatalf("removed key: err = %v", err)
	}
}

// Manager.Open resolves on every call, so after a rotation the next Open
// connects with the new credential, in a new pool, without a restart.
func TestOpenUsesRotatedCredentialFile(t *testing.T) {
	dir := t.TempDir()
	writeSecretVolume(t, dir, map[string]string{
		"HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_A": "collector:" + testPassword + "\n",
	})
	d, name := newFakeDriver(t)
	m := NewManager(FileCredentialResolver{Dir: dir})
	m.driverName = name
	t.Cleanup(func() { _ = m.Close() })
	tg := target("a", "db-a.example", "env/sqlserver-a")

	db1, c1 := mustOpen(t, m, tg)
	c1()
	if !strings.Contains(d.lastDSN(), url.UserPassword("collector", testPassword).String()+"@") {
		t.Fatal("first connection does not use the mounted credential")
	}

	rotated := testPassword + "-rotated"
	writeSecretVolume(t, dir, map[string]string{
		"HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_A": "collector:" + rotated + "\n",
	})
	db2, c2 := mustOpen(t, m, tg)
	defer c2()
	if db2 == db1 {
		t.Fatal("rotated credential reused the old pool")
	}
	if !strings.Contains(d.lastDSN(), url.UserPassword("collector", rotated).String()+"@") {
		t.Fatal("connection after rotation does not use the rotated credential")
	}
}

func TestFileCredentialResolverFallsBack(t *testing.T) {
	const ref = "env/sqlserver-a"
	t.Setenv("HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_A", "env-user:env-password")
	fromEnv := Credential{Username: "env-user", Password: "env-password"}
	withFile := t.TempDir()
	writeSecretVolume(t, withFile, map[string]string{
		"HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_A": "file-user:file-password",
	})
	withoutFile := t.TempDir()
	writeSecretVolume(t, withoutFile, map[string]string{
		"HEARTBEAT_CREDENTIAL_ENV_OTHER": "other:other-password",
	})

	for _, tc := range []struct {
		name string
		dir  string
		want Credential
	}{
		{"no directory configured", "", fromEnv},
		{"directory does not exist", filepath.Join(t.TempDir(), "absent"), fromEnv},
		{"no file for the ref", withoutFile, fromEnv},
		{"file wins over the environment", withFile, Credential{Username: "file-user", Password: "file-password"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := FileCredentialResolver{Dir: tc.dir, Fallback: EnvCredentialResolver{}}
			if got := mustResolve(t, r, ref); got != tc.want {
				t.Fatalf("Resolve = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("no fallback", func(t *testing.T) {
		_, err := FileCredentialResolver{Dir: withoutFile}.Resolve(context.Background(), ref)
		if err == nil || !strings.Contains(err.Error(), "missing credential for ref "+ref) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFileCredentialResolverValues(t *testing.T) {
	for value, want := range map[string]Credential{
		"collector:" + testPassword:            {Username: "collector", Password: testPassword},
		"collector:" + testPassword + "\n":     {Username: "collector", Password: testPassword},
		"collector:" + testPassword + "\r\n":   {Username: "collector", Password: testPassword},
		"collector:pass:with:colons\n":         {Username: "collector", Password: "pass:with:colons"},
		"collector: spaces are kept \n":        {Username: "collector", Password: " spaces are kept "},
		`DOMAIN\collector:` + testPassword:     {Username: `DOMAIN\collector`, Password: testPassword},
		"collector:" + testPassword + "\n\n\n": {Username: "collector", Password: testPassword},
	} {
		dir := t.TempDir()
		writeSecretVolume(t, dir, map[string]string{"HEARTBEAT_CREDENTIAL_ENV_A": value})
		if got := mustResolve(t, FileCredentialResolver{Dir: dir}, "env/a"); got != want {
			t.Errorf("file %q: Resolve = %+v, want %+v", value, got, want)
		}
	}
}

// A file that is there but unusable is an error, never a reason to use the
// environment's credential instead, and the error never quotes the file.
func TestFileCredentialResolverDoesNotFallBackOnBadFiles(t *testing.T) {
	t.Setenv("HEARTBEAT_CREDENTIAL_ENV_A", "env-user:env-password")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside:"+testPassword), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{
			name: "no colon",
			setup: func(t *testing.T, dir string) {
				writeSecretVolume(t, dir, map[string]string{"HEARTBEAT_CREDENTIAL_ENV_A": "collector" + testPassword})
			},
			want: "credential env/a must be username:password",
		},
		{
			name: "empty file",
			setup: func(t *testing.T, dir string) {
				writeSecretVolume(t, dir, map[string]string{"HEARTBEAT_CREDENTIAL_ENV_A": ""})
			},
			want: "credential env/a must be username:password",
		},
		{
			// os.Root refuses symlinks that leave the directory.
			name: "symlink out of the directory",
			setup: func(t *testing.T, dir string) {
				if err := os.Symlink(outside, filepath.Join(dir, "HEARTBEAT_CREDENTIAL_ENV_A")); err != nil {
					t.Fatal(err)
				}
			},
			want: "read credential for ref env/a",
		},
		{
			name: "unreadable entry",
			setup: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "HEARTBEAT_CREDENTIAL_ENV_A"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "read credential for ref env/a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			r := FileCredentialResolver{Dir: dir, Fallback: EnvCredentialResolver{}}
			creds, err := r.Resolve(context.Background(), "env/a")
			if err == nil {
				t.Fatalf("Resolve = %+v, want an error", creds)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Fatalf("error quotes the credential: %v", err)
			}
		})
	}
}
