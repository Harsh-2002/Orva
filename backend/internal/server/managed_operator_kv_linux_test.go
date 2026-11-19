package server

import (
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
	f.expect(http.MethodPost, path+"/incr", writer, `{"delta":2}`, http.StatusOK)
	f.expect(http.MethodPost, path+"/cas", writer, `{"expected":3,"new":4}`, http.StatusOK)
	f.expect(http.MethodDelete, path, writer, "", http.StatusOK)
	f.expect(http.MethodGet, path, reader, "", http.StatusNotFound)
}
