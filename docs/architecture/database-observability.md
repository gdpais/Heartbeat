# Database Observability Architecture

## Scope
- SQL Server is the first DB engine in scope.
- PostgreSQL can store target metadata, probe definitions, and probe assignments for future control-plane workflows.
- Active collector runtime desired state is not read from PostgreSQL for MVP.

## Safety rules
- Credentials are referenced by `credential_ref` only.
- Production probes must be non-blocking.
- Probe definitions are versioned and can be disabled instead of deleted.
- Runtime collector grouping, target activation, probe activation, and scaling live in `config/integrations.yaml` and Kubernetes delivery.

## PostgreSQL model
- `database_targets`
- `probe_definitions`
- `probe_assignments`

## Deferred topology
- `assets`
- `asset_relationships`
- target-to-asset linkage beyond nullable future hooks
