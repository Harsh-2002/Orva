package database

import (
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type SpanOfferInspection struct {
	Decision workloadiam.SpanDecision
	Job      any
	Args     []any
	Bytes    int
}

// PauseSpanOffersForTest exposes detached jobs only to the external host test.
func PauseSpanOffersForTest(t *testing.T, db *Database) func() SpanOfferInspection {
	t.Helper()
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)
	t.Cleanup(db.writer.start)
	return func() SpanOfferInspection {
		t.Helper()
		select {
		case j := <-db.writer.telemetry:
			db.writer.telemetryBytes.Add(-int64(j.bytes))
			j.enqueuedAt = 0
			return SpanOfferInspection{j.spanDecision, j, append([]any(nil), j.args...), j.bytes}
		case <-time.After(time.Second):
			t.Fatal("no admitted span offer")
			return SpanOfferInspection{}
		}
	}
}
