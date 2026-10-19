// Package logschema builds PatientZero's OutSystems 11 logging schema from
// the db-collector's oslog fixtures
// (services/db-collector/internal/collectors/outsystems/testdata/oslog/), so a
// schema change is made once, in the fixtures, and PatientZero follows it
// (ADR 0010).
//
// A fixture creates two cycle tables and the two views of one log type. This
// package reads the first cycle table and the current view's column list, and
// builds what the platform has: ten cycle tables, oslog_<TYPE>_0 to _9, one
// week each, behind oslog_<TYPE> (the current week) and oslog_<TYPE>_Previous
// (the week before).
package logschema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// types maps each cycle-table column, upper case, to its SQL type name
	// in lower case (int, varchar, datetime2, bit, ...).
	types map[string]string

	body    string // the CREATE TABLE body, with the cycle as %[1]d
	indexes []string
}

// PreviousView is the previous week's view.
func (t LogType) PreviousView() string { return t.View + "_Previous" }

// Table is the cycle table for cycle c.
func (t LogType) Table(c int) string { return fmt.Sprintf("%s_%d", t.View, c) }

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
	viewRE        = `(?s)EXEC \(N'CREATE VIEW dbo\.%s AS\s+SELECT (.*?)\s+FROM dbo\.%s_\d+'\);`
	indexRE       = `CREATE (?:UNIQUE )?(?:CLUSTERED |NONCLUSTERED )?INDEX \w+ ON dbo\.%s_%s \([^)]*\);`
	columnDefRE   = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*) ([A-Za-z0-9]+)\b`)
	columnRE      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	cycleRE       = regexp.MustCompile(`(?im)^(\s*CYCLE int (?:NOT )?NULL DEFAULT )(\d+)(,?)$`)
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
// and a current view with an explicit column list.
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
		return LogType{}, fmt.Errorf("no EXEC (N'CREATE VIEW dbo.%s AS SELECT ... FROM dbo.%s_<cycle>')", view, view)
	}
	var cols []string
	for _, c := range strings.Split(vm[1], ",") {
		c = strings.TrimSpace(c)
		if !columnRE.MatchString(c) {
			return LogType{}, fmt.Errorf("view dbo.%s: column %q is not a plain column name", view, c)
		}
		cols = append(cols, c)
	}
	t := LogType{Name: name, View: view, Columns: cols, types: map[string]string{}}
	for _, cm := range columnDefRE.FindAllStringSubmatch(body, -1) {
		t.types[strings.ToUpper(cm[1])] = strings.ToLower(cm[2])
	}
	for _, c := range cols {
		if t.ColumnType(c) == "" {
			return LogType{}, fmt.Errorf("view dbo.%s: column %s is not in %s_%s", view, c, view, num)
		}
	}
	t.body = cycleRE.ReplaceAllString(templ(view, num, body), "${1}%[1]d${3}")
	for _, idx := range regexp.MustCompile(fmt.Sprintf(indexRE, regexp.QuoteMeta(view), num)).FindAllString(src, -1) {
		t.indexes = append(t.indexes, templ(view, num, idx))
	}
	return t, nil
}

// templ replaces the fixture's cycle table name, also inside constraint and
// index names, with a %[1]d placeholder for the cycle.
func templ(view, num, s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	return strings.ReplaceAll(s, view+"_"+num, view+"_%[1]d")
}

// Statements returns the T-SQL that creates the log type's ten cycle tables
// and two views where they do not exist yet, and grants reader SELECT on the
// two views, nothing else. Each statement runs in its own batch. It is safe
// to run again: existing objects, and whichever cycle the views point at, are
// kept.
func (t LogType) Statements(reader string) []string {
	var out []string
	for c := 0; c < Cycles; c++ {
		table := t.Table(c)
		var b strings.Builder
		fmt.Fprintf(&b, "IF OBJECT_ID(N'dbo.%s', N'U') IS NULL\nBEGIN\nCREATE TABLE dbo.%s (", table, table)
		fmt.Fprintf(&b, t.body, c)
		b.WriteString("\n);\n")
		for _, idx := range t.indexes {
			fmt.Fprintf(&b, idx, c)
			b.WriteString("\n")
		}
		b.WriteString("END")
		out = append(out, b.String())
	}
	out = append(out,
		t.viewStatement(t.View, InitialCurrent),
		t.viewStatement(t.PreviousView(), InitialPrevious))
	if reader != "" {
		for _, v := range []string{t.View, t.PreviousView()} {
			out = append(out, fmt.Sprintf("GRANT SELECT ON dbo.%s TO %s;", v, quoteName(reader)))
		}
	}
	return out
}

func (t LogType) viewStatement(view string, cycle int) string {
	def := fmt.Sprintf("CREATE VIEW dbo.%s AS\n    SELECT %s\n    FROM dbo.%s", view, strings.Join(t.Columns, ", "), t.Table(cycle))
	return fmt.Sprintf("IF OBJECT_ID(N'dbo.%s', N'V') IS NULL\n    EXEC (N'%s');", view, strings.ReplaceAll(def, "'", "''"))
}

// ReaderRoleStatement creates the database role that is granted SELECT on
// the views, if it does not exist. The collector's login is added to it by
// the setup script, which runs as sa.
func ReaderRoleStatement(reader string) string {
	return fmt.Sprintf("IF DATABASE_PRINCIPAL_ID(N'%s') IS NULL\n    CREATE ROLE %s;",
		strings.ReplaceAll(reader, "'", "''"), quoteName(reader))
}

func quoteName(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }
