package oauth

import (
	"reflect"
	"testing"
)

func TestParseScope(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"read", []string{"read"}},
		{"read invoke", []string{"read", "invoke"}},
		{"  read   invoke  ", []string{"read", "invoke"}},
		{"read read invoke", []string{"read", "read", "invoke"}}, // ParseScope preserves order, not dedup
	}
	for _, c := range cases {
		got := ParseScope(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseScope(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormaliseScope(t *testing.T) {
	got := NormaliseScope([]string{"invoke", "read", "invoke", "admin"})
	want := "admin invoke read"
	if got != want {
		t.Errorf("NormaliseScope = %q, want %q", got, want)
	}
}

func TestIntersectScope(t *testing.T) {
	got := IntersectScope(
		[]string{"read", "invoke", "write"},
		[]string{"read", "invoke"},
	)
	want := []string{"read", "invoke"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IntersectScope = %v, want %v", got, want)
	}
}

func TestScopeToPermissions_DropsOIDC(t *testing.T) {
	got := ScopeToPermissions("read invoke openid email profile")
	want := []string{"read", "invoke"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScopeToPermissions = %v, want %v (OIDC scopes must drop)", got, want)
	}
}

func TestIsValidScope(t *testing.T) {
	if !IsValidScope("read invoke openid") {
		t.Error("known scopes should validate")
	}
	if IsValidScope("read floob") {
		t.Error("unknown scope should be rejected")
	}
	if !IsValidScope("") {
		t.Error("empty scope should validate (defaulting handled at /authorize)")
	}
}

func TestHumanScopeBullets_FullScopeListsAllFour(t *testing.T) {
	// Each scope renders as its own bullet so the consent screen can
	// show concrete capabilities alongside the high-level "full
	// access" warning banner.
	got := HumanScopeBullets("read invoke write admin")
	if len(got) != 4 {
		t.Fatalf("expected 4 bullets for full scope, got %d: %v", len(got), got)
	}
}

func TestHumanScopeBullets_SkipsOIDC(t *testing.T) {
	got := HumanScopeBullets("read openid email profile")
	if len(got) != 1 {
		t.Fatalf("OIDC scopes must not appear as bullets, got %v", got)
	}
}
