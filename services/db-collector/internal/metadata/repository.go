package metadata

import (
	"context"
	"database/sql"
	"fmt"
)

const scheduledProbesQuery = `
select
  $2 as collector_id,
  dt.id as target_id,
  e.slug as environment_slug,
  dt.name as target_name,
  dt.engine,
  dt.host,
  dt.port,
  coalesce(dt.database_name, '') as database_name,
  dt.credential_ref,
  pa.id as probe_assignment_id,
  pd.id as probe_definition_id,
  pd.name as probe_name,
  pd.category as probe_category,
  pd.query_template,
  pd.timeout_ms,
  pa.interval_seconds
from database_targets dt
join environments e on e.id = dt.environment_id
join probe_assignments pa on pa.database_target_id = dt.id
join probe_definitions pd on pd.id = pa.probe_definition_id
where dt.engine = $1
  and dt.status = 'active'
  and pa.enabled = true
  and pd.enabled = true
order by e.slug, dt.name, pd.name
`

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) Repository {
	return Repository{db: db}
}

func (r Repository) ListScheduledProbes(ctx context.Context, filter QueryFilter) ([]ScheduledProbe, error) {
	rows, err := r.db.QueryContext(ctx, scheduledProbesQuery, filter.Engine, filter.CollectorID)
	if err != nil {
		return nil, fmt.Errorf("query scheduled probes: %w", err)
	}
	defer rows.Close()

	var items []ScheduledProbe
	for rows.Next() {
		var item ScheduledProbe
		if err := rows.Scan(
			&item.CollectorID,
			&item.Target.ID,
			&item.Target.EnvironmentSlug,
			&item.Target.Name,
			&item.Target.Engine,
			&item.Target.Host,
			&item.Target.Port,
			&item.Target.DatabaseName,
			&item.Target.CredentialRef,
			&item.Assignment.ID,
			&item.Definition.ID,
			&item.Definition.Name,
			&item.Definition.Category,
			&item.Definition.QueryTemplate,
			&item.Definition.TimeoutMS,
			&item.Assignment.IntervalSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan scheduled probe: %w", err)
		}
		if filter.EnvironmentSlug != "" && item.Target.EnvironmentSlug != filter.EnvironmentSlug {
			continue
		}
		if len(filter.TargetNames) > 0 && !contains(filter.TargetNames, item.Target.Name) {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled probes: %w", err)
	}
	return items, nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
