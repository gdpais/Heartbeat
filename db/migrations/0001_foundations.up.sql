create extension if not exists pgcrypto;

create table users (
  id uuid primary key default gen_random_uuid(),
  email text not null unique,
  display_name text not null,
  status text not null default 'active' check (status in ('active', 'disabled')),
  last_login_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table roles (
  id uuid primary key default gen_random_uuid(),
  name text not null unique,
  description text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table user_roles (
  user_id uuid not null references users(id) on delete restrict,
  role_id uuid not null references roles(id) on delete restrict,
  created_at timestamptz not null default now(),
  primary key (user_id, role_id),
  unique (user_id, role_id)
);

create table environments (
  id uuid primary key default gen_random_uuid(),
  name text not null,
  slug text not null,
  type text not null check (type in ('production', 'staging', 'development', 'test')),
  description text not null default '',
  status text not null default 'active' check (status in ('active', 'disabled')),
  labels jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index environments_slug_uq on environments (slug);

create table applications (
  id uuid primary key default gen_random_uuid(),
  environment_id uuid not null references environments(id) on delete restrict,
  name text not null,
  platform text not null,
  owner_team text not null default '',
  criticality text not null default 'medium' check (criticality in ('low', 'medium', 'high', 'critical')),
  status text not null default 'active' check (status in ('active', 'disabled')),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (environment_id, name)
);

create table application_components (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  name text not null,
  component_type text not null,
  runtime_name text not null default '',
  labels jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (application_id, name, component_type)
);

create table telemetry_sources (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  source_type text not null,
  ingest_mode text not null check (ingest_mode in ('otlp', 'file', 'api', 'agentless')),
  status text not null default 'active' check (status in ('active', 'disabled')),
  config jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index telemetry_sources_application_source_status_idx on telemetry_sources (application_id, source_type, status);

create table normalization_rules (
  id uuid primary key default gen_random_uuid(),
  source_type text not null,
  version text not null,
  enabled boolean not null default true,
  rule_config jsonb not null default '{}'::jsonb,
  created_by uuid references users(id) on delete set null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (source_type, version)
);

create table database_targets (
  id uuid primary key default gen_random_uuid(),
  environment_id uuid not null references environments(id) on delete restrict,
  engine text not null,
  name text not null,
  host text not null,
  port integer not null check (port > 0 and port <= 65535),
  database_name text,
  status text not null default 'active' check (status in ('active', 'disabled')),
  credential_ref text not null,
  labels jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index database_targets_identity_uq on database_targets (environment_id, engine, host, port, coalesce(database_name, ''));
create index database_targets_environment_engine_status_idx on database_targets (environment_id, engine, status);

create table probe_definitions (
  id uuid primary key default gen_random_uuid(),
  engine text not null,
  name text not null,
  version text not null,
  category text not null,
  default_interval_seconds integer not null check (default_interval_seconds > 0),
  timeout_ms integer not null check (timeout_ms > 0),
  query_template text not null,
  enabled boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (engine, name, version)
);

create table probe_assignments (
  id uuid primary key default gen_random_uuid(),
  database_target_id uuid not null references database_targets(id) on delete cascade,
  probe_definition_id uuid not null references probe_definitions(id) on delete restrict,
  interval_seconds integer not null check (interval_seconds > 0),
  enabled boolean not null default true,
  config jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (database_target_id, probe_definition_id)
);

create table investigations (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  environment_id uuid not null references environments(id) on delete restrict,
  created_by uuid not null references users(id) on delete restrict,
  status text not null default 'queued' check (status in ('queued', 'running', 'completed', 'failed', 'cancelled')),
  subject_type text not null check (subject_type in ('user', 'ip', 'session', 'request')),
  subject_value text not null,
  time_start timestamptz not null,
  time_end timestamptz not null,
  query jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check (time_end >= time_start)
);
create index investigations_environment_created_at_idx on investigations (environment_id, created_at desc);
create index investigations_subject_lookup_idx on investigations (environment_id, subject_type, subject_value, time_start, time_end);
create index investigations_application_created_at_idx on investigations (application_id, created_at desc);

create table investigation_jobs (
  id uuid primary key default gen_random_uuid(),
  investigation_id uuid not null references investigations(id) on delete cascade,
  status text not null default 'queued' check (status in ('queued', 'running', 'completed', 'failed', 'cancelled')),
  queued_at timestamptz not null default now(),
  started_at timestamptz,
  finished_at timestamptz,
  error_message text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index investigation_jobs_one_active_uq on investigation_jobs (investigation_id, status) where status in ('queued', 'running');

create table investigation_results (
  id uuid primary key default gen_random_uuid(),
  investigation_id uuid not null references investigations(id) on delete cascade,
  summary text not null,
  severity text not null check (severity in ('info', 'warning', 'critical')),
  result jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create table evidence_links (
  id uuid primary key default gen_random_uuid(),
  investigation_id uuid not null references investigations(id) on delete cascade,
  source_type text not null,
  title text not null,
  url text not null,
  time_start timestamptz,
  time_end timestamptz,
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create table alert_policies (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  name text not null,
  signal_type text not null,
  severity text not null check (severity in ('info', 'warning', 'critical')),
  enabled boolean not null default true,
  condition jsonb not null default '{}'::jsonb,
  labels jsonb not null default '{}'::jsonb,
  rendered_rule_ref text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (application_id, name)
);

create table adaptive_baselines (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete cascade,
  signal_key text not null,
  "window" text not null,
  method text not null,
  parameters jsonb not null default '{}'::jsonb,
  last_computed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (application_id, signal_key, "window", method)
);

create table notification_routes (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  name text not null,
  channel_type text not null,
  target_ref text not null,
  routing_rules jsonb not null default '{}'::jsonb,
  enabled boolean not null default true,
  alertmanager_route_ref text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (application_id, name)
);

create table alert_events (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  alert_policy_id uuid references alert_policies(id) on delete set null,
  status text not null check (status in ('firing', 'resolved', 'suppressed')),
  severity text not null check (severity in ('info', 'warning', 'critical')),
  fingerprint text not null,
  started_at timestamptz not null,
  resolved_at timestamptz,
  labels jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (application_id, fingerprint, started_at),
  check (resolved_at is null or resolved_at >= started_at)
);
create index alert_events_fingerprint_started_at_idx on alert_events (application_id, fingerprint, started_at desc);

create table report_templates (
  id uuid primary key default gen_random_uuid(),
  name text not null,
  description text not null default '',
  template_type text not null,
  template_config jsonb not null default '{}'::jsonb,
  enabled boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (name)
);

create table report_schedules (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  report_template_id uuid not null references report_templates(id) on delete restrict,
  cron_expression text not null,
  timezone text not null,
  recipients jsonb not null default '[]'::jsonb,
  enabled boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (application_id, report_template_id, cron_expression, timezone)
);

create table report_runs (
  id uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications(id) on delete restrict,
  report_schedule_id uuid references report_schedules(id) on delete set null,
  requested_by uuid references users(id) on delete set null,
  status text not null check (status in ('queued', 'running', 'completed', 'failed', 'cancelled')),
  time_start timestamptz not null,
  time_end timestamptz not null,
  artifact_uri text,
  started_at timestamptz,
  finished_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check (time_end >= time_start),
  check (finished_at is null or started_at is null or finished_at >= started_at)
);
create index report_runs_application_status_idx on report_runs (application_id, status, created_at desc);
