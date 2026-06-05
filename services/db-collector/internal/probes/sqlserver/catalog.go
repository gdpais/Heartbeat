package sqlserver

type Probe struct {
	Name          string
	Category      string
	QueryTemplate string
	Metrics       []Metric
}

type Metric struct {
	Name         string
	Help         string
	ValueColumn  string
	LabelColumns []string
}

type Catalog struct {
	byName map[string]Probe
}

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

func (c Catalog) Get(name string) (Probe, bool) {
	probe, ok := c.byName[name]
	return probe, ok
}
