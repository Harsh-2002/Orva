package mcp

import (
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/database"
)

func TestNetworkErrorHint(t *testing.T) {
	noneFn := &database.Function{NetworkMode: database.NetworkModeNone}
	egressFn := &database.Function{NetworkMode: database.NetworkModeEgress}

	cases := []struct {
		name        string
		fn          *database.Function
		errMsg      string
		stderr      string
		wantHint    bool
		wantSnippet string
	}{
		{
			name:        "none + ENETUNREACH in stderr → hint",
			fn:          noneFn,
			stderr:      "TypeError: fetch failed\nENETUNREACH 172.20.0.3:8443",
			wantHint:    true,
			wantSnippet: "network_mode='egress'",
		},
		{
			name:        "none + ECONNREFUSED in errMsg → hint",
			fn:          noneFn,
			errMsg:      "dial tcp 172.20.0.1:8443: connect: ECONNREFUSED",
			wantHint:    true,
			wantSnippet: "ENETUNREACH",
		},
		{
			name:        "none + OrvaUnavailableError in stderr → hint",
			fn:          noneFn,
			stderr:      "raise OrvaUnavailableError('kv service offline')",
			wantHint:    true,
			wantSnippet: "loopback",
		},
		{
			name:     "egress + ENETUNREACH (real outbound DNS issue) → no hint",
			fn:       egressFn,
			stderr:   "fetch failed: ENETUNREACH api.example.com",
			wantHint: false,
		},
		{
			name:     "none + benign error (assert failure) → no hint",
			fn:       noneFn,
			stderr:   "AssertionError: expected 200 got 500",
			wantHint: false,
		},
		{
			name:     "nil function → no hint",
			fn:       nil,
			stderr:   "ENETUNREACH",
			wantHint: false,
		},
		{
			name:     "no errors at all → no hint",
			fn:       noneFn,
			wantHint: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := networkErrorHint(tc.fn, tc.errMsg, tc.stderr)
			if tc.wantHint {
				if got == "" {
					t.Fatalf("expected hint, got empty string")
				}
				if tc.wantSnippet != "" && !strings.Contains(got, tc.wantSnippet) {
					t.Fatalf("hint missing expected snippet %q; got: %s", tc.wantSnippet, got)
				}
				return
			}
			if got != "" {
				t.Fatalf("expected no hint, got: %s", got)
			}
		})
	}
}
