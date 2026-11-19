package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestManagedOperatorKV(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["write"]`)
	base := "/api/v1/functions/" + f.owner + "/kv"
	path := base + "/counter"
	f.expect(http.MethodGet, base, "", "", http.StatusUnauthorized)
	f.expect(http.MethodGet, base, writer, "", http.StatusForbidden)
	f.expect(http.MethodPut, path, reader, `{"value":1}`, http.StatusForbidden)
	f.expect(http.MethodPut, path, writer, `{"value":1}`, http.StatusOK)
	f.expect(http.MethodGet, base, reader, "", http.StatusOK)
	f.expect(http.MethodGet, "/api/v1/functions/managed/kv/counter", reader, "", http.StatusOK)
	incr := f.expect(http.MethodPost, path+"/incr", writer, `{"delta":2}`, http.StatusOK)
	var counter struct{ Value int }
	managedMust(t, json.Unmarshal(incr.Body.Bytes(), &counter))
	if counter.Value != 3 {
		t.Fatalf("increment returned %d, want 3", counter.Value)
	}
	cas := f.expect(http.MethodPost, path+"/cas", writer, `{"expected":3,"new":4}`, http.StatusOK)
	var swap struct{ OK bool }
	managedMust(t, json.Unmarshal(cas.Body.Bytes(), &swap))
	if !swap.OK {
		t.Fatal("compare-and-set did not swap")
	}
	result := f.expect(http.MethodGet, path, reader, "", http.StatusOK)
	managedMust(t, json.Unmarshal(result.Body.Bytes(), &counter))
	if counter.Value != 4 {
		t.Fatalf("read returned %d after swap, want 4", counter.Value)
	}
	f.expect(http.MethodDelete, path, writer, "", http.StatusOK)
	f.expect(http.MethodGet, path, reader, "", http.StatusNotFound)
}
