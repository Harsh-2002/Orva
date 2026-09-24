package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestMetricsJSONSeparatesHandlerResponseFromWorkerDuration(t *testing.T) {
	m := metrics.New()
	m.RecordDuration(7 * time.Millisecond)
	m.RecordResponseDuration(75 * time.Millisecond)
	h := &SystemHandler{Metrics: m}
	w := httptest.NewRecorder()
	h.GetMetricsJSON(w, httptest.NewRequest("GET", "/api/v1/system/metrics.json", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		LatencyMS         latencyBlock `json:"latency_ms"`
		ResponseLatencyMS latencyBlock `json:"response_latency_ms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.LatencyMS.P50 != 7 || body.ResponseLatencyMS.P50 != 75 {
		t.Fatalf("latency=%+v response=%+v", body.LatencyMS, body.ResponseLatencyMS)
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
		"orva_writer_queue_depth{priority=\"activity\"}",
		"# TYPE orva_writer_batch_attempts_total counter",
		"orva_writer_statement_seconds_total{priority=\"critical\"}",
		"orva_writer_queue_wait_samples_total{priority=\"critical\"}",
		"# TYPE orva_writer_critical_failures_total counter",
		"# TYPE orva_writer_dropped_telemetry_total counter",
		"# TYPE orva_writer_dropped_activity_total counter",
		"# TYPE orva_writer_deleted_function_writes_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
