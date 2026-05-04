package builder

import "strings"

// sdkImportPatterns are the substrings we treat as "this handler imports
// the orva SDK." Conservative on purpose — we look for the canonical
// shapes the docs and editor templates produce. False positives here
// only mean we attach an unnecessary warning, which is cheap; false
// negatives mean we let an agent ship a broken function silently, which
// is exactly the foot-gun this scan exists to prevent.
var sdkImportPatterns = []string{
	"require('orva')",   // Node CommonJS, single-quoted (default editor template)
	"require(\"orva\")", // Node CommonJS, double-quoted
	"from 'orva'",       // Node ESM
	"from \"orva\"",     // Node ESM, double-quoted
	"from orva ",        // Python `from orva import ...`
	"from orva\timport", // Python with tab between identifier and `import`
	"from orva.",        // Python submodule, e.g. `from orva.kv import client`
	"import orva",       // Python `import orva`
}

// SourceUsesOrvaSDK reports whether the supplied handler source appears
// to import the in-sandbox `orva` module (kv / invoke / jobs). The match
// is a plain substring sweep — we don't try to parse the source. The
// caller pairs this with the function's network_mode to decide whether
// to surface a deploy warning.
func SourceUsesOrvaSDK(source string) bool {
	if source == "" {
		return false
	}
	for _, pat := range sdkImportPatterns {
		if strings.Contains(source, pat) {
			return true
		}
	}
	return false
}

// SDKNoneWarning is the canonical warning string emitted when a deploy
// lands code that imports the orva SDK while the function's
// network_mode is "none". Surfaced on both REST and MCP deploy paths.
const SDKNoneWarning = "Function imports the orva SDK (kv / invoke / jobs) " +
	"but network_mode is 'none' — every SDK call will fail at runtime " +
	"with ENETUNREACH because the SDK reaches orvad over the bridge " +
	"network. Update the function with network_mode='egress' to enable " +
	"the SDK; the next invocation will be a cold start as the warm pool " +
	"drains."
