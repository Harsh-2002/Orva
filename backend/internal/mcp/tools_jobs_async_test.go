package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAsyncJobCatalogIsExplicitAndAdminOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, permission := range []string{permRead, permWrite, permAdmin} {
			reg := newRegistry()
			registerJobTools(&regCtx{deps: Deps{AsyncEnabled: enabled}, perms: permSet{permission: true}, reg: reg})
			for _, name := range []string{"enqueue_job", "retry_job", "delete_job", "resume_job"} {
				want := permission == permWrite && !enabled && name != "resume_job" || permission == permAdmin && enabled
				if (reg.Get(name) != nil) != want {
					t.Fatalf("enabled=%v permission=%s tool=%s", enabled, permission, name)
				}
			}
			if enabled && permission == permAdmin {
				if _, err := reg.Dispatch(context.Background(), "resume_job", json.RawMessage(`{"id":"019a8000-0000-7000-8000-000000000001"}`)); err == nil {
					t.Fatal("explicit nil service fell back to legacy")
				}
			}
		}
	}
	raw, err := json.Marshal(JobView{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "authority_") || strings.Contains(string(raw), "origin_kind") {
		t.Fatal("normal projection exposes staged authority")
	}
}
