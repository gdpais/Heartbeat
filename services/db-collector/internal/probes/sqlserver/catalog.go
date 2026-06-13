// Package sqlserver defines the built-in SQL Server probe catalog used by the
// db-collector service.
//
// A [Catalog] maps probe names to [Probe] definitions.  Each [Probe] bundles
// a default SQL query template with the set of [Metric] descriptors that
// describe how to decode the result set into Prometheus gauge samples.
//
// The [DefaultCatalog] function returns a pre-populated catalog with the
// following built-in probes:
//
//   - waits          – cumulative wait statistics from sys.dm_os_wait_stats
//   - blocking       – current blocked-request count from sys.dm_exec_requests
//   - sessions       – session counts by status from sys.dm_exec_sessions
//   - memory_pressure – server memory counters from sys.dm_os_performance_counters
//   - storage        – database file sizes from sys.master_files
//   - throughput     – batch/transaction rate counters from sys.dm_os_performance_counters
package sqlserver

// Probe describes a named SQL Server probe: the SQL to execute and the metrics
// to extract from the result set.
type Probe struct {
	// Name is the unique key used to look up this probe in a [Catalog].
	Name string
	// Category groups related probes and determines whether evidence records
	// are produced (e.g. "blocking", "sessions").
	Category string
	// QueryTemplate is the default SQL query.  Individual collector
	// configurations may override this per probe.
	QueryTemplate string
	// Metrics lists the gauge descriptors that decode columns from the query
	// result set into labelled Prometheus samples.
	Metrics []Metric
}

// Metric describes how to extract a single Prometheus gauge from one column of
// a probe's SQL result set.
type Metric struct {
	// Name is the fully-qualified Prometheus metric name
	// (e.g. "heartbeat_sqlserver_sessions").
	Name string
	// Help is the human-readable description registered with Prometheus.
	Help string
	// ValueColumn is the result-set column whose value becomes the gauge value.
	ValueColumn string
	// LabelColumns are additional result-set columns whose values become
	// Prometheus label values (column name becomes the label key).
	LabelColumns []string
}

// Catalog is an immutable, name-indexed collection of [Probe] definitions.
type Catalog struct {
	byName map[string]Probe
}

// DefaultCatalog returns a [Catalog] pre-populated with all built-in SQL
// Server probes.  See the package documentation for the full list.
func DefaultCatalog() Catalog {
	probes := []Probe{
		{
			Name:          "waits",
			Category:      "waits",
			QueryTemplate: "SELECT wait_type, wait_time_ms FROM sys.dm_os_wait_stats WHERE wait_time_ms > 0",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_wait_time_ms",
				Help:         "Cumulative SQL Server wait time by wait type.",
				ValueColumn:  "wait_time_ms",
				LabelColumns: []string{"wait_type"},
			}},
		},
		{
			Name:          "blocking",
			Category:      "blocking",
			QueryTemplate: "SELECT blocking_session_id, COUNT(*) AS blocked_count FROM sys.dm_exec_requests WHERE blocking_session_id <> 0 GROUP BY blocking_session_id",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_blocked_requests",
				Help:         "Current blocked SQL Server request count by blocking session.",
				ValueColumn:  "blocked_count",
				LabelColumns: []string{"blocking_session_id"},
			}},
		},
		{
			Name:          "sessions",
			Category:      "sessions",
			QueryTemplate: "SELECT status, COUNT(*) AS session_count FROM sys.dm_exec_sessions GROUP BY status",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_sessions",
				Help:         "Current SQL Server session count by status.",
				ValueColumn:  "session_count",
				LabelColumns: []string{"status"},
			}},
		},
		{
			Name:          "memory_pressure",
			Category:      "memory_pressure",
			QueryTemplate: "SELECT 'total_server_memory_kb' AS metric, cntr_value FROM sys.dm_os_performance_counters WHERE counter_name = 'Total Server Memory (KB)'",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_memory_kb",
				Help:         "SQL Server memory counter value in KB.",
				ValueColumn:  "cntr_value",
				LabelColumns: []string{"metric"},
			}},
		},
		{
			Name:          "storage",
			Category:      "storage",
			QueryTemplate: "SELECT DB_NAME(database_id) AS database_name, size * 8.0 / 1024 AS size_mb FROM sys.master_files",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_database_file_size_mb",
				Help:         "SQL Server database file size in MB.",
				ValueColumn:  "size_mb",
				LabelColumns: []string{"database_name"},
			}},
		},
		{
			Name:          "throughput",
			Category:      "throughput",
			QueryTemplate: "SELECT counter_name, cntr_value FROM sys.dm_os_performance_counters WHERE counter_name IN ('Batch Requests/sec', 'Transactions/sec')",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_throughput",
				Help:         "SQL Server throughput performance counter value.",
				ValueColumn:  "cntr_value",
				LabelColumns: []string{"counter_name"},
			}},
		},
	}
	catalog := Catalog{byName: map[string]Probe{}}
	for _, probe := range probes {
		catalog.byName[probe.Name] = probe
	}
	return catalog
}

// Get returns the [Probe] registered under name and true, or the zero value
// and false if no probe with that name exists.
func (c Catalog) Get(name string) (Probe, bool) {
	probe, ok := c.byName[name]
	return probe, ok
}
