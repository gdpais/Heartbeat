// Package sqlserver defines the built-in SQL Server probe catalog used by the
// db-collector service.
//
// A [Catalog] maps probe names to [Probe] definitions.  Each [Probe] bundles
// a default SQL query template with the set of [Metric] descriptors that
// describe how to decode the result set into Prometheus samples.
//
// The [DefaultCatalog] function returns a pre-populated catalog with the
// following built-in probes:
//
//   - waits          – cumulative wait time from sys.dm_os_wait_stats (counter)
//   - blocking       – current blocked-request count from sys.dm_exec_requests
//   - sessions       – session counts by status from sys.dm_exec_sessions
//   - memory_pressure – total server memory from sys.dm_os_performance_counters
//   - storage        – database file sizes from sys.master_files
//   - throughput     – cumulative batch requests and transactions from
//     sys.dm_os_performance_counters (counters)
//
// Metric names follow Prometheus conventions: base units (seconds, bytes),
// and a _total suffix on counters, which only counters carry.
package sqlserver

import (
	"maps"
	"slices"

	collectorexport "heartbeat/services/db-collector/internal/export"
)

// Probe describes a named SQL Server probe: the SQL to execute and the metrics
// to extract from the result set.
type Probe struct {
	// Name is the unique key used to look up this probe in a [Catalog].
	Name string
	// Category groups related probes and determines whether evidence records
	// are produced (e.g. "blocking", "sessions").
	Category string
	// QueryTemplate is the default SQL query.  Individual collector
	// configurations may override this per probe; an override must return
	// the same columns, in the same units.
	QueryTemplate string
	// Metrics lists the descriptors that decode columns from the query result
	// set into labelled Prometheus samples.  Every row is decoded by every
	// descriptor, so one row can carry several metrics in separate columns.
	Metrics []Metric
}

// Metric describes how to extract a single Prometheus metric from one column
// of a probe's SQL result set.
type Metric struct {
	// Name is the fully-qualified Prometheus metric name
	// (e.g. "heartbeat_sqlserver_sessions").
	Name string
	// Help is the human-readable description registered with Prometheus.
	Help string
	// Type is how the metric is exposed.  The zero value is a gauge.  Use
	// [collectorexport.Counter] for values SQL Server accumulates since
	// startup (DMV totals, cumulative performance counters such as cntr_type
	// 272696576): they are exported as SQL Server reports them, and rate() or
	// increase() handle the reset when SQL Server restarts or its statistics
	// are cleared.  Ratios belong in the query and are gauges.
	Type collectorexport.MetricType
	// ValueColumn is the result-set column whose value becomes the sample
	// value.  Rows where the column is missing or NULL produce no sample.
	ValueColumn string
	// Scale multiplies the column value to convert it to the metric's base
	// unit, e.g. 0.001 for milliseconds to seconds or 1024 for KB to bytes.
	// Zero means 1.
	Scale float64
	// LabelColumns are additional result-set columns whose values become
	// Prometheus label values (column name becomes the label key).  Choose
	// columns with a bounded set of values: every distinct value is a series.
	LabelColumns []string
}

// Convert returns raw in the metric's base unit by applying [Metric.Scale].
func (m Metric) Convert(raw float64) float64 {
	if m.Scale == 0 {
		return raw
	}
	return raw * m.Scale
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
				Name:         "heartbeat_sqlserver_wait_seconds_total",
				Help:         "Cumulative SQL Server wait time in seconds by wait type since SQL Server started or wait statistics were cleared.",
				Type:         collectorexport.Counter,
				ValueColumn:  "wait_time_ms",
				Scale:        0.001,
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
			QueryTemplate: "SELECT cntr_value AS total_server_memory_kb FROM sys.dm_os_performance_counters WHERE counter_name = 'Total Server Memory (KB)'",
			Metrics: []Metric{{
				Name:        "heartbeat_sqlserver_total_server_memory_bytes",
				Help:        "Memory SQL Server has committed (Total Server Memory) in bytes.",
				ValueColumn: "total_server_memory_kb",
				Scale:       1024,
			}},
		},
		{
			Name:     "storage",
			Category: "storage",
			// One row per database file: file_name is the logical name, unique
			// within a database, so every file gets its own series.  size is
			// in 8 KB pages.
			QueryTemplate: "SELECT DB_NAME(database_id) AS database_name, name AS file_name, type_desc AS file_type, size AS size_pages FROM sys.master_files",
			Metrics: []Metric{{
				Name:         "heartbeat_sqlserver_database_file_size_bytes",
				Help:         "SQL Server database file size in bytes, one series per file.",
				ValueColumn:  "size_pages",
				Scale:        8192,
				LabelColumns: []string{"database_name", "file_name", "file_type"},
			}},
		},
		{
			Name:     "throughput",
			Category: "throughput",
			// Both counters are cumulative despite their /sec names (cntr_type
			// 272696576), so they are exported as counters and read with
			// rate().  The query pivots them into one row with one column per
			// metric.  Transactions/sec has one row per database plus _Total;
			// only the server-wide _Total is kept.  MAX over no rows is NULL,
			// so a missing counter exports no series rather than 0.
			QueryTemplate: "SELECT " +
				"MAX(CASE WHEN counter_name = 'Batch Requests/sec' THEN cntr_value END) AS batch_requests, " +
				"MAX(CASE WHEN counter_name = 'Transactions/sec' AND instance_name = '_Total' THEN cntr_value END) AS transactions " +
				"FROM sys.dm_os_performance_counters WHERE counter_name IN ('Batch Requests/sec', 'Transactions/sec')",
			Metrics: []Metric{
				{
					Name:        "heartbeat_sqlserver_batch_requests_total",
					Help:        "Cumulative SQL Server batch requests (Batch Requests/sec counter) since SQL Server started.",
					Type:        collectorexport.Counter,
					ValueColumn: "batch_requests",
				},
				{
					Name:        "heartbeat_sqlserver_transactions_total",
					Help:        "Cumulative SQL Server transactions across all databases (Transactions/sec counter, _Total) since SQL Server started.",
					Type:        collectorexport.Counter,
					ValueColumn: "transactions",
				},
			},
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

// Names returns the names of every probe in the catalog, sorted.
func (c Catalog) Names() []string {
	return slices.Sorted(maps.Keys(c.byName))
}
