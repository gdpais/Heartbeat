package oslog

import (
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"heartbeat/tools/patientzero/internal/logschema"
)

const fixtureDir = "../../../../services/db-collector/internal/collectors/outsystems/testdata/oslog"

func schema(t *testing.T) Schema {
	t.Helper()
	types, err := logschema.Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	return NewSchema(types)
}

func TestCheckRejectsUnknownTypesAndColumns(t *testing.T) {
	s := schema(t)
	if err := s.Check([]Row{{Type: "screen", Columns: map[string]any{"instant": time.Now(), "Request_Key": "k"}}}); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	if err := s.Check([]Row{{Type: "audit", Columns: nil}}); err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("unknown log type: %v", err)
	}
	if err := s.Check([]Row{{Type: "screen", Columns: map[string]any{"Error_Id": ""}}}); err == nil || !strings.Contains(err.Error(), "oslog_Screen has no column Error_Id") {
		t.Fatalf("unknown column: %v", err)
	}
}

func TestInsertGoesThroughTheCurrentViewWithTypedTimes(t *testing.T) {
	w := &SQLWriter{Schema: schema(t)}
	at := time.Date(2026, 10, 19, 11, 0, 0, 0, time.FixedZone("WEST", 3600))
	query, args := w.insert(Row{Type: "screen", Columns: map[string]any{"Instant": at, "Screen": "OrderList", "Duration": 5}})
	if want := "INSERT INTO dbo.oslog_Screen ([Duration], [Instant], [Screen]) VALUES (@p1, @p2, @p3);"; query != want {
		t.Fatalf("query\n got %s\nwant %s", query, want)
	}
	got, ok := args[1].(mssql.DateTime1)
	if !ok || !time.Time(got).Equal(at) || time.Time(got).Location() != time.UTC {
		t.Fatalf("Instant bound as %#v, want a datetime in UTC", args[1])
	}
}
