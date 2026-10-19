// Package oslog writes the log rows the OutSystems 11 platform would write for
// a request into PatientZero's logging database, through the current week's
// view of each log type (oslog_<TYPE>), so the weekly rotation only has to
// move the views.
package oslog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"heartbeat/tools/patientzero/internal/logschema"
)

// Row is one log row: the log type (the fixture's name, such as screen) and
// its column values. Columns are the view's own names; a nil value is NULL.
type Row struct {
	Type    string
	Columns map[string]any
}

// Writer stores the log rows of one request.
type Writer interface {
	Write(ctx context.Context, rows []Row) error
}

// Schema is the logging schema by log type.
type Schema map[string]logschema.LogType

// NewSchema indexes the log types by name.
func NewSchema(types []logschema.LogType) Schema {
	s := Schema{}
	for _, t := range types {
		s[t.Name] = t
	}
	return s
}

// Check reports the first row that names a log type or column the schema
// does not have. The logging schema follows the collector's fixtures, so a
// renamed column shows up here instead of as a failed insert.
func (s Schema) Check(rows []Row) error {
	for _, r := range rows {
		t, ok := s[r.Type]
		if !ok {
			return fmt.Errorf("log type %s is not in the logging schema", r.Type)
		}
		for c := range r.Columns {
			if !t.HasColumn(c) {
				return fmt.Errorf("log type %s: view dbo.%s has no column %s", r.Type, t.View, c)
			}
		}
	}
	return nil
}

// SQLWriter inserts rows into the logging database.
type SQLWriter struct {
	DB     *sql.DB
	Schema Schema
}

// Write inserts the rows in one transaction: the request's rows across log
// types are committed together, as one unit the collector sees or does not.
func (w *SQLWriter) Write(ctx context.Context, rows []Row) error {
	if err := w.Schema.Check(rows); err != nil {
		return err
	}
	tx, err := w.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	for _, r := range rows {
		query, args := w.insert(r)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("insert into %s: %w", w.Schema[r.Type].View, err)
		}
	}
	return tx.Commit()
}

func (w *SQLWriter) insert(r Row) (string, []any) {
	t := w.Schema[r.Type]
	cols := make([]string, 0, len(r.Columns))
	for c := range r.Columns {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	names := make([]string, len(cols))
	params := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		names[i] = "[" + c + "]"
		params[i] = fmt.Sprintf("@p%d", i+1)
		args[i] = value(r.Columns[c])
	}
	return fmt.Sprintf("INSERT INTO dbo.%s (%s) VALUES (%s);", t.View,
		strings.Join(names, ", "), strings.Join(params, ", ")), args
}

// value binds times in UTC, the zone the logging database stores Instant
// in (the collector's default log_timezone).
func value(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.UTC()
	}
	return v
}
