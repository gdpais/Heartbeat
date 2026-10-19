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
			for _, other := range []int{2, 3} {
				if other != c && strings.Contains(s, fmt.Sprintf("%s_%d", lt.View, other)) {
					t.Errorf("%s: %s still names the fixture's cycle %d:\n%s", lt.Name, table, other, s)
				}
			}
			if strings.Contains(s, "%!") {
				t.Errorf("%s: formatting error in %s:\n%s", lt.Name, table, s)
			}
		}
		current, previous := stmts[Cycles], stmts[Cycles+1]
		if !strings.Contains(current, "CREATE VIEW dbo."+lt.View+" AS") || !strings.HasSuffix(current, "FROM dbo."+lt.Table(InitialCurrent)+"');") {
			t.Errorf("%s: current view:\n%s", lt.Name, current)
		}
		if !strings.Contains(previous, "CREATE VIEW dbo."+lt.PreviousView()+" AS") || !strings.HasSuffix(previous, "FROM dbo."+lt.Table(InitialPrevious)+"');") {
			t.Errorf("%s: previous view:\n%s", lt.Name, previous)
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
		"no table":     "SELECT 1;",
		"no cycle":     strings.Replace(screenFixture, "    Cycle int NOT NULL DEFAULT 2,\n", "", 1),
		"no view":      screenFixture[:strings.Index(screenFixture, "EXEC")],
		"wrong cycle":  strings.Replace(screenFixture, "DEFAULT 2,", "DEFAULT 7,", 1),
		"star in view": strings.Replace(screenFixture, "SELECT Instant, Duration, Cycle", "SELECT *", 1),
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
