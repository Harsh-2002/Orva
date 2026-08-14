package database

import "testing"

// A custom route may not shadow a prefix Orva serves itself. "/fn/" is the
// load-bearing entry here: before this guard the reserved list stopped at
// "/_orva/", so a route under /fn/ was accepted into the table.
func TestValidateRoutePathRejectsReservedPrefixes(t *testing.T) {
	reserved := []string{
		"/api/v1/functions",
		"/auth/login",
		"/web/index.html",
		"/_orva/internal",
		"/fn/019df200-7b00-7e00-9c00-aab1cd2e3f40",
		"/fn/anything/at/all",
		"/mcp/tools",
		"/webhook/inbound",
	}
	for _, p := range reserved {
		if err := ValidateRoutePath(p); err == nil {
			t.Errorf("ValidateRoutePath(%q) = nil, want a reserved-prefix error", p)
		}
	}
}

func TestValidateRoutePathAcceptsOrdinaryRoutes(t *testing.T) {
	ok := []string{
		"/webhooks/stripe",
		"/shortener/*",
		"/", // exact root: served by the custom-route catch-all
		"/*",
		"/deeply/nested/path",
	}
	for _, p := range ok {
		if err := ValidateRoutePath(p); err != nil {
			t.Errorf("ValidateRoutePath(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidateRoutePathRejectsMalformed(t *testing.T) {
	bad := map[string]string{
		"no-leading-slash": "must start with /",
		"/mid*dle/path":    "wildcard not final",
		"/prefix/*/more":   "wildcard not final",
	}
	for p, why := range bad {
		if err := ValidateRoutePath(p); err == nil {
			t.Errorf("ValidateRoutePath(%q) = nil, want an error (%s)", p, why)
		}
	}
}
