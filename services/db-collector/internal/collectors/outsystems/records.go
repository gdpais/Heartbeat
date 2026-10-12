package outsystems

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// Column names the query contract gives a meaning (queries/README.md).
const (
	columnInstant     = "Instant"
	columnMessage     = "Message"
	columnSeverity    = "Severity"
	columnApplication = "Application_Name"
	columnEspace      = "Espace_Name"
	columnModule      = "Module_Name"
	columnStack       = "Stack"
)

// Attribute and resource attribute names the extractor sets.
const (
	attrServiceName = "service.name"
	attrEnvironment = "deployment.environment.name"
	attrLogType     = "outsystems.log_type"
	attrCollector   = "heartbeat.collector"
	attrTarget      = "heartbeat.target"
	attrFingerprint = "error.fingerprint"
	// unknownService is service.name for rows without an application or
	// module name.
	unknownService = "unknown"
	// errorLogType is the log type whose records are ERROR by default and get
	// error.fingerprint.
	errorLogType = "error"
	// scopeName is the instrumentation scope of every record.
	scopeName = "heartbeat/db-collector/outsystems_logs"
)

// severities maps the Severity values the contract allows to OTLP severity
// numbers.
var severities = map[string]logspb.SeverityNumber{
	"TRACE": logspb.SeverityNumber_SEVERITY_NUMBER_TRACE,
	"DEBUG": logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG,
	"INFO":  logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
	"WARN":  logspb.SeverityNumber_SEVERITY_NUMBER_WARN,
	"ERROR": logspb.SeverityNumber_SEVERITY_NUMBER_ERROR,
	"FATAL": logspb.SeverityNumber_SEVERITY_NUMBER_FATAL,
}

// recordContext is what every record of a batch shares.
type recordContext struct {
	collector   string
	target      string
	environment string
	logType     string
	// location is the log time zone; datetime values are wall clocks in it.
	location *time.Location
	// observed is when the batch was read.
	observed time.Time
}

// buildLogs turns rows of batch into OTLP logs, one ResourceLogs per
// application in order of first appearance, records in row order.
//
//   - The timestamp is Instant, read in the log time zone; the body is
//     Message; the severity comes from Severity, else ERROR for the error
//     log type and INFO for the others.
//   - Every other non-NULL column is an attribute under its exact name (see
//     [attributeValue] for the types).  Application_Name, Espace_Name and
//     Module_Name stay attributes as well as naming the service.
//   - Records of the error log type get error.fingerprint (see
//     [Fingerprint]).
//   - Resource attributes: service.name (Application_Name, else Espace_Name,
//     else Module_Name, else "unknown"), deployment.environment.name (when set),
//     outsystems.log_type, heartbeat.collector and heartbeat.target.
//
// instants holds each row's Instant as a wall clock, from checkContract.
func buildLogs(batch Batch, rows [][]any, instants []time.Time, rc recordContext) []*logspb.ResourceLogs {
	index := map[string]int{}
	for i, column := range batch.Columns {
		if _, seen := index[column.Name]; !seen {
			index[column.Name] = i
		}
	}
	value := func(row []any, name string) any {
		if i, ok := index[name]; ok {
			return row[i]
		}
		return nil
	}
	var out []*logspb.ResourceLogs
	scopes := map[string]*logspb.ScopeLogs{}
	for r, row := range rows {
		service := firstText(value(row, columnApplication), value(row, columnEspace), value(row, columnModule))
		scope, ok := scopes[service]
		if !ok {
			scope = &logspb.ScopeLogs{Scope: &commonpb.InstrumentationScope{Name: scopeName}}
			scopes[service] = scope
			out = append(out, &logspb.ResourceLogs{Resource: resource(service, rc), ScopeLogs: []*logspb.ScopeLogs{scope}})
		}
		number, text := severity(value(row, columnSeverity), rc.logType)
		record := &logspb.LogRecord{
			TimeUnixNano:         unixNano(Real(instants[r], rc.location)),
			ObservedTimeUnixNano: unixNano(rc.observed),
			SeverityNumber:       number,
			SeverityText:         text,
		}
		if message := value(row, columnMessage); message != nil {
			record.Body = stringValue(textOf(message))
		}
		for i, column := range batch.Columns {
			switch column.Name {
			case columnInstant, columnMessage, columnSeverity:
				continue
			}
			if attribute, ok := attributeValue(row[i], column.DatabaseType, rc.location); ok {
				record.Attributes = append(record.Attributes, &commonpb.KeyValue{Key: column.Name, Value: attribute})
			}
		}
		if rc.logType == errorLogType {
			message, stack := optionalText(value(row, columnMessage)), optionalText(value(row, columnStack))
			record.Attributes = append(record.Attributes, &commonpb.KeyValue{Key: attrFingerprint, Value: stringValue(Fingerprint(message, stack))})
		}
		scope.LogRecords = append(scope.LogRecords, record)
	}
	return out
}

// resource returns the resource of service's records.
func resource(service string, rc recordContext) *resourcepb.Resource {
	attributes := []*commonpb.KeyValue{{Key: attrServiceName, Value: stringValue(service)}}
	if rc.environment != "" {
		attributes = append(attributes, &commonpb.KeyValue{Key: attrEnvironment, Value: stringValue(rc.environment)})
	}
	attributes = append(attributes,
		&commonpb.KeyValue{Key: attrLogType, Value: stringValue(rc.logType)},
		&commonpb.KeyValue{Key: attrCollector, Value: stringValue(rc.collector)},
		&commonpb.KeyValue{Key: attrTarget, Value: stringValue(rc.target)},
	)
	return &resourcepb.Resource{Attributes: attributes}
}

// firstText returns the first value that is non-empty text, or
// unknownService.
func firstText(values ...any) string {
	for _, v := range values {
		if v == nil {
			continue
		}
		if text := strings.TrimSpace(textOf(v)); text != "" {
			return text
		}
	}
	return unknownService
}

// severity returns the severity of a row from its Severity value, case
// insensitive.  A missing, NULL or unknown value gives ERROR for the error log
// type and INFO otherwise.
func severity(raw any, logType string) (logspb.SeverityNumber, string) {
	if raw != nil {
		text := strings.ToUpper(strings.TrimSpace(textOf(raw)))
		if number, ok := severities[text]; ok {
			return number, text
		}
	}
	if logType == errorLogType {
		return logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, "ERROR"
	}
	return logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "INFO"
}

// attributeValue converts a column value to an attribute value by its Go
// type and database type.  NULL gives no attribute.
//
//   - Integers (tinyint to bigint) are ints, float and real doubles, bit a
//     bool, and decimal, numeric and money doubles (text if unparsable).
//   - Character types are strings; char and nchar lose their padding.
//   - datetime, datetime2 and smalldatetime are wall clocks in the log time
//     zone, sent as RFC 3339 UTC strings; datetimeoffset as RFC 3339 UTC;
//     date as YYYY-MM-DD and time as hh:mm:ss[.fffffff].
//   - uniqueidentifier is a GUID string; binary, varbinary, image and
//     rowversion are lower-case hex strings.
func attributeValue(v any, databaseType string, location *time.Location) (*commonpb.AnyValue, bool) {
	switch v := v.(type) {
	case nil:
		return nil, false
	case bool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}, true
	case int64:
		return intValue(v), true
	case int32:
		return intValue(int64(v)), true
	case int16:
		return intValue(int64(v)), true
	case int8:
		return intValue(int64(v)), true
	case uint8:
		return intValue(int64(v)), true
	case int:
		return intValue(int64(v)), true
	case float64:
		return doubleValue(v), true
	case float32:
		return doubleValue(float64(v)), true
	case string:
		if databaseType == "CHAR" || databaseType == "NCHAR" {
			v = strings.TrimRight(v, " ")
		}
		return stringValue(v), true
	case time.Time:
		switch databaseType {
		case "DATE":
			return stringValue(v.Format(time.DateOnly)), true
		case "TIME":
			return stringValue(v.Format("15:04:05.9999999")), true
		case "DATETIMEOFFSET":
			return stringValue(v.UTC().Format(time.RFC3339Nano)), true
		default:
			return stringValue(Real(v, location).UTC().Format(time.RFC3339Nano)), true
		}
	case []byte:
		switch databaseType {
		case "UNIQUEIDENTIFIER":
			var id mssql.UniqueIdentifier
			if err := id.Scan(v); err == nil {
				return stringValue(id.String()), true
			}
		case "DECIMAL", "NUMERIC", "MONEY", "SMALLMONEY":
			if f, err := strconv.ParseFloat(string(v), 64); err == nil {
				return doubleValue(f), true
			}
			return stringValue(string(v)), true
		}
		return stringValue(hex.EncodeToString(v)), true
	default:
		return stringValue(fmt.Sprint(v)), true
	}
}

// textOf returns v as text: strings as they are, byte slices as UTF-8, and
// anything else formatted.
func textOf(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

// optionalText is textOf with NULL as "".
func optionalText(v any) string {
	if v == nil {
		return ""
	}
	return textOf(v)
}

func stringValue(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

func intValue(i int64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: i}}
}

func doubleValue(f float64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: f}}
}

// unixNano returns t as OTLP nanoseconds since the Unix epoch; times before
// it give 0 (unknown).
func unixNano(t time.Time) uint64 {
	if ns := t.UnixNano(); ns > 0 {
		return uint64(ns)
	}
	return 0
}
