package scale

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/golang-sql/civil"
)

// Window is the (From, To] range one query run reads, as the collector
// binds it: From is the last Instant delivered, To now minus the lag.
type Window struct {
	Name     string
	From, To time.Time
}

// Windows are the runs worth measuring on a filled database: the busiest
// minute, read as it happens, and backlogs of an hour, a day and a week, as
// after an outage (a week is initial_lookback's limit).
func Windows(ctx context.Context, db *sql.DB) ([]Window, error) {
	var end, peak sql.NullTime
	err := db.QueryRowContext(ctx, `SELECT
    (SELECT MAX(Instant) FROM dbo.oslog_Screen),
    (SELECT TOP (1) DATEADD(minute, DATEDIFF(minute, 0, Instant), 0) FROM (
        SELECT Instant FROM dbo.oslog_Screen UNION ALL SELECT Instant FROM dbo.oslog_Screen_Previous) AS s
     GROUP BY DATEADD(minute, DATEDIFF(minute, 0, Instant), 0) ORDER BY COUNT(*) DESC)`).Scan(&end, &peak)
	if err != nil {
		return nil, fmt.Errorf("the log's last and busiest minute: %w", err)
	}
	if !end.Valid || !peak.Valid {
		return nil, fmt.Errorf("dbo.oslog_Screen has no rows: generate the log first")
	}
	e := end.Time.Truncate(time.Second).Add(time.Second)
	return []Window{
		{"busiest minute", peak.Time.Add(-time.Millisecond), peak.Time.Add(time.Minute - time.Millisecond)},
		{"1 h backlog", e.Add(-time.Hour), e},
		{"1 day backlog", e.Add(-24 * time.Hour), e},
		{"7 day backlog", e.Add(-Week), e},
	}, nil
}

// Result is one query over one window.
type Result struct {
	LogType string
	Window  string
	// Rows is what the query returned (at most the limit).
	Rows int
	// First is the first run's logical reads: the plan is compiled and
	// stale statistics updated then. The others are the fewest of the next
	// three runs, the collector's steady state.
	First        int64
	LogicalReads int64
	CPU          time.Duration
	Elapsed      time.Duration
}

// Measure runs every query in queriesDir (the collector's own files) over
// each window with limit rows (batch_limit), on one connection whose
// logical reads and CPU time are counted, and returns the results in log
// type order.
func Measure(ctx context.Context, db *sql.DB, queriesDir string, windows []Window, limit int) ([]Result, error) {
	paths, err := filepath.Glob(filepath.Join(queriesDir, "*.sql"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no queries (*.sql) in %s", queriesDir)
	}
	sort.Strings(paths)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	session := func() (reads, cpu int64, err error) {
		err = conn.QueryRowContext(ctx, "SELECT logical_reads, cpu_time FROM sys.dm_exec_sessions WHERE session_id = @@SPID").Scan(&reads, &cpu)
		return reads, cpu, err
	}
	var out []Result
	for _, path := range paths {
		query, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		logType := strings.TrimSuffix(filepath.Base(path), ".sql")
		for _, w := range windows {
			res := Result{LogType: logType, Window: w.Name, LogicalReads: -1}
			for run := range 4 {
				reads0, cpu0, err := session()
				if err != nil {
					return nil, err
				}
				started := time.Now()
				n, err := runQuery(ctx, conn, string(query), w, limit)
				if err != nil {
					return nil, fmt.Errorf("%s over the %s: %w", logType, w.Name, err)
				}
				elapsed := time.Since(started)
				reads1, cpu1, err := session()
				if err != nil {
					return nil, err
				}
				reads, cpu := reads1-reads0, time.Duration(cpu1-cpu0)*time.Millisecond
				if run == 0 {
					res.First, res.Rows = reads, n
					continue
				}
				if res.LogicalReads < 0 || reads < res.LogicalReads {
					res.LogicalReads = reads
				}
				if run == 1 || cpu < res.CPU {
					res.CPU = cpu
				}
				if run == 1 || elapsed < res.Elapsed {
					res.Elapsed = elapsed
				}
			}
			out = append(out, res)
		}
	}
	return out, nil
}

// runQuery runs a log query with the collector's parameters: datetime2
// bounds (civil, so no time zone conversion) and the row limit.
func runQuery(ctx context.Context, conn *sql.Conn, query string, w Window, limit int) (int, error) {
	rows, err := conn.QueryContext(ctx, query,
		sql.Named("from", civil.DateTimeOf(w.From)), sql.Named("to", civil.DateTimeOf(w.To)), sql.Named("limit", limit))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

// TableSize is one log type's rows and space across its cycle and detail
// tables.
type TableSize struct {
	LogType string
	Rows    int64
	MB      float64
}

// Sizes returns the rows and reserved space of each log type's tables.
func Sizes(ctx context.Context, db *sql.DB, views map[string]string) ([]TableSize, error) {
	var out []TableSize
	for logType, view := range views {
		var rows sql.NullInt64
		var pages sql.NullInt64
		err := db.QueryRowContext(ctx, `SELECT SUM(CASE WHEN p.index_id IN (0, 1) AND t.name NOT LIKE '%[_]Detail[_]%' THEN p.row_count END), SUM(p.reserved_page_count)
FROM sys.dm_db_partition_stats AS p JOIN sys.tables AS t ON t.object_id = p.object_id
WHERE t.name LIKE @p1`, strings.ReplaceAll(view, "_", "[_]")+"[_]%").Scan(&rows, &pages)
		if err != nil {
			return nil, err
		}
		out = append(out, TableSize{LogType: logType, Rows: rows.Int64, MB: float64(pages.Int64) * 8 / 1024})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LogType < out[j].LogType })
	return out, nil
}

// Report writes the measurement as Markdown: the database's size, then each
// query over each window against the collector's probe deadline (half the
// scrape interval, at most 10 s).
func Report(w io.Writer, database string, sizes []TableSize, windows []Window, results []Result, limit int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# OutSystems log queries on %s\n\n", database)
	fmt.Fprintf(&b, "Measured %s. Each query runs as the collector runs it (`@limit` = %d, datetime2 bounds) on one connection; reads and CPU are the fewest of three runs after a first one, which compiles the plan.\n\n", time.Now().UTC().Format(time.RFC3339), limit)
	b.WriteString("| Log type | Rows | Space (MB) |\n| --- | ---: | ---: |\n")
	var rows int64
	var mb float64
	for _, s := range sizes {
		fmt.Fprintf(&b, "| %s | %d | %.0f |\n", s.LogType, s.Rows, s.MB)
		rows += s.Rows
		mb += s.MB
	}
	fmt.Fprintf(&b, "| **all** | **%d** | **%.0f** |\n\n", rows, mb)
	b.WriteString("| Window | From | To |\n| --- | --- | --- |\n")
	for _, w := range windows {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", w.Name, w.From.Format(time.DateTime), w.To.Format(time.DateTime))
	}
	b.WriteString("\n| Log type | Window | Rows returned | Logical reads (first run) | Logical reads | CPU (ms) | Elapsed (ms) |\n| --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	var slowest Result
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %d |\n", r.LogType, r.Window, r.Rows, r.First, r.LogicalReads, r.CPU.Milliseconds(), r.Elapsed.Milliseconds())
		if r.Elapsed > slowest.Elapsed {
			slowest = r
		}
	}
	fmt.Fprintf(&b, "\nSlowest: %s over the %s, %d ms; the probe deadline is half the scrape interval, at most 10,000 ms.\n", slowest.LogType, slowest.Window, slowest.Elapsed.Milliseconds())
	_, err := io.WriteString(w, b.String())
	return err
}
