// Package normalization contains thin parsers that emit the shared Heartbeat
// application event contract. OTLP receiving and fan-out remain delegated to
// the stock OpenTelemetry Collector.
package normalization

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ApplicationEvent mirrors packages/telemetry-contracts/src/application_event.schema.json.
type ApplicationEvent struct {
	Timestamp   time.Time      `json:"timestamp"`
	Environment string         `json:"environment"`
	Application string         `json:"application"`
	Component   string         `json:"component,omitempty"`
	Platform    string         `json:"platform,omitempty"`
	Severity    string         `json:"severity"`
	Message     string         `json:"message"`
	UserID      string         `json:"user_id,omitempty"`
	SessionID   string         `json:"session_id,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	ClientIP    string         `json:"client_ip,omitempty"`
	Labels      map[string]any `json:"labels,omitempty"`
}

// ParseApplicationEvent validates a JSON event and normalises a few common
// field names into the shared Heartbeat event envelope.
func ParseApplicationEvent(raw []byte) (ApplicationEvent, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ApplicationEvent{}, fmt.Errorf("parse application event: %w", err)
	}
	event := ApplicationEvent{
		Environment: firstString(payload, "environment", "env"),
		Application: firstString(payload, "application", "app", "module"),
		Component:   firstString(payload, "component", "action", "screen", "operation"),
		Platform:    firstString(payload, "platform"),
		Severity:    strings.ToLower(firstString(payload, "severity", "level")),
		Message:     firstString(payload, "message", "msg", "text"),
		UserID:      firstString(payload, "user_id", "userId", "user"),
		SessionID:   firstString(payload, "session_id", "sessionId", "session"),
		RequestID:   firstString(payload, "request_id", "requestId", "trace_id", "traceId"),
		ClientIP:    firstString(payload, "client_ip", "clientIp", "ip"),
		Labels:      labels(payload),
	}
	if event.Severity == "" {
		event.Severity = "info"
	}
	timestamp := firstString(payload, "timestamp", "time")
	if timestamp == "" {
		event.Timestamp = time.Now().UTC()
	} else {
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return ApplicationEvent{}, fmt.Errorf("timestamp must be RFC3339: %w", err)
		}
		event.Timestamp = parsed.UTC()
	}
	if event.Environment == "" || event.Application == "" || event.Message == "" {
		return ApplicationEvent{}, fmt.Errorf("environment, application, and message are required")
	}
	return event, nil
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func labels(payload map[string]any) map[string]any {
	raw, ok := payload["labels"].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for key, value := range raw {
		switch value.(type) {
		case string, float64, bool:
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
