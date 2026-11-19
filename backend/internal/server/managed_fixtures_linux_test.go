package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestManagedFixtureRESTWorkflow(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("fixture-reader", `["read"]`)
	writer, _ := f.key("fixture-writer", `["write"]`)
	byName := "/api/v1/functions/managed/fixtures"
	byID := "/api/v1/functions/" + f.owner + "/fixtures"
	item := byID + "/smoke"
	const create = `{"name":"smoke","method":"POST","path":"/check","headers":{"X-Test":"preset"},"body":"initial"}`
	const update = `{"name":"ignored","method":"PUT","path":"/check","headers":{"X-Test":"preset"},"body":"updated"}`

	for _, route := range []struct{ method, path, body, wrongKey string }{
		{http.MethodGet, byName, "", writer},
		{http.MethodGet, item, "", writer},
		{http.MethodPost, byName, create, reader},
		{http.MethodPut, item, update, reader},
		{http.MethodDelete, item, "", reader},
	} {
		f.expect(route.method, route.path, "", route.body, http.StatusUnauthorized)
		f.expect(route.method, route.path, route.wrongKey, route.body, http.StatusForbidden)
	}
	assertList := func(want int) {
		t.Helper()
		rec := f.expect(http.MethodGet, byName, reader, "", http.StatusOK)
		var result struct{ Fixtures []json.RawMessage }
		managedMust(t, json.Unmarshal(rec.Body.Bytes(), &result))
		if result.Fixtures == nil || len(result.Fixtures) != want {
			t.Fatalf("want %d fixtures in an array, got %s", want, rec.Body.String())
		}
	}
	assertList(0)
	f.expect(http.MethodGet, "/api/v1/functions/missing/fixtures", reader, "", http.StatusNotFound)
	f.expect(http.MethodPost, byName, writer, create, http.StatusCreated)
	f.expect(http.MethodPost, byID, writer, create, http.StatusConflict)
	assertList(1)
	f.expect(http.MethodPut, item, writer, update, http.StatusOK)
	rec := f.expect(http.MethodGet, byName+"/smoke", reader, "", http.StatusOK)
	var saved struct {
		FunctionID string `json:"function_id"`
		Name       string
		Method     string
		Path       string
		Headers    map[string]string
		Body       string
	}
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	if saved.FunctionID != f.owner || saved.Name != "smoke" || saved.Method != "PUT" ||
		saved.Path != "/check" || saved.Headers["X-Test"] != "preset" || saved.Body != "updated" {
		t.Fatalf("unexpected saved fixture: %s", rec.Body.String())
	}
	f.expect(http.MethodDelete, item, writer, "", http.StatusNoContent)
	f.expect(http.MethodDelete, item, writer, "", http.StatusNoContent)
	f.expect(http.MethodGet, item, reader, "", http.StatusNotFound)
	assertList(0)
}
