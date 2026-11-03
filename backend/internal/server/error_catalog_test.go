package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// emitPatterns find the ways the REST handlers put an error code on the wire.
// The last one matters: a code assigned to a variable before the call is
// invisible to a scan of respond.Error arguments, which is how REPLAY_FAILED
// went undocumented.
var emitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`respond\.Error\([^"\n]*"([A-Z][A-Z0-9_]+)"`),
	regexp.MustCompile(`Code:\s*"([A-Z][A-Z0-9_]+)"`),
	regexp.MustCompile(`write(?:Invoke)?AuthError\([^"\n]*"([A-Z][A-Z0-9_]+)"`),
	regexp.MustCompile(`\bcode\s*(?::=|=)\s*"([A-Z][A-Z0-9_]+)"`),
}

var catalogRow = regexp.MustCompile("(?m)^\\| `([A-Z][A-Z0-9_]+)` \\|")

// Codes listed here are knowingly absent from one side. Keep it empty: an entry
// is a decision to leave operators guessing, and needs a reason beside it.
var (
	emittedButUndocumentedOK = map[string]string{}
	documentedButNotEmitted  = map[string]string{}
)

func emittedErrorCodes(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	for _, dir := range []string{".", "handlers"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, re := range emitPatterns {
				for _, m := range re.FindAllStringSubmatch(string(src), -1) {
					if _, seen := found[m[1]]; !seen {
						found[m[1]] = f
					}
				}
			}
		}
	}
	return found
}

func documentedErrorCodes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "ERRORS.md"))
	if err != nil {
		t.Fatalf("read docs/ERRORS.md: %v", err)
	}
	doc := map[string]bool{}
	for _, m := range catalogRow.FindAllStringSubmatch(string(b), -1) {
		doc[m[1]] = true
	}
	return doc
}

// TestErrorCatalogMatchesHandlers keeps docs/ERRORS.md and the REST handlers in
// step in both directions. It covered 33 of 70 codes before this existed.
func TestErrorCatalogMatchesHandlers(t *testing.T) {
	emitted := emittedErrorCodes(t)
	documented := documentedErrorCodes(t)

	// A scan that quietly matches nothing would pass vacuously.
	if len(emitted) < 50 || len(documented) < 50 {
		t.Fatalf("scan looks broken: %d emitted codes, %d documented rows", len(emitted), len(documented))
	}

	var missing, stale []string
	for code, file := range emitted {
		if !documented[code] {
			if _, ok := emittedButUndocumentedOK[code]; !ok {
				missing = append(missing, code+" ("+file+")")
			}
		}
	}
	for code := range documented {
		if _, ok := emitted[code]; !ok {
			if _, allowed := documentedButNotEmitted[code]; !allowed {
				stale = append(stale, code)
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("emitted by a handler but missing from docs/ERRORS.md — add a row for each:\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("documented in docs/ERRORS.md but no handler emits it — remove the row, or if it is emitted another way teach emitPatterns:\n  %s",
			strings.Join(stale, "\n  "))
	}
}
