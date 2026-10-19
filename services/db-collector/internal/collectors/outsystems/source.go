package outsystems

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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
	// MaxBytes, when above 0, stops reading once about this much row data is
	// held (see [readRows]); the batch is then [Batch.Partial].
	MaxBytes int64
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
	// Partial reports that reading stopped at [QueryParams.MaxBytes] with
	// rows left: the last row is the first of a new Instant, which the caller
	// drops as it drops the last Instant of a full batch.
	Partial bool
}

// Source runs log-type queries against a target.
type Source interface {
	// Query runs query with params and returns at most params.Limit+1 rows,
	// so a query that ignores @limit is detected without reading it all, and
	// fewer, marked Partial, once params.MaxBytes of row data is read.
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
	if err := readRows(rows, &batch, params); err != nil {
		return Batch{}, err
	}
	return batch, nil
}

// rowReader is the part of [sql.Rows] that readRows uses.
type rowReader interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// readRows reads rows into batch, whose Columns are set: at most
// params.Limit+1 rows, so a query that ignores @limit is detected without
// reading it all.
//
// With params.MaxBytes above 0 it also stops once that much row data (see
// [rowBytes]) is held, so a backlog of rows with long stacks cannot exhaust
// the collector's memory.  It stops only where Instant changes: it keeps the
// first row of the next Instant and marks the batch Partial, and the caller
// drops that row as it drops the last Instant of a full batch.  Rows that
// share an Instant are never split, which no watermark could resume between;
// they are bounded by params.Limit as before.  Without an Instant column
// (which the caller rejects) it never stops early.
func readRows(rows rowReader, batch *Batch, params QueryParams) error {
	instant := slices.IndexFunc(batch.Columns, func(c Column) bool { return c.Name == columnInstant })
	var size int64
	for len(batch.Rows) <= params.Limit && rows.Next() {
		values := make([]any, len(batch.Columns))
		scans := make([]any, len(batch.Columns))
		for i := range values {
			scans[i] = &values[i]
		}
		if err := rows.Scan(scans...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		spent := params.MaxBytes > 0 && size >= params.MaxBytes
		if spent && instant >= 0 && len(batch.Rows) < params.Limit && !sameInstant(values[instant], batch.Rows[len(batch.Rows)-1][instant]) {
			batch.Rows, batch.Partial = append(batch.Rows, values), true
			return nil
		}
		batch.Rows = append(batch.Rows, values)
		size += rowBytes(values)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}
	return nil
}

// rowBytes estimates the memory a scanned row holds: the text and binary
// values plus an interface and a header per value.
func rowBytes(values []any) int64 {
	n := int64(len(values)) * 32
	for _, v := range values {
		switch v := v.(type) {
		case string:
			n += int64(len(v))
		case []byte:
			n += int64(len(v))
		}
	}
	return n
}

// sameInstant reports whether a and b are the same Instant value.
func sameInstant(a, b any) bool {
	x, ok := a.(time.Time)
	y, ok2 := b.(time.Time)
	return ok && ok2 && x.Equal(y)
}

// TargetSysadmin reports whether the login of target's pool is
// sysadmin-equivalent, from the Manager's last login check.
func (s SQLServerSource) TargetSysadmin(target collectormetadata.DatabaseTarget) (sysadmin, ok bool) {
	return s.Manager.Sysadmin(target)
}
