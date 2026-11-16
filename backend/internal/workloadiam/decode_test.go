package workloadiam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestStrictDecode(t *testing.T) {
	valid := `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"prefix":"cfg/"}},"context":"bootstrap"}],"inbound":[]}`
	for _, data := range []string{valid, `{"schema_version":1,"allow":[],"inbound":[]}`, strings.Replace(valid, `cfg/`, `\ud83d\ude00`, 1), strings.Replace(valid, `cfg/`, `\\ud800`, 1)} {
		if _, err := Decode([]byte(data)); err != nil {
			t.Fatalf("valid policy rejected: %v", err)
		}
	}
	cases := map[string]string{
		"empty": "", "null": "null", "array": "[]", "trailing": valid + ` {}`, "trailing-garbage": valid + `x`,
		"unknown-top":         strings.Replace(valid, `"inbound":[]`, `"inbound":[],"revision":1`, 1),
		"host-owner":          strings.Replace(valid, `"inbound":[]`, `"inbound":[],"owner_id":"`+owner+`"`, 1),
		"host-artifact":       strings.Replace(valid, `"inbound":[]`, `"inbound":[],"approved_artifact":"x"`, 1),
		"wrong-case":          strings.Replace(valid, `schema_version`, `Schema_Version`, 1),
		"missing-version":     strings.Replace(valid, `"schema_version":1,`, "", 1),
		"missing-allow":       `{"schema_version":1,"inbound":[]}`,
		"missing-inbound":     `{"schema_version":1,"allow":[]}`,
		"duplicate-top":       strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"duplicate-escaped":   strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_versio\u006e":1`, 1),
		"duplicate-statement": strings.Replace(valid, `"context":"bootstrap"`, `"context":"bootstrap","context":"execution"`, 1),
		"duplicate-selector":  strings.Replace(valid, `"prefix":"cfg/"`, `"prefix":"cfg/","prefix":""`, 1),
		"unknown-nested":      strings.Replace(valid, `"prefix":"cfg/"`, `"prefix":"cfg/","owner":"x"`, 1),
		"null-array":          strings.Replace(valid, `"inbound":[]`, `"inbound":null`, 1),
		"null-statement":      `{"schema_version":1,"allow":[null],"inbound":[]}`,
		"null-action":         strings.Replace(valid, `"kv.get"`, `null`, 1),
		"null-context":        strings.Replace(valid, `"bootstrap"`, `null`, 1),
		"null-resource":       strings.Replace(valid, `{"own_kv":{"prefix":"cfg/"}}`, `null`, 1),
		"null-optional":       strings.Replace(valid, `"prefix":"cfg/"`, `"prefix":"cfg/","key":null`, 1),
		"future":              strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		"fraction-version":    strings.Replace(valid, `"schema_version":1`, `"schema_version":1.0`, 1),
		"string-version":      strings.Replace(valid, `"schema_version":1`, `"schema_version":"1"`, 1),
		"wrong-action-type":   strings.Replace(valid, `["kv.get"]`, `"kv.get"`, 1),
		"wrong-resource-type": strings.Replace(valid, `{"own_kv":{"prefix":"cfg/"}}`, `[]`, 1),
		"bad-surrogate":       strings.Replace(valid, `cfg/`, `\ud800`, 1),
		"low-surrogate":       strings.Replace(valid, `cfg/`, `\udc00`, 1),
		"surrogate-pair":      strings.Replace(valid, `cfg/`, `\ud800\u0041`, 1),
		"bad-hex":             strings.Replace(valid, `cfg/`, `\uZZZZ`, 1),
		"invalid-utf8":        strings.Replace(valid, `cfg/`, string([]byte{0xff}), 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := Decode([]byte(data))
			if err == nil {
				t.Fatalf("accepted %s", data)
			}
			if p.SchemaVersion != 0 || p.Allow != nil || p.Inbound != nil {
				t.Fatal("partial invalid policy returned")
			}
		})
	}
}

func TestValidationMatrix(t *testing.T) {
	cases := []struct {
		name string
		s    Statement
		want bool
	}{
		{"zero", Statement{}, false}, {"unknown-action", statement(Execution, key("k"), "kv.*"), false},
		{"case-action", statement(Execution, key("k"), "KV.get"), false}, {"empty-actions", statement(Execution, key("k")), false},
		{"duplicate-action", statement(Execution, key("k"), KVGet, KVGet), false},
		{"get-on-function", statement(Execution, function(other), KVGet), false}, {"invoke-on-kv", statement(Execution, key("k"), FunctionsInvoke), false},
		{"cron-on-execution", statement(Execution, activeExecution(), CronsUpsert), false},
		{"span-on-cron", statement(Execution, cron("tick", owner), SpansEmit), false},
		{"list-on-exact", statement(Execution, key("k"), KVList), false},
		{"list-prefix", statement(Execution, prefix(""), KVList), true},
		{"zero-resource", statement(Execution, Resource{}, KVGet), false},
		{"empty-selector", statement(Execution, Resource{OwnKV: &KVResource{}}, KVGet), false},
		{"two-selectors", statement(Execution, Resource{OwnKV: &KVResource{Key: str("k"), Prefix: str("")}}, KVGet), false},
		{"two-resources", statement(Execution, Resource{OwnKV: &KVResource{Key: str("k")}, OwnExecution: &ExecutionResource{}}, KVGet), false},
		{"empty-key", statement(Execution, key(""), KVGet), false},
		{"invalid-utf8-key", statement(Execution, key(string([]byte{0xff})), KVGet), false},
		{"256-runes", statement(Execution, key(strings.Repeat("界", 256)), KVGet), true},
		{"257-runes", statement(Execution, prefix(strings.Repeat("界", 257)), KVGet), false},
		{"empty-context", statement("", key("k"), KVGet), false}, {"unknown-context", statement("background", key("k"), KVGet), false},
		{"bootstrap-get", statement(Bootstrap, key("k"), KVGet), true}, {"bootstrap-list", statement(Bootstrap, prefix(""), KVList), true},
		{"bootstrap-put", statement(Bootstrap, key("k"), KVPut), false}, {"bootstrap-invoke", statement(Bootstrap, function(other), FunctionsInvoke), false},
		{"bootstrap-span", statement(Bootstrap, activeExecution(), SpansEmit), false}, {"bootstrap-mixed", statement(Bootstrap, key("k"), KVGet, KVDelete), false},
		{"literal-cron", statement(Execution, cron("*%_", owner), CronsUpsert), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(withStatements(tc.s))
			if (err == nil) != tc.want {
				t.Fatalf("valid=%v: %v", tc.want, err)
			}
		})
	}
	for _, badID := range []string{"", "name", "*", strings.ToUpper(owner), strings.ReplaceAll(owner, "-", ""), "{" + owner + "}", "urn:uuid:" + owner, "00000000-0000-0000-0000-000000000000", "019a8dd0-0000-0000-8000-000000000001", "019a8dd0-0000-7000-0000-000000000001"} {
		if Validate(withStatements(statement(Execution, function(badID), FunctionsInvoke))) == nil {
			t.Fatalf("invalid UUID accepted: %s", badID)
		}
	}
	for _, acceptance := range []Acceptance{
		{}, {CallerID: "*", Triggers: []Trigger{F2F}}, {CallerID: owner, Triggers: []Trigger{}},
		{CallerID: owner, Triggers: []Trigger{"invoke"}}, {CallerID: owner, Triggers: []Trigger{F2F, F2F}},
	} {
		p := emptyPolicy()
		p.Inbound = []Acceptance{acceptance}
		if Validate(p) == nil {
			t.Fatalf("invalid acceptance %+v", acceptance)
		}
	}
	for _, m := range []Metadata{{}, {OwnerID: owner, Revision: 0, ApprovedArtifact: "x"}, {OwnerID: owner, Revision: 1}, {OwnerID: owner, Revision: 1, ApprovedArtifact: strings.Repeat("x", 257)}} {
		if _, err := Compile(m, emptyPolicy()); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	if Validate(Policy{}) == nil || Validate(Policy{SchemaVersion: 1}) == nil {
		t.Fatal("zero policy accepted")
	}
}

func TestBounds(t *testing.T) {
	t.Run("statements", func(t *testing.T) {
		p := emptyPolicy()
		for range MaxStatements {
			p.Allow = append(p.Allow, statement(Execution, key("k"), KVGet))
		}
		if err := Validate(p); err != nil {
			t.Fatal(err)
		}
		p.Allow = append(p.Allow, p.Allow[0])
		if Validate(p) == nil {
			t.Fatal("one-over statements accepted")
		}
	})
	t.Run("combined-targets", func(t *testing.T) {
		p := withStatements(statement(Execution, function(other), FunctionsInvoke))
		for i := 1; i < MaxTargets; i++ {
			p.Inbound = append(p.Inbound, Acceptance{CallerID: fmt.Sprintf("019a8dd0-0000-7000-8000-%012x", i), Triggers: []Trigger{F2F}})
		}
		if _, err := Compile(metadata(owner), p); err != nil {
			t.Fatal(err)
		}
		p.Inbound = append(p.Inbound, Acceptance{CallerID: owner, Triggers: []Trigger{F2F}})
		if Validate(p) == nil {
			t.Fatal("combined one-over accepted")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		base := []byte(`{"schema_version":1,"allow":[],"inbound":[]}`)
		exact := append(append([]byte{}, base...), bytes.Repeat([]byte(" "), MaxPolicyBytes-len(base))...)
		if _, err := Decode(exact); err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(append(exact, ' ')); err == nil {
			t.Fatal("one-over bytes accepted")
		}
		p := emptyPolicy()
		for range MaxStatements {
			p.Allow = append(p.Allow, statement(Execution, key(strings.Repeat("界", 256)), KVGet))
		}
		if Validate(p) == nil {
			t.Fatal("oversized nested strings accepted")
		}
	})
	t.Run("nested-counts", func(t *testing.T) {
		p := emptyPolicy()
		p.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{F2F, Job, Cron, F2F}}}
		if Validate(p) == nil {
			t.Fatal("trigger count accepted")
		}
		p = withStatements(statement(Execution, key("k"), make([]Action, 11)...))
		if Validate(p) == nil {
			t.Fatal("action count accepted")
		}
		p = emptyPolicy()
		for range MaxTargets + 1 {
			p.Inbound = append(p.Inbound, Acceptance{CallerID: owner, Triggers: []Trigger{F2F}})
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(data); err == nil {
			t.Fatal("array over limit decoded")
		}
	})
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"schema_version":1,"allow":[],"inbound":[]}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Decode(data)
		if err != nil {
			return
		}
		if err := Validate(p); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(encoded); err != nil {
			t.Fatalf("accepted policy cannot round-trip: %v", err)
		}
	})
}

func TestActionResourceMatrix(t *testing.T) {
	resources := []Resource{key("k"), prefix(""), function(other), cron("tick", owner), activeExecution()}
	actions := []Action{KVGet, KVList, KVPut, KVDelete, KVIncr, KVCAS, FunctionsInvoke, JobsEnqueue, CronsUpsert, SpansEmit, "*", "unknown"}
	for i, r := range resources {
		for _, a := range actions {
			for _, phase := range []Phase{Execution, Bootstrap} {
				t.Run(fmt.Sprintf("%d/%s/%s", i, a, phase), func(t *testing.T) {
					want := false
					switch a {
					case KVGet, KVPut, KVDelete, KVIncr, KVCAS:
						want = i == 0 || i == 1
					case KVList:
						want = i == 1
					case FunctionsInvoke, JobsEnqueue:
						want = i == 2
					case CronsUpsert:
						want = i == 3
					case SpansEmit:
						want = i == 4
					}
					if phase == Bootstrap && a != KVGet && a != KVList {
						want = false
					}
					if got := Validate(withStatements(statement(phase, r, a))) == nil; got != want {
						t.Fatalf("valid=%v, want %v", got, want)
					}
				})
			}
		}
	}
}
