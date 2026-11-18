//go:build linux

package builder

import (
	"strings"
	"testing"
)

func TestLifecycleStrictInput(t *testing.T) {
	const owner = "019aaa00-0000-7000-8000-000000000001"
	const generation = "019aaa00-0000-7000-8000-000000000002"
	const valid = `{"generation":"` + generation + `","policy_revision":"9007199254740993","authority_epoch":"9223372036854775807","confirm":true}`
	tool := `{"function_id":"` + owner + `",` + valid[1:]
	for _, tc := range []struct {
		raw, path string
		tool      bool
	}{{valid, owner, false}, {tool, "", true}} {
		got, err := DecodeLifecycleInput([]byte(tc.raw), tc.path, tc.tool)
		if err != nil || got.OwnerID != owner || got.ExpectedGeneration != generation ||
			got.ExpectedPolicyRevision != 9007199254740993 || got.ExpectedAuthorityEpoch != 9223372036854775807 {
			t.Fatalf("decoded %+v, %v", got, err)
		}
	}
	for _, raw := range []string{`null`, `{}`, valid + `{}`, tool, strings.Repeat(" ", MaxLifecycleRequestBytes) + valid,
		strings.Replace(valid, `,"confirm":true`, "", 1),
		strings.Replace(valid, `true`, `false`, 1), strings.Replace(valid, `true`, `null`, 1),
		strings.Replace(valid, `true`, `true,"\u0063onfirm":true`, 1),
		strings.Replace(valid, `true`, `true,"admin":true`, 1),
		strings.Replace(valid, `"9007199254740993"`, `9007199254740993`, 1),
		strings.Replace(valid, `"9007199254740993"`, `"09007199254740993"`, 1),
		strings.Replace(valid, `"9007199254740993"`, `"+1"`, 1),
		strings.Replace(valid, `"9007199254740993"`, `"0"`, 1),
		strings.Replace(valid, "9223372036854775807", "9223372036854775808", 1),
		strings.Replace(valid, generation, `\ud800`, 1),
		strings.Replace(valid, generation, "\xff", 1),
		strings.Replace(valid, generation, "latest", 1),
	} {
		if _, err := DecodeLifecycleInput([]byte(raw), owner, false); err == nil {
			t.Errorf("REST accepted %q", raw)
		}
	}
	for _, path := range []string{"", "name", "019AAA00-0000-7000-8000-000000000001"} {
		if _, err := DecodeLifecycleInput([]byte(valid), path, false); err == nil {
			t.Errorf("REST accepted path %q", path)
		}
	}
	if _, err := DecodeLifecycleInput([]byte(valid), "", true); err == nil {
		t.Error("tool accepted absent owner")
	}
}
