package sqlserver

type Probe struct {
	Name          string
	Category      string
	QueryTemplate string
	MetricNames   []string
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
			MetricNames:   []string{"heartbeat_sqlserver_waits_wait_time_ms"},
		},
		{
			Name:          "blocking",
			Category:      "blocking",
			QueryTemplate: "SELECT blocking_session_id, COUNT(*) AS blocked_count FROM sys.dm_exec_requests WHERE blocking_session_id <> 0 GROUP BY blocking_session_id",
			MetricNames:   []string{"heartbeat_sqlserver_blocking_blocked_count"},
		},
		{
			Name:          "sessions",
			Category:      "sessions",
			QueryTemplate: "SELECT status, COUNT(*) AS session_count FROM sys.dm_exec_sessions GROUP BY status",
			MetricNames:   []string{"heartbeat_sqlserver_sessions_session_count"},
		},
		{
			Name:          "memory_pressure",
			Category:      "memory_pressure",
			QueryTemplate: "SELECT 'total_server_memory_kb' AS metric, cntr_value FROM sys.dm_os_performance_counters WHERE counter_name = 'Total Server Memory (KB)'",
			MetricNames:   []string{"heartbeat_sqlserver_memory_pressure_cntr_value"},
		},
		{
			Name:          "storage",
			Category:      "storage",
			QueryTemplate: "SELECT DB_NAME(database_id) AS database_name, size * 8.0 / 1024 AS size_mb FROM sys.master_files",
			MetricNames:   []string{"heartbeat_sqlserver_storage_size_mb"},
		},
		{
			Name:          "throughput",
			Category:      "throughput",
			QueryTemplate: "SELECT counter_name, cntr_value FROM sys.dm_os_performance_counters WHERE counter_name IN ('Batch Requests/sec', 'Transactions/sec')",
			MetricNames:   []string{"heartbeat_sqlserver_throughput_cntr_value"},
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
