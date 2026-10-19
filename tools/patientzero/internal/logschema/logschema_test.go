package logschema

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// fixtureDir is the db-collector's oslog fixtures, the one place the logging
// schema is defined.
const fixtureDir = "../../../../services/db-collector/internal/collectors/outsystems/testdata/oslog"

func TestLoadReadsEveryFixture(t *testing.T) {
	types, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, lt := range types {
		names = append(names, lt.Name+"="+lt.View)
		if !lt.HasColumn("Instant") {
			t.Errorf("%s: no Instant column in %v", lt.Name, lt.Columns)
		}
	}
	want := "cyclic_job=oslog_Cyclic_Job error=oslog_Error extension=oslog_Extension general=oslog_General " +
		"integration=oslog_Integration mobile_request=oslog_mobile_request request_event=oslog_RequestEvent screen=oslog_Screen"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("log types:\n got %s\nwant %s", got, want)
	}
}

func TestStatementsBuildTenCycleTablesAndTwoViews(t *testing.T) {
	types, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, lt := range types {
		stmts := lt.Statements("oslog_reader")
		if len(stmts) != Cycles+4 {
			t.Fatalf("%s: %d statements, want %d", lt.Name, len(stmts), Cycles+4)
		}
		for c := 0; c < Cycles; c++ {
			s := stmts[c]
			table := lt.Table(c)
			if !strings.Contains(s, "CREATE TABLE dbo."+table+" (") {
				t.Errorf("%s: statement %d does not create %s:\n%s", lt.Name, c, table, s)
			}
			if !regexp.MustCompile(fmt.Sprintf(`CYCLE INT (NOT )?NULL DEFAULT %d\b`, c)).MatchString(strings.ToUpper(s)) {
				t.Errorf("%s: %s does not default Cycle to %d:\n%s", lt.Name, table, c, s)
			}
			if !strings.Contains(s, "INDEX ") {
				t.Errorf("%s: %s has no index on Instant:\n%s", lt.Name, table, s)
			}
			if lt.Detail != nil && !strings.Contains(s, "IF OBJECT_ID(N'dbo."+lt.Detail.Table(c)+"', N'U') IS NULL\nBEGIN\nCREATE TABLE dbo."+lt.Detail.Table(c)+" (") {
				t.Errorf("%s: statement %d does not create %s:\n%s", lt.Name, c, lt.Detail.Table(c), s)
			}
			for _, other := range []int{2, 3} {
				if other != c && regexp.MustCompile(fmt.Sprintf(`oslog_\w+_%d\b`, other)).MatchString(s) {
					t.Errorf("%s: %s still names the fixture's cycle %d:\n%s", lt.Name, table, other, s)
				}
			}
			if strings.Contains(s, "%!") {
				t.Errorf("%s: formatting error in %s:\n%s", lt.Name, table, s)
			}
		}
		for _, v := range []struct {
			stmt, view string
			cycle      int
		}{{stmts[Cycles], lt.View, InitialCurrent}, {stmts[Cycles+1], lt.PreviousView(), InitialPrevious}} {
			from := "FROM dbo." + lt.Table(v.cycle) + "');"
			if lt.Detail != nil {
				from = fmt.Sprintf("FROM dbo.%s error\n    LEFT JOIN dbo.%s errorDetail ON errorDetail.Id = error.Id');", lt.Table(v.cycle), lt.Detail.Table(v.cycle))
			}
			if !strings.HasPrefix(v.stmt, "IF OBJECT_ID(N'dbo."+v.view+"', N'V') IS NULL\n    EXEC (N'CREATE VIEW dbo."+v.view+" AS") || !strings.HasSuffix(v.stmt, from) {
				t.Errorf("%s: view %s, want it FROM cycle %d:\n%s", lt.Name, v.view, v.cycle, v.stmt)
			}
		}
		if got := stmts[Cycles+2]; got != "GRANT SELECT ON dbo."+lt.View+" TO [oslog_reader];" {
			t.Errorf("%s: grant %q", lt.Name, got)
		}
	}
}

func TestStatementsWithoutReaderGrantNothing(t *testing.T) {
	lt, err := Parse("screen", screenFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range lt.Statements("") {
		if strings.Contains(s, "GRANT") {
			t.Fatalf("granted without a reader: %s", s)
		}
	}
}

func TestParseRejectsAnUnexpectedShape(t *testing.T) {
	for name, src := range map[string]string{
		"no table":                   "SELECT 1;",
		"no cycle":                   strings.Replace(screenFixture, "    Cycle int NOT NULL DEFAULT 2,\n", "", 1),
		"no view":                    screenFixture[:strings.Index(screenFixture, "EXEC")],
		"wrong cycle":                strings.Replace(screenFixture, "DEFAULT 2,", "DEFAULT 7,", 1),
		"star in view":               strings.Replace(screenFixture, "SELECT Instant, Duration, Cycle", "SELECT *", 1),
		"expression":                 strings.Replace(screenFixture, "Duration,", "Duration * 2 AS Duration,", 1),
		"unknown column":             strings.Replace(screenFixture, "Duration,", "Elapsed,", 1),
		"inner join":                 strings.Replace(errorFixture, "LEFT JOIN", "JOIN", 1),
		"join of another cycle":      strings.Replace(errorFixture, "LEFT JOIN dbo.oslog_Error_Detail_3", "LEFT JOIN dbo.oslog_Error_Detail_2", 1),
		"join on an unknown column":  strings.Replace(errorFixture, "ON d.Id = e.Id", "ON d.Id = e.Error_Id", 1),
		"no detail table":            strings.Replace(errorFixture, "CREATE TABLE dbo.oslog_Error_Detail_2", "CREATE TABLE dbo.oslog_Other_2", 1),
		"detail not returned":        strings.Replace(errorFixture, "CASE WHEN d.FullStackTrace IS NULL THEN e.Stack ELSE d.FullStackTrace END AS Stack", "e.Stack", 1),
		"mixed values":               strings.Replace(errorFixture, "ELSE d.FullStackTrace", "ELSE d.Id", 1),
		"plain column of the detail": strings.Replace(errorFixture, "e.Instant,", "d.Id,", 1),
		"fallback without a length":  strings.Replace(errorFixture, "Stack varchar(2000)", "Stack text", 1),
	} {
		if _, err := Parse("screen", src); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestQuotesReaderNames(t *testing.T) {
	if got := ReaderRoleStatement("a]b'c"); got != "IF DATABASE_PRINCIPAL_ID(N'a]b''c') IS NULL\n    CREATE ROLE [a]]b'c];" {
		t.Fatalf("got %q", got)
	}
}

const screenFixture = `CREATE TABLE dbo.oslog_Screen_2 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Cycle int NOT NULL DEFAULT 2,
    CONSTRAINT PK_oslog_Screen_2 PRIMARY KEY NONCLUSTERED (Instant)
);
CREATE CLUSTERED INDEX IX_oslog_Screen_2_Instant ON dbo.oslog_Screen_2 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_Screen AS
    SELECT Instant, Duration, Cycle
    FROM dbo.oslog_Screen_3');
`

const errorFixture = `CREATE TABLE dbo.oslog_Error_2 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NOT NULL,
    Session_Id varchar(50) NOT NULL,
    Stack varchar(2000) NULL,
    Cycle int NOT NULL DEFAULT 2,
    CONSTRAINT PK_oslog_Error_2 PRIMARY KEY CLUSTERED (Id)
);
CREATE TABLE dbo.oslog_Error_Detail_2 (
    Id varchar(50) NOT NULL,
    FullStackTrace varchar(max) NOT NULL,
    CONSTRAINT PK_oslog_Error_Detail_2 PRIMARY KEY CLUSTERED (Id)
);
EXEC (N'CREATE VIEW dbo.oslog_Error AS
    SELECT e.Id, e.Instant,
        CASE WHEN d.FullStackTrace IS NULL THEN e.Stack ELSE d.FullStackTrace END AS Stack,
        e.Cycle
    FROM dbo.oslog_Error_3 e
    LEFT JOIN dbo.oslog_Error_Detail_3 d ON d.Id = e.Id');
`

func TestParseReadsTheDetailJoin(t *testing.T) {
	lt, err := Parse("error", errorFixture)
	if err != nil {
		t.Fatal(err)
	}
	want := Detail{Name: "oslog_Error_Detail", Key: "Id", On: "Id", Column: "Stack", Value: "FullStackTrace", Fallback: "Stack", Limit: 2000}
	if lt.Detail == nil || *lt.Detail != want {
		t.Fatalf("detail %+v, want %+v", lt.Detail, want)
	}
	if got := strings.Join(lt.Columns, " "); got != "Id Instant Stack Cycle" {
		t.Fatalf("columns %s", got)
	}
	if !lt.HasColumn("stack") || lt.ColumnType("Stack") != "varchar" || lt.CycleColumn("STACK") != "Stack" || lt.CycleColumn("Id") != "Id" {
		t.Fatalf("Stack: has %v, type %q, stored in %q", lt.HasColumn("stack"), lt.ColumnType("Stack"), lt.CycleColumn("STACK"))
	}
	stmts := lt.Statements("")
	if want := "IF OBJECT_ID(N'dbo.oslog_Error_Detail_4', N'U') IS NULL\nBEGIN\nCREATE TABLE dbo.oslog_Error_Detail_4 (\n    Id varchar(50) NOT NULL,\n    FullStackTrace varchar(max) NOT NULL,\n    CONSTRAINT PK_oslog_Error_Detail_4 PRIMARY KEY CLUSTERED (Id)\n);\nEND"; !strings.HasSuffix(stmts[4], want) {
		t.Fatalf("cycle 4:\n%s\nwant it to end in\n%s", stmts[4], want)
	}
	// The join's ON may name the cycle table first.
	swapped, err := Parse("error", strings.Replace(errorFixture, "ON d.Id = e.Id", "ON e.Id = d.Id", 1))
	if err != nil || swapped.Detail.Key != "Id" || swapped.Detail.On != "Id" {
		t.Fatalf("ON e.Id = d.Id: %+v, %v", swapped.Detail, err)
	}
}

func TestRotateStatementsOnlyMoveTheViews(t *testing.T) {
	lt, err := Parse("error", errorFixture)
	if err != nil {
		t.Fatal(err)
	}
	got := lt.RotateStatements(5, 4)
	want := []string{
		"EXEC (N'CREATE OR ALTER VIEW dbo.oslog_Error AS\n    SELECT e.Id, e.Instant,\n        CASE WHEN d.FullStackTrace IS NULL THEN e.Stack ELSE d.FullStackTrace END AS Stack,\n        e.Cycle\n    FROM dbo.oslog_Error_5 e\n    LEFT JOIN dbo.oslog_Error_Detail_5 d ON d.Id = e.Id');",
		"EXEC (N'CREATE OR ALTER VIEW dbo.oslog_Error_Previous AS\n    SELECT e.Id, e.Instant,\n        CASE WHEN d.FullStackTrace IS NULL THEN e.Stack ELSE d.FullStackTrace END AS Stack,\n        e.Cycle\n    FROM dbo.oslog_Error_4 e\n    LEFT JOIN dbo.oslog_Error_Detail_4 d ON d.Id = e.Id');",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestColumnTypes(t *testing.T) {
	types, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]LogType{}
	for _, lt := range types {
		byName[lt.Name] = lt
	}
	for _, c := range []struct{ log, col, want string }{
		{"screen", "Instant", "datetime2"},
		{"screen", "request_key", "varchar"},
		{"error", "Id", "varchar"},
		{"error", "Stack", "varchar"},
		{"integration", "Is_Expose", "bit"},
		{"integration", "Cycle", "int"},
		{"mobile_request", "Error_Id", "varchar"},
		{"request_event", "INSTANT", "datetime2"},
		{"screen", "Constraint", ""},
		{"error", "CONSTRAINT", ""},
	} {
		if got := byName[c.log].ColumnType(c.col); got != c.want {
			t.Errorf("%s.%s: %q, want %q", c.log, c.col, got, c.want)
		}
	}
}
