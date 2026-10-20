// Package logschema builds PatientZero's OutSystems 11 logging schema from
// the db-collector's oslog fixtures
// (services/db-collector/internal/collectors/outsystems/testdata/oslog/), so a
// schema change is made once, in the fixtures, and PatientZero follows it
// (ADR 0010).
//
// A fixture creates two cycle tables and the two views of one log type. This
// package reads the first cycle table and the current view, and builds what
// the platform has: ten cycle tables, oslog_<TYPE>_0 to _9, one week each,
// behind oslog_<TYPE> (the current week) and oslog_<TYPE>_Previous (the week
// before). The error log's view also left-joins a detail table per cycle,
// oslog_Error_Detail_0 to _9, for stack traces longer than the cycle table
// keeps (see [Detail]).
package logschema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Cycles is the number of cycle tables per log type.
const Cycles = 10

// Initial cycles: the views point at these when they are first created. The
// weekly rotation moves them later.
const (
	InitialCurrent  = 1
	InitialPrevious = 0
)

// LogType is one log type: the fixture's cycle table, generalized.
type LogType struct {
	// Name is the fixture's file name without .sql, the collector's log type
	// (screen, error, mobile_request, ...).
	Name string
	// View is the current week's view, for example oslog_Screen; the cycle
	// tables are View_0 to View_9 and the previous week's view View_Previous.
	View string
	// Columns are the view's columns, in the fixture's order.
	Columns []string
	// Detail is the table the views left-join for values longer than the
	// cycle table keeps, or nil. A view with a detail table computes a
	// column, so rows cannot be inserted through it.
	Detail *Detail
	// types maps each cycle-table column, upper case, to its SQL type name
	// in lower case (int, varchar, datetime2, bit, ...).
	types map[string]string

	tables []table // the cycle table, then the detail table, if any
	view   string  // the view's SELECT, with the cycle as %[1]d
}

// Detail is a log type's detail table: the platform's oslog_Error view
// returns
//
//	CASE WHEN errorDetail.FullStackTrace IS NULL THEN error.Stack ELSE errorDetail.FullStackTrace END AS Stack
//
// from oslog_Error_<cycle> error LEFT JOIN oslog_Error_Detail_<cycle>
// errorDetail ON errorDetail.Id = error.Id.
type Detail struct {
	// Name is the tables' name without the cycle: they are Name_0 to
	// Name_9, for example oslog_Error_Detail.
	Name string
	// Key is the detail table's key and On the cycle-table column it
	// joins (Id and Id).
	Key, On string
	// Column is the view column the detail table overrides (Stack), Value
	// the detail table's column it then returns (FullStackTrace), and
	// Fallback the cycle-table column it returns otherwise (Stack).
	Column, Value, Fallback string
	// Limit is Fallback's length in characters: a longer value goes to
	// the detail table.
	Limit int
}

// Table is the detail table for cycle c.
func (d Detail) Table(c int) string { return fmt.Sprintf("%s_%d", d.Name, c) }

// table is one table of a cycle: its name without the cycle, and its
// CREATE TABLE body and indexes with the cycle as %[1]d.
type table struct {
	name    string
	body    string
	indexes []string
}

// PreviousView is the previous week's view.
func (t LogType) PreviousView() string { return t.View + "_Previous" }

// Table is the cycle table for cycle c.
func (t LogType) Table(c int) string { return fmt.Sprintf("%s_%d", t.View, c) }

// CycleColumn is the cycle-table column a view column is stored in: the
// detail table's Fallback for its Column, else the column itself.
func (t LogType) CycleColumn(name string) string {
	if t.Detail != nil && strings.EqualFold(name, t.Detail.Column) {
		return t.Detail.Fallback
	}
	return name
}

// ColumnType is the column's SQL type name in lower case, without its
// length (int, varchar, datetime2, bit, ...), or "" when the cycle
// table has no such column. Names compare case-insensitively.
func (t LogType) ColumnType(name string) string { return t.types[strings.ToUpper(name)] }

// HasColumn reports whether the view has the column, compared like SQL
// Server's default collation does: case-insensitively.
func (t LogType) HasColumn(name string) bool {
	for _, c := range t.Columns {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

var (
	createTableRE = regexp.MustCompile(`(?s)CREATE TABLE dbo\.(oslog_\w+?)_(\d+) \((.*?)\n\);`)
	viewRE        = `(?s)EXEC \(N'CREATE VIEW dbo\.%s AS\s+(SELECT (.*?)\s+FROM dbo\.%s_(\d+)(.*?))'\);`
	indexRE       = `CREATE (?:UNIQUE )?(?:CLUSTERED |NONCLUSTERED )?INDEX \w+ ON dbo\.%s_%s \([^)]*\);`
	columnDefRE   = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*) ([A-Za-z0-9]+)\b(?:\((\d+|max)\))?`)
	cycleRE       = regexp.MustCompile(`(?im)^(\s*CYCLE int (?:NOT )?NULL DEFAULT )(\d+)(,?)$`)
	// The view's FROM clause after the cycle table: nothing, or an alias,
	// optionally left-joined to a detail table of the same cycle.
	fromRE = regexp.MustCompile(`^(?:(\w+)(?: LEFT JOIN dbo\.(oslog_\w+?)_(\d+) (\w+) ON (\w+)\.(\w+) = (\w+)\.(\w+))?)?$`)
	// A view column: a cycle-table column, or the detail table's value
	// where there is one.
	columnRE   = regexp.MustCompile(`^(?:(\w+)\.)?([A-Za-z_][A-Za-z0-9_]*)$`)
	overrideRE = regexp.MustCompile(`^CASE WHEN (\w+)\.(\w+) IS NULL THEN (\w+)\.(\w+) ELSE (\w+)\.(\w+) END AS ([A-Za-z_][A-Za-z0-9_]*)$`)
)

// Load reads every <log type>.sql fixture in dir (the *.rows.sql files are
// rows, not schema) and returns the log types sorted by name.
func Load(dir string) ([]LogType, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	var types []LogType
	for _, path := range paths {
		if strings.HasSuffix(path, ".rows.sql") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(filepath.Base(path), ".sql")
		t, err := Parse(name, string(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		types = append(types, t)
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("no oslog fixtures (*.sql) in %s", dir)
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Name < types[j].Name })
	return types, nil
}

// Parse reads one fixture. It fails, rather than guessing, when the fixture
// no longer has the shape it expects: one cycle table with a Cycle default,
// and a current view with an explicit column list over that table, or over
// that table left-joined to a detail table (see [Detail]).
func Parse(name, src string) (LogType, error) {
	m := createTableRE.FindStringSubmatch(src)
	if m == nil {
		return LogType{}, fmt.Errorf("no CREATE TABLE dbo.oslog_<TYPE>_<cycle> (...);")
	}
	view, num, body := m[1], m[2], m[3]
	cycles := cycleRE.FindAllStringSubmatch(body, -1)
	if len(cycles) != 1 || cycles[0][2] != num {
		return LogType{}, fmt.Errorf("%s_%s: want one Cycle int [NOT] NULL DEFAULT %s column", view, num, num)
	}
	vm := regexp.MustCompile(fmt.Sprintf(viewRE, regexp.QuoteMeta(view), regexp.QuoteMeta(view))).FindStringSubmatch(src)
	if vm == nil {
		return LogType{}, fmt.Errorf("no EXEC (N'CREATE VIEW dbo.%s AS SELECT ... FROM dbo.%s_<cycle> ...')", view, view)
	}
	def, list, current, from := vm[1], vm[2], vm[3], strings.Join(strings.Fields(vm[4]), " ")
	t := LogType{Name: name, View: view, types: map[string]string{}}
	lengths := map[string]int{}
	for _, cm := range columnDefRE.FindAllStringSubmatch(body, -1) {
		t.types[strings.ToUpper(cm[1])] = strings.ToLower(cm[2])
		if n, err := strconv.Atoi(cm[3]); err == nil {
			lengths[strings.ToUpper(cm[1])] = n
		}
	}

	fm := fromRE.FindStringSubmatch(from)
	if fm == nil {
		return LogType{}, fmt.Errorf("view dbo.%s: FROM dbo.%s_%s %s is not the cycle table, optionally left-joined to a detail table", view, view, current, from)
	}
	alias, detailAlias := fm[1], ""
	var detailTypes map[string]string
	if fm[2] != "" {
		d := &Detail{Name: fm[2]}
		detailAlias = fm[4]
		if fm[3] != current || detailAlias == alias {
			return LogType{}, fmt.Errorf("view dbo.%s: joins %s_%s %s, want the detail table of cycle %s under its own alias", view, d.Name, fm[3], detailAlias, current)
		}
		switch {
		case fm[5] == detailAlias && fm[7] == alias:
			d.Key, d.On = fm[6], fm[8]
		case fm[5] == alias && fm[7] == detailAlias:
			d.Key, d.On = fm[8], fm[6]
		default:
			return LogType{}, fmt.Errorf("view dbo.%s: the join of %s is not ON %s.<column> = %s.<column>", view, d.Name, detailAlias, alias)
		}
		dm := regexp.MustCompile(`(?s)CREATE TABLE dbo\.` + regexp.QuoteMeta(d.Name) + `_` + num + ` \((.*?)\n\);`).FindStringSubmatch(src)
		if dm == nil {
			return LogType{}, fmt.Errorf("view dbo.%s joins %s, but there is no CREATE TABLE dbo.%s_%s (...);", view, d.Name, d.Name, num)
		}
		detailTypes = map[string]string{}
		for _, cm := range columnDefRE.FindAllStringSubmatch(dm[1], -1) {
			detailTypes[strings.ToUpper(cm[1])] = strings.ToLower(cm[2])
		}
		if detailTypes[strings.ToUpper(d.Key)] == "" || t.ColumnType(d.On) == "" {
			return LogType{}, fmt.Errorf("view dbo.%s: %s.%s or %s_%s.%s does not exist", view, d.Name, d.Key, view, num, d.On)
		}
		t.Detail = d
		t.tables = append(t.tables, table{name: d.Name, body: dm[1]})
	}
	t.tables = append([]table{{name: view, body: body}}, t.tables...)

	for _, item := range strings.Split(list, ",") {
		item = strings.Join(strings.Fields(item), " ")
		if cm := columnRE.FindStringSubmatch(item); cm != nil && cm[1] == alias {
			if t.ColumnType(cm[2]) == "" {
				return LogType{}, fmt.Errorf("view dbo.%s: column %s is not in %s_%s", view, cm[2], view, num)
			}
			t.Columns = append(t.Columns, cm[2])
			continue
		}
		om := overrideRE.FindStringSubmatch(item)
		if om == nil || t.Detail == nil || t.Detail.Column != "" ||
			om[1] != detailAlias || om[5] != detailAlias || om[2] != om[6] || om[3] != alias {
			return LogType{}, fmt.Errorf("view dbo.%s: column %q is not a plain column name", view, item)
		}
		d := t.Detail
		d.Column, d.Value, d.Fallback, d.Limit = om[7], om[2], om[4], lengths[strings.ToUpper(om[4])]
		if detailTypes[strings.ToUpper(d.Value)] == "" || t.ColumnType(d.Fallback) == "" {
			return LogType{}, fmt.Errorf("view dbo.%s: %s.%s or %s_%s.%s does not exist", view, d.Name, d.Value, view, num, d.Fallback)
		}
		if d.Limit == 0 {
			return LogType{}, fmt.Errorf("view dbo.%s: %s_%s.%s has no length", view, view, num, d.Fallback)
		}
		t.Columns = append(t.Columns, d.Column)
	}
	if t.Detail != nil && t.Detail.Column == "" {
		return LogType{}, fmt.Errorf("view dbo.%s joins %s but returns none of its columns", view, t.Detail.Name)
	}

	var names []string
	for _, tb := range t.tables {
		names = append(names, tb.name)
	}
	for i, tb := range t.tables {
		tb.body = templ(tb.body, num, names...)
		if i == 0 {
			tb.body = cycleRE.ReplaceAllString(tb.body, "${1}%[1]d${3}")
		}
		for _, idx := range regexp.MustCompile(fmt.Sprintf(indexRE, regexp.QuoteMeta(tb.name), num)).FindAllString(src, -1) {
			tb.indexes = append(tb.indexes, templ(idx, num, tb.name))
		}
		t.tables[i] = tb
	}
	t.view = templ(strings.ReplaceAll(def, "''", "'"), current, names...)
	return t, nil
}

// templ replaces the fixture's tables of cycle num, named after each of
// names, also inside constraint and index names, with a %[1]d placeholder
// for the cycle.
func templ(s, num string, names ...string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	for _, name := range names {
		s = strings.ReplaceAll(s, name+"_"+num, name+"_%[1]d")
	}
	return s
}

// Statements returns the T-SQL that creates the log type's ten cycle tables
// (with their detail tables) and two views where they do not exist yet, and
// grants reader SELECT on the two views, nothing else. Each statement runs
// in its own batch. It is safe to run again: existing objects, and whichever
// cycle the views point at, are kept.
func (t LogType) Statements(reader string) []string {
	var out []string
	for c := 0; c < Cycles; c++ {
		var b strings.Builder
		for i, tb := range t.tables {
			name := fmt.Sprintf("%s_%d", tb.name, c)
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "IF OBJECT_ID(N'dbo.%s', N'U') IS NULL\nBEGIN\nCREATE TABLE dbo.%s (", name, name)
			fmt.Fprintf(&b, tb.body, c)
			b.WriteString("\n);\n")
			for _, idx := range tb.indexes {
				fmt.Fprintf(&b, idx, c)
				b.WriteString("\n")
			}
			b.WriteString("END")
		}
		out = append(out, b.String())
	}
	out = append(out,
		t.viewStatement(t.View, InitialCurrent, false),
		t.viewStatement(t.PreviousView(), InitialPrevious, false))
	if reader != "" {
		for _, v := range []string{t.View, t.PreviousView()} {
			out = append(out, fmt.Sprintf("GRANT SELECT ON dbo.%s TO %s;", v, quoteName(reader)))
		}
	}
	return out
}

// RotateStatements points the current view at cycle current and the
// previous view at cycle previous, as the platform's weekly rotation does:
// only the views move. Each statement runs in its own batch.
func (t LogType) RotateStatements(current, previous int) []string {
	return []string{t.viewStatement(t.View, current, true), t.viewStatement(t.PreviousView(), previous, true)}
}

func (t LogType) viewStatement(view string, cycle int, replace bool) string {
	def := strings.ReplaceAll(fmt.Sprintf("CREATE VIEW dbo.%s AS\n    %s", view, fmt.Sprintf(t.view, cycle)), "'", "''")
	if replace {
		return fmt.Sprintf("EXEC (N'CREATE OR ALTER%s');", strings.TrimPrefix(def, "CREATE"))
	}
	return fmt.Sprintf("IF OBJECT_ID(N'dbo.%s', N'V') IS NULL\n    EXEC (N'%s');", view, def)
}

// ReaderRoleStatement creates the database role that is granted SELECT on
// the views, if it does not exist. The collector's login is added to it by
// the setup script, which runs as sa.
func ReaderRoleStatement(reader string) string {
	return fmt.Sprintf("IF DATABASE_PRINCIPAL_ID(N'%s') IS NULL\n    CREATE ROLE %s;",
		strings.ReplaceAll(reader, "'", "''"), quoteName(reader))
}

func quoteName(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }
