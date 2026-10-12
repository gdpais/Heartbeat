package outsystems

import (
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

// attributes returns kv as a map of Go values.
func attributes(kv []*commonpb.KeyValue) map[string]any {
	out := map[string]any{}
	for _, attribute := range kv {
		switch v := attribute.Value.Value.(type) {
		case *commonpb.AnyValue_StringValue:
			out[attribute.Key] = v.StringValue
		case *commonpb.AnyValue_IntValue:
			out[attribute.Key] = v.IntValue
		case *commonpb.AnyValue_DoubleValue:
			out[attribute.Key] = v.DoubleValue
		case *commonpb.AnyValue_BoolValue:
			out[attribute.Key] = v.BoolValue
		default:
			out[attribute.Key] = attribute.Value
		}
	}
	return out
}

func TestBuildLogsMapsRowsToRecords(t *testing.T) {
	lisbon, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	// Summer time: Lisbon is UTC+1.
	instant := time.Date(2026, 7, 1, 13, 0, 0, 123_000_000, time.UTC)
	observed := time.Date(2026, 7, 1, 12, 5, 0, 0, time.UTC)
	batch := Batch{Columns: []Column{
		{"Instant", "DATETIME"}, {"Message", "NVARCHAR"}, {"Severity", "VARCHAR"},
		{"Application_Name", "NVARCHAR"}, {"Espace_Name", "NVARCHAR"}, {"Request_Key", "CHAR"},
		{"User_Id", "INT"}, {"Duration", "BIGINT"}, {"Ratio", "FLOAT"}, {"Is_Expose", "BIT"},
		{"Amount", "DECIMAL"}, {"Session_Guid", "UNIQUEIDENTIFIER"}, {"Payload", "VARBINARY"},
		{"Should_Have_Run_At", "DATETIME"}, {"Day", "DATE"}, {"Stack", "NVARCHAR"}, {"Tenant_Id", "INT"},
	}}
	rows := [][]any{
		{instant, "Order 12 failed", "warn", "Shop", "ShopCore", "ab12                                ",
			int64(7), int64(1500), 0.25, true,
			[]byte("12.50"), []byte{0x67, 0x45, 0x23, 0x01, 0xab, 0x89, 0xef, 0xcd, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}, []byte{0xde, 0xad},
			time.Date(2026, 7, 1, 12, 59, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), "at A()\nat B()", nil},
		{instant, nil, nil, nil, "Billing", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
		{instant, "x", "LOUD", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
		{instant, "Order 13 failed", "Error", "Shop", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "at A()\nat B()", nil},
	}
	instants := []time.Time{instant, instant, instant, instant}
	out := buildLogs(batch, rows, instants, recordContext{
		collector: "oslogs", target: "logging-db", environment: "prod", logType: "error", location: lisbon, observed: observed,
	})

	// One resource per application, in order of first appearance.
	if len(out) != 3 {
		t.Fatalf("got %d resources, want Shop, Billing and unknown", len(out))
	}
	services := []string{}
	for _, rl := range out {
		resource := attributes(rl.Resource.Attributes)
		services = append(services, resource["service.name"].(string))
		want := map[string]any{
			"service.name": resource["service.name"], "deployment.environment.name": "prod", "outsystems.log_type": "error",
			"heartbeat.collector": "oslogs", "heartbeat.target": "logging-db",
		}
		if len(resource) != len(want) {
			t.Errorf("resource attributes %v, want %v", resource, want)
		}
		for key, value := range want {
			if resource[key] != value {
				t.Errorf("resource %s = %v, want %v", key, resource[key], value)
			}
		}
	}
	if services[0] != "Shop" || services[1] != "Billing" || services[2] != "unknown" {
		t.Fatalf("services = %v", services)
	}
	shop := out[0].ScopeLogs[0].LogRecords
	if len(shop) != 2 {
		t.Fatalf("Shop has %d records, want 2", len(shop))
	}

	first := shop[0]
	if want := uint64(time.Date(2026, 7, 1, 12, 0, 0, 123_000_000, time.UTC).UnixNano()); first.TimeUnixNano != want {
		t.Errorf("timestamp = %d, want Instant read in Lisbon time (%d)", first.TimeUnixNano, want)
	}
	if first.ObservedTimeUnixNano != uint64(observed.UnixNano()) {
		t.Errorf("observed timestamp = %d", first.ObservedTimeUnixNano)
	}
	if first.Body.GetStringValue() != "Order 12 failed" {
		t.Errorf("body = %v", first.Body)
	}
	if first.SeverityNumber != logspb.SeverityNumber_SEVERITY_NUMBER_WARN || first.SeverityText != "WARN" {
		t.Errorf("severity = %v %q, want WARN from the Severity column", first.SeverityNumber, first.SeverityText)
	}
	got := attributes(first.Attributes)
	want := map[string]any{
		"Application_Name":   "Shop",
		"Espace_Name":        "ShopCore",
		"Request_Key":        "ab12",
		"User_Id":            int64(7),
		"Duration":           int64(1500),
		"Ratio":              0.25,
		"Is_Expose":          true,
		"Amount":             12.5,
		"Session_Guid":       "01234567-89AB-CDEF-0123-456789ABCDEF",
		"Payload":            "dead",
		"Should_Have_Run_At": "2026-07-01T11:59:00Z",
		"Day":                "2026-07-01",
		"Stack":              "at A()\nat B()",
		"error.fingerprint":  Fingerprint("Order 12 failed", "at A()\nat B()"),
	}
	if len(got) != len(want) {
		t.Errorf("attributes %v,\nwant %v (Instant, Message, Severity and NULL columns are not attributes)", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("attribute %s = %#v, want %#v", key, got[key], value)
		}
	}
	// Records of one error with different values share a fingerprint.
	if second := attributes(shop[1].Attributes); second["error.fingerprint"] != got["error.fingerprint"] {
		t.Errorf("fingerprints differ: %v, %v", second["error.fingerprint"], got["error.fingerprint"])
	}
	if shop[1].SeverityText != "ERROR" {
		t.Errorf("severity %q, want ERROR (case-insensitive)", shop[1].SeverityText)
	}

	billing := out[1].ScopeLogs[0].LogRecords[0]
	if billing.Body != nil {
		t.Errorf("NULL Message gave body %v", billing.Body)
	}
	if billing.SeverityText != "ERROR" || billing.SeverityNumber != logspb.SeverityNumber_SEVERITY_NUMBER_ERROR {
		t.Errorf("error row without Severity is %q, want ERROR", billing.SeverityText)
	}
	unknown := out[2].ScopeLogs[0].LogRecords[0]
	if unknown.SeverityText != "ERROR" {
		t.Errorf("unknown Severity value gave %q, want the log type's default", unknown.SeverityText)
	}
}

func TestSeverityDefaultsByLogType(t *testing.T) {
	for _, tt := range []struct {
		raw     any
		logType string
		want    string
	}{
		{nil, "error", "ERROR"},
		{nil, "general", "INFO"},
		{"debug", "general", "DEBUG"},
		{" Trace ", "screen", "TRACE"},
		{"FATAL", "error", "FATAL"},
		{"WARNING", "general", "INFO"},
		{[]byte("warn"), "general", "WARN"},
	} {
		if _, got := severity(tt.raw, tt.logType); got != tt.want {
			t.Errorf("severity(%v, %s) = %s, want %s", tt.raw, tt.logType, got, tt.want)
		}
	}
}

func TestBuildLogsAddsNoFingerprintOutsideTheErrorLogType(t *testing.T) {
	instant := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	batch := Batch{Columns: []Column{{"Instant", "DATETIME2"}, {"Message", "NVARCHAR"}, {"Ratio", "REAL"}}}
	out := buildLogs(batch, [][]any{{instant, "slow query", float32(0.5)}}, []time.Time{instant}, recordContext{logType: "general", location: time.UTC})
	record := out[0].ScopeLogs[0].LogRecords[0]
	got := attributes(record.Attributes)
	if _, ok := got["error.fingerprint"]; ok {
		t.Error("general record has error.fingerprint")
	}
	if got["Ratio"] != 0.5 || record.SeverityText != "INFO" {
		t.Errorf("attributes %v, severity %s", got, record.SeverityText)
	}
	resource := attributes(out[0].Resource.Attributes)
	if _, ok := resource["deployment.environment.name"]; ok {
		t.Error("empty environment became a resource attribute")
	}
}
