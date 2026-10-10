package outsystems

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/golang-sql/civil"
	mssql "github.com/microsoft/go-mssqldb"

	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// QueryParams are the parameters bound to a log-type query.
type QueryParams struct {
	// From (exclusive) and To (inclusive) bound Instant.  They are wall-clock
	// values in the log time zone, held as UTC times (see [Wall]).
	From, To time.Time
	// Limit is the most rows the query may return.
	Limit int
	// DateTime sends From and To as datetime instead of datetime2.  It is set
	// once the Instant column is known to be datetime: SQL Server converts a
	// datetime column to datetime2 for a datetime2 comparison, which turns
	// .003 into .0033333 and would make the row read back as .003 (the driver
	// rounds datetime to milliseconds) compare above its own watermark.  As
	// datetime, the parameter rounds to the same 1/300 s tick as the column.
	DateTime bool
}

// Column describes one result column.
type Column struct {
	Name string
	// DatabaseType is the driver's type name, such as "DATETIME" or
	// "NVARCHAR".
	DatabaseType string
}

// Batch is the result of one log-type query.
type Batch struct {
	Columns []Column
	// Rows holds the values in column order, as the driver returns them; nil
	// is SQL NULL.
	Rows [][]any
}

// Source runs log-type queries against a target.
type Source interface {
	// Query runs query with params and returns at most params.Limit+1 rows,
	// so a query that ignores @limit is detected without reading it all.
	Query(ctx context.Context, target collectormetadata.DatabaseTarget, query string, params QueryParams) (Batch, error)
}

// SQLServerSource runs log-type queries through the collector's SQL Server
// connection manager: pooled connections, credential references, login
// checks, and [connector.SessionSettings] in front of every batch.
type SQLServerSource struct {
	Manager connector.Manager
}

// Query implements [Source].
func (s SQLServerSource) Query(ctx context.Context, target collectormetadata.DatabaseTarget, query string, params QueryParams) (Batch, error) {
	db, cleanup, err := s.Manager.Open(ctx, target)
	if err != nil {
		return Batch{}, err
	}
	defer cleanup()
	from, to := any(civil.DateTimeOf(params.From)), any(civil.DateTimeOf(params.To))
	if params.DateTime {
		from, to = mssql.DateTime1(params.From), mssql.DateTime1(params.To)
	}
	rows, err := db.QueryContext(ctx, connector.WithSessionSettings(query),
		sql.Named("from", from), sql.Named("to", to), sql.Named("limit", params.Limit))
	if err != nil {
		return Batch{}, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return Batch{}, fmt.Errorf("read columns: %w", err)
	}
	batch := Batch{Columns: make([]Column, len(types))}
	for i, columnType := range types {
		batch.Columns[i] = Column{Name: columnType.Name(), DatabaseType: columnType.DatabaseTypeName()}
	}
	for len(batch.Rows) <= params.Limit && rows.Next() {
		values := make([]any, len(types))
		scans := make([]any, len(types))
		for i := range values {
			scans[i] = &values[i]
		}
		if err := rows.Scan(scans...); err != nil {
			return Batch{}, fmt.Errorf("scan row: %w", err)
		}
		batch.Rows = append(batch.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return Batch{}, fmt.Errorf("iterate rows: %w", err)
	}
	return batch, nil
}

// TargetSysadmin reports whether the login of target's pool is
// sysadmin-equivalent, from the Manager's last login check.
func (s SQLServerSource) TargetSysadmin(target collectormetadata.DatabaseTarget) (sysadmin, ok bool) {
	return s.Manager.Sysadmin(target)
}
