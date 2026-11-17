package mcp

import (
	"encoding/json"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestCronControlProjectionPreservesDecimalStrings(t *testing.T) {
	row := &database.AsyncCron{ConfigRevision: 9007199254740993, AsyncProvenance: database.AsyncProvenance{
		CreationCallerRevision: 9007199254740993, CreationTargetRevision: 9007199254740993, AuthorityGeneration: 9007199254740993,
		AdmissionCallerRevision: 9007199254740993, AdmissionTargetRevision: 9007199254740993,
	}}
	raw, err := json.Marshal(iamCronView(row))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"config_revision", "creation_caller_revision", "creation_target_revision", "authority_generation", "admission_caller_revision", "admission_target_revision"} {
		if out[key] != "9007199254740993" {
			t.Fatalf("%s rounded or not string: %v", key, out[key])
		}
	}
}

func TestCronControlCatalogExplicitAdminOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, permission := range []string{permRead, permWrite, permAdmin} {
			reg := newRegistry()
			registerCronTools(&regCtx{deps: Deps{CronEnabled: enabled}, perms: permSet{permission: true}, reg: reg})
			for _, name := range []string{"create_cron_schedule", "update_cron_schedule", "delete_cron_schedule", "resume_cron_schedule"} {
				want := enabled && permission == permAdmin || !enabled && permission == permWrite && name != "resume_cron_schedule"
				if (reg.Get(name) != nil) != want {
					t.Fatal(enabled, permission, name)
				}
			}
			if enabled && permission == permAdmin && !reg.Get("delete_cron_schedule").Destructive {
				t.Fatal("AI destructive hint missing")
			}
		}
	}
}
