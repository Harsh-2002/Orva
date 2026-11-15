package sdkhost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

func put(t *testing.T, h *Host, key, val string) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"key": key, "value": val})
	if _, e := h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, "kv.put", args); e != nil {
		t.Fatalf("put %s: %+v", key, e)
	}
}

// A list page of large values is cut at the reply bound with a cursor, and
// following the cursors yields every key exactly once, in order.
func TestListLargePagesAreCutAndResumable(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	val := strings.Repeat("v", 60_000)
	const n = 60 // ~3.6 MB in total: more than one reply may carry
	for i := 0; i < n; i++ {
		put(t, h, fmt.Sprintf("big-%03d", i), val)
	}
	var seen []string
	cursor, pages := "", 0
	for {
		a, _ := json.Marshal(map[string]any{"prefix": "big-", "limit": 1000, "cursor": cursor})
		r, e := h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, "kv.list", a)
		if e != nil {
			t.Fatalf("list: %+v", e)
		}
		raw, _ := json.Marshal(r)
		if len(raw) > maxListReplyBytes+len(val)+4096 {
			t.Fatalf("page of %d bytes exceeds the reply bound %d", len(raw), maxListReplyBytes)
		}
		var page struct {
			Keys       []struct{ Key string } `json:"keys"`
			NextCursor string                 `json:"next_cursor"`
		}
		_ = json.Unmarshal(raw, &page)
		for _, k := range page.Keys {
			seen = append(seen, k.Key)
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > n {
			t.Fatal("cursor does not advance")
		}
	}
	if pages < 2 {
		t.Fatalf("expected the result to be split across pages, got %d", pages)
	}
	if len(seen) != n {
		t.Fatalf("saw %d keys, want %d", len(seen), n)
	}
	for i, k := range seen {
		if k != fmt.Sprintf("big-%03d", i) {
			t.Fatalf("key %d = %s: out of order or duplicated", i, k)
		}
	}
}

// A limit above the database slice size still returns exactly that many small entries.
func TestListLimitAboveSliceSize(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	for i := 0; i < 250; i++ {
		put(t, h, fmt.Sprintf("s-%03d", i), "x")
	}
	a, _ := json.Marshal(map[string]any{"prefix": "s-", "limit": 200})
	r, e := h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, "kv.list", a)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r)
	var page struct {
		Keys       []struct{ Key string } `json:"keys"`
		NextCursor string                 `json:"next_cursor"`
	}
	_ = json.Unmarshal(raw, &page)
	if len(page.Keys) != 200 || page.NextCursor != "s-199" {
		t.Fatalf("got %d keys, cursor %q; want 200, s-199", len(page.Keys), page.NextCursor)
	}
}

func TestBatchResultsOverTheReplyBoundAreRefused(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	h := New(db)
	val := strings.Repeat("v", 60_000)
	var ops []map[string]any
	for i := 0; i < 60; i++ {
		put(t, h, fmt.Sprintf("b-%02d", i), val)
		ops = append(ops, map[string]any{"op": "get", "key": fmt.Sprintf("b-%02d", i)})
	}
	a, _ := json.Marshal(map[string]any{"ops": ops})
	_, e := h.Handle(context.Background(), sandbox.SDKCaller{FunctionID: fnA}, "kv.batch", a)
	if e == nil || e.Status != 413 || e.Code != "RESULT_TOO_LARGE" {
		t.Fatalf("err = %+v, want 413 RESULT_TOO_LARGE", e)
	}
}
