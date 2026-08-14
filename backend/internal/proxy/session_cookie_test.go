package proxy

import "testing"

// session_token carries full control-plane authority and is accepted as invoke
// auth, and the dashboard's own invoke client sends it with credentials against
// /fn/. It must never reach sandboxed user code — but a function's own cookies
// must survive, so this is a surgical removal rather than dropping the header.
func TestStripSessionCookie(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"only the session cookie", "session_token=abc123", ""},
		{"session cookie among others", "theme=dark; session_token=abc123; lang=en", "theme=dark; lang=en"},
		{"leading session cookie", "session_token=abc; theme=dark", "theme=dark"},
		{"trailing session cookie", "theme=dark; session_token=abc", "theme=dark"},
		{"no session cookie", "theme=dark; lang=en", "theme=dark; lang=en"},
		{"case-insensitive name", "SESSION_TOKEN=abc; theme=dark", "theme=dark"},
		{"untidy spacing", "  session_token=abc ;   theme=dark  ", "theme=dark"},
		{"empty header", "", ""},
		// A cookie whose name merely contains the session name must survive.
		{"lookalike name", "my_session_token=abc; theme=dark", "my_session_token=abc; theme=dark"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripSessionCookie(tc.in); got != tc.want {
				t.Errorf("stripSessionCookie(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
