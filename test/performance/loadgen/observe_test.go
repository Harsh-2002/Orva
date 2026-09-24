package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func metricFixture(overrides map[string]float64) string {
	var b strings.Builder
	for _, key := range observedMetrics {
		fmt.Fprintf(&b, "%s %g\n", key, overrides[key])
	}
	return b.String()
}

func TestParseWriterMetricsRejectsIncompleteOrInvalidScrapes(t *testing.T) {
	valid := metricFixture(map[string]float64{
		"orva_writer_committed_jobs_total{priority=\"critical\"}": 12,
	})
	got, err := parseWriterMetrics([]byte(valid))
	if err != nil || got["orva_writer_committed_jobs_total{priority=\"critical\"}"] != 12 {
		t.Fatalf("valid scrape: metrics=%v, err=%v", got, err)
	}
	for _, tc := range []struct {
		name string
		text string
	}{
		{"missing", strings.Replace(valid, "orva_writer_critical_failures_total 0\n", "", 1)},
		{"duplicate", valid + "orva_writer_critical_failures_total 0\n"},
		{"nan", strings.Replace(valid, "orva_writer_critical_failures_total 0", "orva_writer_critical_failures_total NaN", 1)},
		{"negative", strings.Replace(valid, "orva_writer_critical_failures_total 0", "orva_writer_critical_failures_total -1", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWriterMetrics([]byte(tc.text)); err == nil {
				t.Fatal("accepted incomplete or invalid metrics")
			}
		})
	}
}

func TestWriterDrainIncludesInflightBytes(t *testing.T) {
	critical, empty := int64(1), int64(0)
	snap := writerSnapshot{
		health:  writerHealth{Writer: &writerHealthBlock{CriticalBytes: &critical, ActivityBytes: &empty, TelemetryBytes: &empty}},
		metrics: map[string]float64{},
	}
	if writerDrained(snap) {
		t.Fatal("empty channels hid in-flight critical bytes")
	}
	critical = 0
	if !writerDrained(snap) {
		t.Fatal("empty writer did not drain")
	}
}

func TestRunObservedCapturesCommittedWrites(t *testing.T) {
	var active, committed, retained atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fn/test":
			active.Add(1)
			retained.Add(128)
			time.Sleep(5 * time.Millisecond)
			committed.Add(1)
			retained.Add(-128)
			active.Add(-1)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/system/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "healthy", "sandbox": map[string]any{"resource_limits": "cgroup_v2"},
				"writer": map[string]any{"critical_queue_bytes": retained.Load(),
					"activity_queue_bytes": 0, "telemetry_queue_bytes": 0},
			})
		case "/metrics":
			_, _ = fmt.Fprint(w, metricFixture(map[string]float64{
				"orva_active_requests": float64(active.Load()),
				"orva_writer_committed_jobs_total{priority=\"critical\"}": float64(committed.Load()),
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := runObserved(context.Background(), config{
		URLs: []string{server.URL + "/fn/test"}, Requests: 100,
		Concurrency: 10, Timeout: time.Second,
		ObserveURL: server.URL, DrainTimeout: time.Second,
		RequireCgroupV2: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status[200] != 100 || out.Writer == nil || !out.Writer.ObservationComplete || out.Writer.CriticalCommittedJobs != 100 ||
		out.Writer.CriticalFailures != 0 || out.Writer.DroppedTelemetry != 0 ||
		out.Writer.SandboxResourceLimits != "cgroup_v2" {
		t.Fatalf("observed report=%+v", out)
	}
}

func TestRunObservedRejectsUnenforcedSandboxBeforeLoad(t *testing.T) {
	var invoked atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fn/test":
			invoked.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/system/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "healthy", "sandbox": map[string]any{"resource_limits": "rlimit_only"},
				"writer": map[string]any{"critical_queue_bytes": 0,
					"activity_queue_bytes": 0, "telemetry_queue_bytes": 0},
			})
		case "/metrics":
			_, _ = fmt.Fprint(w, metricFixture(nil))
		}
	}))
	defer server.Close()
	_, err := runObserved(context.Background(), config{
		URLs: []string{server.URL + "/fn/test"}, Requests: 1,
		Concurrency: 1, Timeout: time.Second,
		ObserveURL: server.URL, DrainTimeout: time.Second,
		RequireCgroupV2: true,
	})
	if err == nil || invoked.Load() != 0 {
		t.Fatalf("unenforced sandbox admitted load: err=%v invoked=%d", err, invoked.Load())
	}
}

func TestObserveWriterRejectsMissingInFlightCounter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/system/health":
			_, _ = fmt.Fprint(w, `{"status":"healthy","sandbox":{"resource_limits":"cgroup_v2"},"writer":{"critical_queue_bytes":0,"activity_queue_bytes":0}}`)
		case "/metrics":
			_, _ = fmt.Fprint(w, metricFixture(nil))
		}
	}))
	defer server.Close()
	if _, err := observeWriter(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("missing telemetry in-flight bytes were mistaken for zero")
	}
}

func TestWriterDeltaRejectsCounterReset(t *testing.T) {
	before := writerSnapshot{metrics: map[string]float64{
		"orva_writer_dropped_telemetry_total": 10,
	}}
	after := writerSnapshot{metrics: map[string]float64{
		"orva_writer_dropped_telemetry_total": 2,
	}}
	if _, err := writerDelta(before, after, writerPeaks{}, time.Second, 0); err == nil {
		t.Fatal("counter reset could be mistaken for zero telemetry loss")
	}
}

func TestWriterDeltaReportsBatchAndQueueWait(t *testing.T) {
	beforeMetrics, err := parseWriterMetrics([]byte(metricFixture(map[string]float64{
		"orva_writer_batch_attempts_total{priority=\"critical\"}":     5,
		"orva_writer_committed_jobs_total{priority=\"critical\"}":     20,
		"orva_writer_queue_wait_seconds_total{priority=\"critical\"}": 10,
		"orva_writer_queue_wait_samples_total{priority=\"critical\"}": 20,
	})))
	if err != nil {
		t.Fatal(err)
	}
	afterMetrics, err := parseWriterMetrics([]byte(metricFixture(map[string]float64{
		"orva_writer_batch_attempts_total{priority=\"critical\"}":     15,
		"orva_writer_batch_attempts_total{priority=\"activity\"}":     3,
		"orva_writer_batch_attempts_total{priority=\"telemetry\"}":    2,
		"orva_writer_committed_jobs_total{priority=\"critical\"}":     120,
		"orva_writer_queue_wait_seconds_total{priority=\"critical\"}": 35,
		"orva_writer_queue_wait_samples_total{priority=\"critical\"}": 120,
	})))
	if err != nil {
		t.Fatal(err)
	}
	before := writerSnapshot{metrics: beforeMetrics}
	after := writerSnapshot{metrics: afterMetrics}
	after.health.Sandbox.ResourceLimits = "cgroup_v2"
	report, err := writerDelta(before, after, writerPeaks{}, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.CriticalBatchAttempts != 10 || report.ActivityBatchAttempts != 3 ||
		report.TelemetryBatchAttempts != 2 || report.CriticalCommittedJobs != 100 ||
		report.CriticalQueueWaitSecs != 25 || report.CriticalQueueWaitCount != 100 ||
		!report.ObservationComplete {
		t.Fatalf("writer delta=%+v", report)
	}
}

func TestValidateObserverOriginAndDrainBudget(t *testing.T) {
	base := config{URLs: []string{"http://scratch.example/fn/test"}, Requests: 1,
		Concurrency: 1, Timeout: time.Second, ObserveURL: "http://scratch.example",
		DrainTimeout: time.Second}
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*config)
	}{
		{"different server", func(c *config) { c.ObserveURL = "http://production.example" }},
		{"server path", func(c *config) { c.ObserveURL += "/fn/test" }},
		{"missing drain budget", func(c *config) { c.DrainTimeout = 0 }},
		{"cgroup without observation", func(c *config) { c.ObserveURL = ""; c.RequireCgroupV2 = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.edit(&c)
			if err := validate(c); err == nil {
				t.Fatal("accepted unsafe observation configuration")
			}
		})
	}
}

func TestWaitWriterDrainTimesOutOnInflightBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/system/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "healthy", "sandbox": map[string]any{"resource_limits": "cgroup_v2"},
				"writer": map[string]any{"critical_queue_bytes": 128,
					"activity_queue_bytes": 0, "telemetry_queue_bytes": 0},
			})
		case "/metrics":
			_, _ = fmt.Fprint(w, metricFixture(nil))
		}
	}))
	defer server.Close()
	var peaks writerPeaks
	_, err := waitWriterDrain(context.Background(), server.Client(), server.URL, 120*time.Millisecond, &peaks)
	if err == nil || peaks.CriticalBytes != 128 {
		t.Fatalf("in-flight work appeared drained: err=%v peaks=%+v", err, peaks)
	}
}

func TestRunObservedMarksIncompleteWriterReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fn/test":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/system/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "healthy", "sandbox": map[string]any{"resource_limits": "cgroup_v2"},
				"writer": map[string]any{"critical_queue_bytes": 128,
					"activity_queue_bytes": 0, "telemetry_queue_bytes": 0},
			})
		case "/metrics":
			_, _ = fmt.Fprint(w, metricFixture(nil))
		}
	}))
	defer server.Close()
	out, err := runObserved(context.Background(), config{
		URLs: []string{server.URL + "/fn/test"}, Requests: 1,
		Concurrency: 1, Timeout: time.Second,
		ObserveURL: server.URL, DrainTimeout: 120 * time.Millisecond,
	})
	if err == nil || out.Status[200] != 1 || out.Writer == nil ||
		out.Writer.ObservationComplete || out.Writer.ObservationError == "" {
		t.Fatalf("partial run hid drain failure: report=%+v err=%v", out, err)
	}
}
