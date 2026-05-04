package mcp

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

var portableNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func TestSanitiseChannelToolName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "plain snake_case unchanged",
			input: "stripe_charge",
			want:  "stripe_charge",
		},
		{
			name:  "kebab-case → snake_case",
			input: "stripe-charge",
			want:  "stripe_charge",
		},
		{
			name:  "mixed case → lower",
			input: "StripeCharge",
			want:  "stripecharge",
		},
		{
			name:  "non-portable chars → underscore (collapsed)",
			input: "image:resize/webp",
			want:  "image_resize_webp",
		},
		{
			name:  "leading digit → fn_ prefix",
			input: "1stripe-charge",
			want:  "fn_1stripe_charge",
		},
		{
			name:  "leading/trailing junk trimmed",
			input: "--foo--bar--",
			want:  "foo_bar",
		},
		{
			name:  "long name truncated with hash suffix",
			input: strings.Repeat("a", 80),
			// 54 a's + "_" + 8 hex chars = 63 total, deterministic per input
			want: strings.Repeat("a", 54) + "_" + sha1Prefix(strings.Repeat("a", 80), 8),
		},
		{
			name:    "empty after sanitisation",
			input:   "---",
			wantErr: "no valid characters",
		},
		{
			name:    "all whitespace",
			input:   "   ",
			wantErr: "is empty",
		},
		{
			name:    "reserved prefix mcp_",
			input:   "mcp_admin",
			wantErr: "reserved prefix",
		},
		{
			name:    "reserved prefix mcp__ flatten",
			input:   "mcp__server__tool",
			wantErr: "reserved prefix",
		},
		{
			name:    "reserved prefix system_",
			input:   "system_health",
			wantErr: "reserved prefix",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitiseChannelToolName(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result %q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !portableNameRE.MatchString(got) {
				t.Fatalf("result %q violates portable-name regex %q", got, portableNameRE)
			}
			if len(got) > 63 {
				t.Fatalf("result %q is %d chars (max 63)", got, len(got))
			}
		})
	}
}

// sha1Prefix mirrors the production sha1-truncation rule used inside
// sanitiseChannelToolName so the truncation test case can express its
// expected value declaratively.
func sha1Prefix(s string, n int) string {
	sum := sha1.Sum([]byte(s))
	hexed := hex.EncodeToString(sum[:])
	if n > len(hexed) {
		n = len(hexed)
	}
	return hexed[:n]
}
