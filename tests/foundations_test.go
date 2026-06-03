package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredFoundationFilesExist(t *testing.T) {
	root := repoRoot(t)
	required := []string{
		"docs/product/requirements.md",
		"docs/product/phased-roadmap.md",
		"docs/architecture/overview.md",
		"docs/architecture/database-observability.md",
		"docs/architecture/session-analysis.md",
		"docs/architecture/alerting.md",
		"docs/runbooks/local-dev.md",
		"infra/docker-compose.yml",
		"packages/config-schema/src/integrations.schema.json",
		"packages/telemetry-contracts/src/application_event.schema.json",
		"packages/telemetry-contracts/src/session_investigation.schema.json",
		"packages/telemetry-contracts/src/alert_evidence.schema.json",
		"packages/telemetry-contracts/src/report_payload.schema.json",
		"db/migrations/0001_foundations.up.sql",
		"db/migrations/0001_foundations.down.sql",
	}

	for _, rel := range required {
		t.Run(rel, func(t *testing.T) {
			mustExist(t, filepath.Join(root, rel))
		})
	}
}

func TestMonorepoScaffoldingExists(t *testing.T) {
	root := repoRoot(t)
	required := []string{
		"apps/api/cmd/api",
		"apps/api/internal",
		"apps/web",
		"services/otel-gateway/cmd/otel-gateway",
		"services/otel-gateway/internal",
		"services/db-collector/cmd/db-collector",
		"services/db-collector/internal",
		"services/session-analyzer/cmd/session-analyzer",
		"services/session-analyzer/internal",
		"services/reporting/cmd/reporting",
		"services/reporting/internal",
		"infra/grafana/provisioning/datasources",
		"infra/grafana/provisioning/dashboards",
		"infra/prometheus",
		"infra/loki",
		"infra/alertmanager",
		"infra/otel-collector",
		"infra/k8s",
		"tests/integration",
		"tests/e2e",
	}

	for _, rel := range required {
		t.Run(rel, func(t *testing.T) {
			mustExist(t, filepath.Join(root, rel))
		})
	}
}

func TestSchemaContractsAreValidJSON(t *testing.T) {
	root := repoRoot(t)
	schemas := []string{
		"packages/config-schema/src/integrations.schema.json",
		"packages/telemetry-contracts/src/application_event.schema.json",
		"packages/telemetry-contracts/src/session_investigation.schema.json",
		"packages/telemetry-contracts/src/alert_evidence.schema.json",
		"packages/telemetry-contracts/src/report_payload.schema.json",
	}

	for _, rel := range schemas {
		t.Run(rel, func(t *testing.T) {
			schema := mustReadJSONSchema(t, filepath.Join(root, rel))
			if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
				t.Fatalf("unexpected $schema in %s: %#v", rel, schema["$schema"])
			}
			if _, ok := schema["title"]; !ok {
				t.Fatalf("missing title in %s", rel)
			}
			if _, ok := schema["type"]; !ok {
				t.Fatalf("missing type in %s", rel)
			}
		})
	}
}

func TestDockerComposeConfigIsValid(t *testing.T) {
	root := repoRoot(t)
	runCommand(t, root, "docker", "compose", "-f", filepath.Join(root, "infra/docker-compose.yml"), "config")
}

func TestMigrationContainsCoreTablesAndExcludesNonGoals(t *testing.T) {
	sql := migrationSQL(t)
	expected := []string{
		"create table users",
		"create table roles",
		"create table user_roles",
		"create table environments",
		"create table applications",
		"create table application_components",
		"create table telemetry_sources",
		"create table normalization_rules",
		"create table database_targets",
		"create table probe_definitions",
		"create table probe_assignments",
		"create table investigations",
		"create table investigation_jobs",
		"create table investigation_results",
		"create table evidence_links",
		"create table alert_policies",
		"create table adaptive_baselines",
		"create table notification_routes",
		"create table alert_events",
		"create table report_templates",
		"create table report_schedules",
		"create table report_runs",
	}
	for _, fragment := range expected {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("migration missing fragment %q", fragment)
		}
	}

	forbidden := []string{
		"create table outsystems_sources",
		"create table session_identity_mappings",
		"create table integration_connections",
		"create table grafana_links",
		"create table audit_events",
		"create table collector_instances",
		"create table collector_assignments",
		"create table assets",
		"create table asset_relationships",
	}
	for _, fragment := range forbidden {
		if strings.Contains(sql, fragment) {
			t.Fatalf("migration unexpectedly contains fragment %q", fragment)
		}
	}
}

func TestMigrationHasExpectedUniquesAndIndexes(t *testing.T) {
	sql := migrationSQL(t)
	expected := []string{
		"unique (user_id, role_id)",
		"unique (environment_id, name)",
		"unique (application_id, name, component_type)",
		"unique (source_type, version)",
		"create unique index database_targets_identity_uq on database_targets (environment_id, engine, host, port, coalesce(database_name, ''))",
		"unique (engine, name, version)",
		"unique (database_target_id, probe_definition_id)",
		"unique (application_id, name)",
		"unique (application_id, signal_key, \"window\", method)",
		"unique (application_id, fingerprint, started_at)",
		"unique (application_id, report_template_id, cron_expression, timezone)",
		"create unique index environments_slug_uq",
		"create index investigations_environment_created_at_idx",
		"create index investigations_subject_lookup_idx",
		"create index alert_events_fingerprint_started_at_idx",
	}
	for _, fragment := range expected {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("migration missing fragment %q", fragment)
		}
	}
}
