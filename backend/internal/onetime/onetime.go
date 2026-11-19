// Package onetime is the single list of tools that mint a credential shown only
// once, plus the redaction both live Chat and the boot-time history scrub use.
// It is a leaf package (stdlib only) so database, mcp and ai/agent can all
// share it without an import cycle.
package onetime

import (
	"encoding/json"
	"strings"
)

// Placeholder replaces a one-time credential in anything stored.
const Placeholder = "[redacted: shown once to the operator]"

// Fields maps each credential-minting tool to the dot-separated JSON paths of
// its result that carry the credential. Legacy and managed catalogs share these
// tool names. A tool that mints a credential MUST be listed here.
var Fields = map[string][]string{
	"create_api_key":         {"key"},
	"create_webhook":         {"secret"},
	"create_inbound_webhook": {"secret"},
}

// Redact replaces each present path with Placeholder and reports whether the
// output differs from the input. A result that is not exactly one valid JSON
// value fails closed: the whole result is replaced by the placeholder string.
// Redacting an already redacted result returns it unchanged.
func Redact(raw string, paths []string) (string, bool) {
	if len(paths) == 0 {
		return raw, false
	}
	doc, ok := parse(raw)
	if !ok {
		return quoted(), true
	}
	changed := false
	for _, p := range paths {
		if redactPath(doc, strings.Split(p, ".")) {
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return quoted(), true
	}
	if string(b) == raw {
		return raw, false
	}
	return string(b), true
}

// Values returns the string values found at the paths, so copies of a credential
// that escaped into free text can be located. ok is false when raw is not one
// valid JSON value (nothing can be located).
func Values(raw string, paths []string) (values []string, ok bool) {
	doc, ok := parse(raw)
	if !ok {
		return nil, false
	}
	for _, p := range paths {
		collect(doc, strings.Split(p, "."), &values)
	}
	return values, true
}

func quoted() string {
	b, _ := json.Marshal(Placeholder)
	return string(b)
}

func parse(raw string) (any, bool) {
	// Trailing bytes could hide a credential from the field walk.
	if !json.Valid([]byte(raw)) {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	return doc, true
}

func redactPath(node any, path []string) bool {
	switch n := node.(type) {
	case []any:
		changed := false
		for _, el := range n {
			if redactPath(el, path) {
				changed = true
			}
		}
		return changed
	case map[string]any:
		v, ok := n[path[0]]
		if !ok {
			return false
		}
		if len(path) == 1 {
			if s, isStr := v.(string); isStr && s == Placeholder {
				return false
			}
			n[path[0]] = Placeholder
			return true
		}
		return redactPath(v, path[1:])
	}
	return false
}

func collect(node any, path []string, out *[]string) {
	switch n := node.(type) {
	case []any:
		for _, el := range n {
			collect(el, path, out)
		}
	case map[string]any:
		v, ok := n[path[0]]
		if !ok {
			return
		}
		if len(path) > 1 {
			collect(v, path[1:], out)
			return
		}
		if s, isStr := v.(string); isStr && s != "" && s != Placeholder {
			*out = append(*out, s)
		}
	}
}
