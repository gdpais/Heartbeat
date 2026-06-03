package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUpMigrationCreatesCoreTables(t *testing.T) {
	root := repoRoot(t)
	stack := newPostgresStack(t, root)
	stack.ResetDatabase(t)
	stack.ApplyMigration(t, "/migrations/0001_foundations.up.sql")

	stdout := stack.PSQL(t, stack.DBName, `
select string_agg(name, ',')
from (
  values
    (to_regclass('public.users')::text),
    (to_regclass('public.applications')::text),
    (to_regclass('public.investigations')::text),
    (to_regclass('public.report_runs')::text)
) as t(name);
`)

	if !strings.Contains(stdout, "users,applications,investigations,report_runs") {
		t.Fatalf("unexpected table check output: %s", stdout)
	}
}

func TestDownMigrationDropsTables(t *testing.T) {
	root := repoRoot(t)
	stack := newPostgresStack(t, root)
	stack.ResetDatabase(t)
	stack.ApplyMigration(t, "/migrations/0001_foundations.up.sql")
	stack.ApplyMigration(t, "/migrations/0001_foundations.down.sql")

	stdout := stack.PSQL(t, stack.DBName, `select coalesce(to_regclass('public.users')::text, '');`)
	if !strings.Contains(stdout, "\n\n") {
		t.Fatalf("expected empty to_regclass output, got: %q", stdout)
	}
}

func TestInvestigationLookupQueryPlanUsesIndex(t *testing.T) {
	root := repoRoot(t)
	stack := newPostgresStack(t, root)
	stack.ResetDatabase(t)
	stack.ApplyMigration(t, "/migrations/0001_foundations.up.sql")
	stack.PSQL(t, stack.DBName, `
insert into users (email, display_name) values ('admin@example.com', 'Admin');
insert into environments (name, slug, type) values ('Prod', 'prod', 'production');
insert into applications (environment_id, name, platform)
select id, 'Wallboard', 'outsystems' from environments where slug = 'prod';
insert into investigations (application_id, environment_id, created_by, subject_type, subject_value, time_start, time_end, query)
select a.id, e.id, u.id, 'user', 'alice@example.com', now() - interval '1 hour', now(), '{}'::jsonb
from applications a
join environments e on e.id = a.environment_id
join users u on u.email = 'admin@example.com';
`)

	stdout := stack.PSQL(t, stack.DBName, `
set enable_seqscan = off;
explain (costs off)
select *
from investigations
where environment_id = (select id from environments where slug = 'prod')
  and subject_type = 'user'
  and subject_value = 'alice@example.com'
  and time_start <= now()
  and time_end >= now() - interval '2 hours';
`)

	if !strings.Contains(stdout, "investigations_subject_lookup_idx") {
		t.Fatalf("query plan did not use expected index: %s", stdout)
	}
}

func TestMigrationFilesUseSQLExtension(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"db/migrations/0001_foundations.up.sql",
		"db/migrations/0001_foundations.down.sql",
	} {
		if filepath.Ext(rel) != ".sql" {
			t.Fatalf("expected .sql file extension for %s", rel)
		}
		mustExist(t, filepath.Join(root, rel))
	}
}
