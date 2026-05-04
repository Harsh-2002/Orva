package builder

import "testing"

func TestSourceUsesOrvaSDK(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"empty", "", false},
		{"node cjs single quotes", `const { kv } = require('orva')`, true},
		{"node cjs double quotes", `const orva = require("orva");`, true},
		{"node esm single quotes", `import { kv } from 'orva'`, true},
		{"node esm double quotes", `import { kv } from "orva";`, true},
		{"python from-import", "from orva import kv\n", true},
		{"python from-import tab", "from orva\timport kv\n", true},
		{"python plain import", "import orva\nfrom datetime import datetime\n", true},
		{"unrelated comment mentioning orva", "// the orva platform deploys this\nexports.handler = async () => 'hi'", false},
		{"requires orvado not orva", `require('orvado')`, false},
		{"python from-orva-package not bare", "from orva.kv import client", true}, // legitimately imports the SDK
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SourceUsesOrvaSDK(tc.src)
			if got != tc.want {
				t.Fatalf("SourceUsesOrvaSDK(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}
