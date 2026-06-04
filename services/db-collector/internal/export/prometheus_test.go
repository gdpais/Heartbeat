package export

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestExporterRecordsSamples(t *testing.T) {
	reg := prometheus.NewRegistry()
	exporter := NewPrometheusExporter(reg)

	samples := []Sample{
		{
			Metric: "heartbeat_sqlserver_wait_seconds",
			Value:  42,
			Labels: map[string]string{
				"environment": "prod",
				"target":      "core-db",
				"category":    "lck_m_x",
			},
		},
	}
	if err := exporter.Record(samples); err != nil {
		t.Fatalf("Record: %v", err)
	}

	metricValue := testutil.ToFloat64(exporter.gauges["heartbeat_sqlserver_wait_seconds"].WithLabelValues("lck_m_x", "prod", "core-db"))
	if metricValue != 42 {
		t.Fatalf("unexpected metric value: %v", metricValue)
	}
}
