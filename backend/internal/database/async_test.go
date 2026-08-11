package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTelemetryQueueDropsWhenSaturated(t *testing.T) {
	db := &Database{}
	db.writer = newAsyncWriter(db)
	for i := 0; i < cap(db.writer.telemetry)+1; i++ {
		db.AsyncExecTelemetry("SELECT 1")
	}
	stats := db.WriterStats()
	if stats.TelemetryDepth != stats.TelemetryCap || stats.DroppedTelemetry != 1 {
		t.Fatalf("writer stats=%+v", stats)
	}
}

func TestWriterRecordsPostEnqueueFailuresByPriority(t *testing.T) {
	db := newTestDB(t)
	if err := db.AsyncExecCritical(context.Background(), "not valid sql"); err != nil {
		t.Fatalf("enqueue critical: %v", err)
	}
	db.AsyncExecTelemetry("also not valid sql")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := db.WriterStats()
		if stats.CriticalFailures == 1 && stats.DroppedTelemetry == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("writer did not report commit failures: %+v", db.WriterStats())
}

func TestCriticalQueueHonorsDeadlineWhenSaturated(t *testing.T) {
	db := &Database{}
	db.writer = newAsyncWriter(db)
	for i := 0; i < cap(db.writer.critical); i++ {
		db.writer.critical <- writeJob{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := db.AsyncExecCritical(ctx, "SELECT 1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if db.WriterStats().CriticalTimeouts != 1 {
		t.Fatal("critical timeout was not counted")
	}
}
