package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/metrics"
)

// TestMetricsExpositionFormat is the L2 guard: the /metrics text must carry
// Prometheus `# TYPE` lines and must NOT declare orva_invocation_duration_ms as
// both a summary (quantile=…) and a histogram (le=…), which is invalid
// OpenMetrics and made strict scrapers reject the whole exposition.
func TestMetricsExpositionFormat(t *testing.T) {
	h := &SystemHandler{Metrics: metrics.New()}
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	h.GetMetrics(w, req)
	body := w.Body.String()

	// Every family must have a TYPE line.
	for _, want := range []string{
		"# TYPE orva_invocations_total counter",
		"# TYPE orva_active_requests gauge",
		"# TYPE orva_invocation_duration_ms histogram",
		"# HELP orva_invocations_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, body)
		}
	}

	// The duplicate summary series must be gone: no quantile labels on the
	// duration family.
	if strings.Contains(body, "orva_invocation_duration_ms{quantile=") {
		t.Errorf("duration family still emits summary quantile series (duplicate declaration)\n---\n%s", body)
	}

	// The histogram series must still be present.
	if !strings.Contains(body, "orva_invocation_duration_ms_bucket{le=") {
		t.Errorf("duration histogram buckets missing\n---\n%s", body)
	}
}

func TestKVMetricsAndWriterSaturationAreExposed(t *testing.T) {
	db := newTestDB(t)
	h := &SystemHandler{Metrics: metrics.New(), DB: db}
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	h.GetMetrics(w, req)
	body := w.Body.String()
	for _, want := range []string{
		"# TYPE orva_kv_operations_total counter",
		"orva_kv_operations_total{operation=\"put\"}",
		"# TYPE orva_kv_batch_rollbacks_total counter",
		"# TYPE orva_writer_queue_depth gauge",
		"# TYPE orva_writer_critical_failures_total counter",
		"# TYPE orva_writer_dropped_telemetry_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
