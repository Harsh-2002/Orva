package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const observationLimit = 2 << 20

var observedMetrics = []string{
	"orva_active_requests",
	"orva_writer_queue_depth{priority=\"critical\"}",
	"orva_writer_queue_depth{priority=\"activity\"}",
	"orva_writer_queue_depth{priority=\"telemetry\"}",
	"orva_writer_committed_jobs_total{priority=\"critical\"}",
	"orva_writer_committed_jobs_total{priority=\"activity\"}",
	"orva_writer_committed_jobs_total{priority=\"telemetry\"}",
	"orva_writer_connection_wait_seconds_total{priority=\"critical\"}",
	"orva_writer_statement_seconds_total{priority=\"critical\"}",
	"orva_writer_commit_seconds_total{priority=\"critical\"}",
	"orva_writer_critical_timeouts_total",
	"orva_writer_critical_failures_total",
	"orva_writer_dropped_telemetry_total",
	"orva_writer_dropped_activity_total",
}

type writerHealth struct {
	Status  string `json:"status"`
	Sandbox struct {
		ResourceLimits string `json:"resource_limits"`
	} `json:"sandbox"`
	Writer *writerHealthBlock `json:"writer"`
}

type writerHealthBlock struct {
	CriticalBytes  *int64 `json:"critical_queue_bytes"`
	ActivityBytes  *int64 `json:"activity_queue_bytes"`
	TelemetryBytes *int64 `json:"telemetry_queue_bytes"`
}

type writerSnapshot struct {
	health  writerHealth
	metrics map[string]float64
}

type writerPeaks struct {
	ActiveRequests int64 `json:"active_requests"`
	CriticalDepth  int64 `json:"critical_depth"`
	ActivityDepth  int64 `json:"activity_depth"`
	TelemetryDepth int64 `json:"telemetry_depth"`
	CriticalBytes  int64 `json:"critical_bytes"`
	ActivityBytes  int64 `json:"activity_bytes"`
	TelemetryBytes int64 `json:"telemetry_bytes"`
}

type writerReport struct {
	ObservationComplete    bool        `json:"observation_complete"`
	ObservationError       string      `json:"observation_error,omitempty"`
	SandboxResourceLimits  string      `json:"sandbox_resource_limits"`
	DrainSeconds           float64     `json:"drain_seconds"`
	ObservationErrors      int         `json:"observation_errors"`
	Peak                   writerPeaks `json:"peak"`
	CriticalCommittedJobs  int64       `json:"critical_committed_jobs"`
	ActivityCommittedJobs  int64       `json:"activity_committed_jobs"`
	TelemetryCommittedJobs int64       `json:"telemetry_committed_jobs"`
	CriticalFailures       int64       `json:"critical_failures"`
	CriticalTimeouts       int64       `json:"critical_timeouts"`
	DroppedTelemetry       int64       `json:"dropped_telemetry"`
	DroppedActivity        int64       `json:"dropped_activity"`
	CriticalConnectionSecs float64     `json:"critical_connection_seconds"`
	CriticalStatementSecs  float64     `json:"critical_statement_seconds"`
	CriticalCommitSecs     float64     `json:"critical_commit_seconds"`
}

func (p *writerPeaks) update(s writerSnapshot) {
	maxInto := func(dst *int64, n int64) {
		if n > *dst {
			*dst = n
		}
	}
	maxInto(&p.ActiveRequests, int64(s.metrics["orva_active_requests"]))
	maxInto(&p.CriticalDepth, int64(s.metrics["orva_writer_queue_depth{priority=\"critical\"}"]))
	maxInto(&p.ActivityDepth, int64(s.metrics["orva_writer_queue_depth{priority=\"activity\"}"]))
	maxInto(&p.TelemetryDepth, int64(s.metrics["orva_writer_queue_depth{priority=\"telemetry\"}"]))
	maxInto(&p.CriticalBytes, *s.health.Writer.CriticalBytes)
	maxInto(&p.ActivityBytes, *s.health.Writer.ActivityBytes)
	maxInto(&p.TelemetryBytes, *s.health.Writer.TelemetryBytes)
}

func parseWriterMetrics(data []byte) (map[string]float64, error) {
	want := make(map[string]struct{}, len(observedMetrics))
	for _, key := range observedMetrics {
		want[key] = struct{}{}
	}
	got := make(map[string]float64, len(want))
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		if _, ok := want[parts[0]]; !ok {
			continue
		}
		if _, exists := got[parts[0]]; exists {
			return nil, fmt.Errorf("duplicate writer metric %q", parts[0])
		}
		value, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, fmt.Errorf("invalid writer metric %q", parts[0])
		}
		got[parts[0]] = value
	}
	for _, key := range observedMetrics {
		if _, ok := got[key]; !ok {
			return nil, fmt.Errorf("missing writer metric %q", key)
		}
	}
	return got, nil
}

func getObservation(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, observationLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > observationLimit {
		return nil, fmt.Errorf("%s exceeds observation limit", endpoint)
	}
	return data, nil
}

func observeWriter(ctx context.Context, client *http.Client, origin string) (writerSnapshot, error) {
	origin = strings.TrimRight(origin, "/")
	healthData, err := getObservation(ctx, client, origin+"/api/v1/system/health")
	if err != nil {
		return writerSnapshot{}, fmt.Errorf("health observation: %w", err)
	}
	var health writerHealth
	if err := json.Unmarshal(healthData, &health); err != nil {
		return writerSnapshot{}, fmt.Errorf("decode health observation: %w", err)
	}
	if health.Status != "healthy" || health.Sandbox.ResourceLimits == "" || health.Writer == nil ||
		health.Writer.CriticalBytes == nil || health.Writer.ActivityBytes == nil ||
		health.Writer.TelemetryBytes == nil {
		return writerSnapshot{}, errors.New("observed server is not healthy or lacks writer/sandbox status")
	}
	metricsData, err := getObservation(ctx, client, origin+"/metrics")
	if err != nil {
		return writerSnapshot{}, fmt.Errorf("metrics observation: %w", err)
	}
	metrics, err := parseWriterMetrics(metricsData)
	if err != nil {
		return writerSnapshot{}, err
	}
	return writerSnapshot{health: health, metrics: metrics}, nil
}

func writerDrained(s writerSnapshot) bool {
	return s.metrics["orva_active_requests"] == 0 &&
		s.metrics["orva_writer_queue_depth{priority=\"critical\"}"] == 0 &&
		s.metrics["orva_writer_queue_depth{priority=\"activity\"}"] == 0 &&
		s.metrics["orva_writer_queue_depth{priority=\"telemetry\"}"] == 0 &&
		*s.health.Writer.CriticalBytes == 0 && *s.health.Writer.ActivityBytes == 0 &&
		*s.health.Writer.TelemetryBytes == 0
}

func waitWriterDrain(ctx context.Context, client *http.Client, origin string, timeout time.Duration, peaks *writerPeaks) (writerSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stable := 0
	for {
		snap, err := observeWriter(ctx, client, origin)
		if err != nil {
			return writerSnapshot{}, err
		}
		peaks.update(snap)
		if writerDrained(snap) {
			stable++
			if stable == 2 {
				return snap, nil
			}
		} else {
			stable = 0
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return writerSnapshot{}, fmt.Errorf("writer drain did not finish: %w", ctx.Err())
		}
	}
}

func writerDelta(before, after writerSnapshot, peaks writerPeaks, elapsed time.Duration, observationErrors int) (*writerReport, error) {
	delta := func(key string) (float64, error) {
		a, b := after.metrics[key], before.metrics[key]
		if a < b {
			return 0, fmt.Errorf("writer metric %q regressed (server restarted?)", key)
		}
		return a - b, nil
	}
	values := make(map[string]float64, len(observedMetrics))
	for _, key := range observedMetrics {
		if strings.Contains(key, "queue_depth") || key == "orva_active_requests" {
			continue
		}
		var err error
		values[key], err = delta(key)
		if err != nil {
			return nil, err
		}
	}
	value := func(name string) float64 { return values[name] }
	return &writerReport{
		ObservationComplete:    observationErrors == 0,
		SandboxResourceLimits:  after.health.Sandbox.ResourceLimits,
		DrainSeconds:           elapsed.Seconds(),
		ObservationErrors:      observationErrors,
		Peak:                   peaks,
		CriticalCommittedJobs:  int64(value("orva_writer_committed_jobs_total{priority=\"critical\"}")),
		ActivityCommittedJobs:  int64(value("orva_writer_committed_jobs_total{priority=\"activity\"}")),
		TelemetryCommittedJobs: int64(value("orva_writer_committed_jobs_total{priority=\"telemetry\"}")),
		CriticalFailures:       int64(value("orva_writer_critical_failures_total")),
		CriticalTimeouts:       int64(value("orva_writer_critical_timeouts_total")),
		DroppedTelemetry:       int64(value("orva_writer_dropped_telemetry_total")),
		DroppedActivity:        int64(value("orva_writer_dropped_activity_total")),
		CriticalConnectionSecs: value("orva_writer_connection_wait_seconds_total{priority=\"critical\"}"),
		CriticalStatementSecs:  value("orva_writer_statement_seconds_total{priority=\"critical\"}"),
		CriticalCommitSecs:     value("orva_writer_commit_seconds_total{priority=\"critical\"}"),
	}, nil
}

func runObserved(ctx context.Context, c config) (report, error) {
	if err := validate(c); err != nil {
		return report{}, err
	}
	if c.ObserveURL == "" {
		return run(ctx, c)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	before, err := observeWriter(ctx, client, c.ObserveURL)
	if err != nil {
		return report{}, err
	}
	if c.RequireCgroupV2 && before.health.Sandbox.ResourceLimits != "cgroup_v2" {
		return report{}, fmt.Errorf("observed sandbox limits are %q, need cgroup_v2", before.health.Sandbox.ResourceLimits)
	}
	stopped := make(chan struct{})
	observed := make(chan struct {
		peaks  writerPeaks
		errors int
	}, 1)
	go func() {
		var peaks writerPeaks
		peaks.update(before)
		errors := 0
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				snap, err := observeWriter(ctx, client, c.ObserveURL)
				if err != nil {
					errors++
				} else {
					peaks.update(snap)
				}
			case <-stopped:
				observed <- struct {
					peaks  writerPeaks
					errors int
				}{peaks, errors}
				return
			}
		}
	}()
	out, runErr := run(ctx, c)
	close(stopped)
	partial := <-observed
	if runErr != nil {
		return out, runErr
	}
	drainStart := time.Now()
	after, err := waitWriterDrain(ctx, client, c.ObserveURL, c.DrainTimeout, &partial.peaks)
	if err != nil {
		out.Writer = &writerReport{ObservationError: err.Error(), ObservationErrors: partial.errors + 1, Peak: partial.peaks}
		return out, err
	}
	out.Writer, err = writerDelta(before, after, partial.peaks, time.Since(drainStart), partial.errors)
	if err != nil {
		out.Writer = &writerReport{ObservationError: err.Error(), ObservationErrors: partial.errors + 1, Peak: partial.peaks}
		return out, err
	}
	if partial.errors > 0 {
		out.Writer.ObservationError = "writer observation failed during load"
		return out, errors.New(out.Writer.ObservationError)
	}
	return out, nil
}
