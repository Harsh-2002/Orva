package trace

import (
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTraceIDAtPreservesTimePrefixAndRandomSuffix(t *testing.T) {
	random := [10]byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9}
	id := traceIDAt(time.UnixMilli(0x010203040506), random)
	if want := "tr_010203040506a0a1a2a3a4a5a6a7a8a9"; id != want {
		t.Fatalf("trace ID = %q, want %q", id, want)
	}
	if _, err := hex.DecodeString(id[3:]); err != nil {
		t.Fatal(err)
	}
	if later := traceIDAt(time.UnixMilli(0x010203040507), random); later <= id {
		t.Fatalf("later local trace ID did not sort after %q: %q", id, later)
	}
}

func TestNewTraceIDUsesCurrentTimeAndUniqueSuffix(t *testing.T) {
	before := uint64(time.Now().UnixMilli())
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := NewTraceID()
		if len(id) != 35 || !strings.HasPrefix(id, "tr_") {
			t.Fatalf("invalid trace ID: %q", id)
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate trace ID: %q", id)
		}
		seen[id] = struct{}{}
		raw, err := hex.DecodeString(id[3:])
		if err != nil {
			t.Fatal(err)
		}
		var millis uint64
		for _, b := range raw[:6] {
			millis = millis<<8 | uint64(b)
		}
		if after := uint64(time.Now().UnixMilli()); millis < before || millis > after {
			t.Fatalf("trace ID timestamp %d outside generation window [%d,%d]", millis, before, after)
		}
	}
}

func TestIncomingTraceparentRemainsUnchanged(t *testing.T) {
	const upstream = "a76d2714dc124184b7f50904ff9c9913"
	req := httptest.NewRequest("GET", "/fn/id", nil)
	req.Header.Set("Traceparent", "00-"+upstream+"-ef3c1c54c1d4399b-01")
	traceID, parent := FromHTTPRequest(req)
	if traceID != "tr_"+upstream || parent != "sp_ef3c1c54c1d4399b" {
		t.Fatalf("external trace context was rewritten: trace=%q parent=%q", traceID, parent)
	}
}
