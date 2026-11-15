package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

func TestSDKPermissionCreateUpdateList(t *testing.T) {
	db := newTestDB(t)
	h := &FunctionHandler{DB: db, Registry: registry.New(db)}
	for _, network := range []string{"none", "egress"} {
		t.Run(network, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/functions", strings.NewReader(
				`{"name":"sdk-`+network+`","runtime":"node","network_mode":"`+network+`"}`)))
			if rec.Code != http.StatusCreated {
				t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
			}
			var fn database.Function
			if err := json.Unmarshal(rec.Body.Bytes(), &fn); err != nil || fn.SDK {
				t.Fatalf("create sdk default: %+v (%v)", fn, err)
			}
			drains, refreshes := 0, 0
			h.PoolDrain = func(id string) { drains++ }
			h.PoolRefresh = func(id string) { refreshes++ }
			for _, body := range []string{`{"sdk":true}`, `{}`, `{"sdk":false}`} {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/functions/"+fn.ID, strings.NewReader(body))
				req.SetPathValue("id", fn.ID)
				rec = httptest.NewRecorder()
				h.Update(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("update %d: %s", rec.Code, rec.Body.String())
				}
				want := body != `{"sdk":false}`
				var fields map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil || fields["sdk"] != want {
					t.Fatalf("update JSON %s (%v)", rec.Body.String(), err)
				}
				list := httptest.NewRecorder()
				h.List(list, httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil))
				if list.Code != http.StatusOK {
					t.Fatalf("list %d: %s", list.Code, list.Body.String())
				}
				var result database.ListFunctionsResult
				if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				for _, row := range result.Functions {
					if row.ID == fn.ID && row.SDK != want {
						t.Fatalf("list sdk=%v, want %v", row.SDK, want)
					}
				}
			}
			if drains != 2 || refreshes != 0 {
				t.Fatalf("toggles: hard drains=%d soft refreshes=%d", drains, refreshes)
			}
		})
	}
}

func TestSDKPermissionCreateExplicitOptIn(t *testing.T) {
	db := newTestDB(t)
	h := &FunctionHandler{DB: db, Registry: registry.New(db)}
	for _, network := range []string{"none", "egress"} {
		rec := httptest.NewRecorder()
		h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/functions", strings.NewReader(
			`{"name":"sdk-`+network+`","runtime":"node","network_mode":"`+network+`","sdk":true}`)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
		}
		fn, err := db.GetFunctionByName("sdk-" + network)
		if err != nil || !fn.SDK || fn.AuthMode != database.AuthModeNone {
			t.Fatalf("explicit SDK opt-in changed incoming auth or was lost: %+v (%v)", fn, err)
		}
	}
}

func TestSDKPermissionRollbackRestoresSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, network string
		sdk, legacy   bool
	}{
		{"explicit-true-none", "none", true, false},
		{"explicit-false-egress", "egress", false, false},
		{"legacy-egress", "egress", true, true},
		{"legacy-none", "none", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			fn := &database.Function{
				ID: "019df200-7b00-7e00-9c00-aab1cd2e3f41", Name: "rollback-sdk", Runtime: "node",
				Entrypoint: "handler.js", Status: "active", SDK: tc.sdk, NetworkMode: tc.network,
				CodeHash: strings.Repeat("a", 64), MemoryMB: 64, CPUs: 1, TimeoutMS: 1000,
			}
			if err := db.InsertFunction(fn); err != nil {
				t.Fatal(err)
			}
			dep := &database.Deployment{
				ID: "019df200-7b00-7e00-9c00-aab1cd2e3f42", FunctionID: fn.ID, Version: 1,
				Status: "succeeded", CodeHash: fn.CodeHash, Snapshot: database.SnapshotFromFunction(fn),
			}
			if err := db.InsertDeployment(dep); err != nil {
				t.Fatal(err)
			}
			if err := db.SetDeploymentSnapshot(dep.ID, dep.Snapshot); err != nil {
				t.Fatal(err)
			}
			if tc.legacy {
				for _, stmt := range []string{
					`ALTER TABLE functions DROP COLUMN sdk`,
					`UPDATE deployments SET snapshot = json_remove(snapshot, '$.sdk')`,
				} {
					if _, err := db.WriteDB().Exec(stmt); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Migrate(); err != nil {
					t.Fatal(err)
				}
			}
			// Only permission differs: unchanged code must not trigger the no-op guard.
			fn.SDK = !tc.sdk
			reg := registry.New(db)
			if err := reg.Set(fn); err != nil {
				t.Fatal(err)
			}
			dataDir := t.TempDir()
			version := filepath.Join(dataDir, "functions", fn.ID, "versions", fn.CodeHash)
			if err := os.MkdirAll(version, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(version, ".orva-ready"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			drains, refreshes := 0, 0
			h := &FunctionHandler{
				DB: db, Registry: reg, DataDir: dataDir,
				PoolDrain: func(string) { drains++ }, PoolRefresh: func(string) { refreshes++ },
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/functions/"+fn.ID+"/rollback",
				strings.NewReader(fmt.Sprintf(`{"deployment_id":%q}`, dep.ID)))
			req.SetPathValue("fn_id", fn.ID)
			rec := httptest.NewRecorder()
			h.Rollback(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("rollback %d: %s", rec.Code, rec.Body.String())
			}
			got, err := db.GetFunction(fn.ID)
			if err != nil || got.SDK != tc.sdk || got.ActiveDeploymentID != dep.ID || got.NetworkMode != tc.network {
				t.Fatalf("rollback state %+v (%v)", got, err)
			}
			cached, err := reg.Get(fn.ID)
			if err != nil || cached.SDK != tc.sdk {
				t.Fatalf("rollback registry state %+v (%v)", cached, err)
			}
			stored, err := db.GetDeployment(dep.ID)
			if err != nil || stored.Snapshot == nil || stored.Snapshot.SDK != tc.sdk {
				t.Fatalf("normalized snapshot %+v (%v)", stored, err)
			}
			if drains != 1 || refreshes != 0 {
				t.Fatalf("permission rollback did not hard-drain: drains=%d refreshes=%d", drains, refreshes)
			}
		})
	}
}
