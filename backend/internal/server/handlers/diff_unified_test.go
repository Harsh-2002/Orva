package handlers

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// applyUnified is a minimal `patch`: it applies the hunks to before and checks
// that each hunk's old-side AND new-side start match where the hunk really
// falls, so a wrong "+start" fails instead of being ignored.
func applyUnified(t *testing.T, before, unified string) string {
	t.Helper()
	old := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	if before == "" {
		old = nil
	}
	var out []string
	pos := 0 // next unread index in old
	lines := strings.Split(unified, "\n")
	for i := 0; i < len(lines); i++ {
		m := hunkHeader.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		var oldStart, oldCount, newStart, newCount int
		oldCount, newCount = 1, 1
		fmt.Sscanf(m[1], "%d", &oldStart)
		if m[2] != "" {
			fmt.Sscanf(m[2], "%d", &oldCount)
		}
		fmt.Sscanf(m[3], "%d", &newStart)
		if m[4] != "" {
			fmt.Sscanf(m[4], "%d", &newCount)
		}
		first := oldStart - 1 // zero-based index of the hunk's first old line
		if oldCount == 0 {
			first = oldStart // "-N,0": N is the line before the insertion
		}
		out = append(out, old[pos:first]...)
		pos = first
		wantNew := len(out) + 1
		if newCount == 0 {
			wantNew = len(out)
		}
		if newStart != wantNew {
			t.Fatalf("hunk %q: new start is %d, but the hunk begins at new line %d", lines[i], newStart, wantNew)
		}
		gotOld, gotNew := 0, 0
		for i++; i < len(lines) && !strings.HasPrefix(lines[i], "@@ "); i++ {
			switch {
			case lines[i] == "" && i == len(lines)-1:
			case strings.HasPrefix(lines[i], "-"):
				if old[pos] != lines[i][1:] {
					t.Fatalf("removed line %q is %q in the original", lines[i][1:], old[pos])
				}
				pos++
				gotOld++
			case strings.HasPrefix(lines[i], "+"):
				out = append(out, lines[i][1:])
				gotNew++
			case strings.HasPrefix(lines[i], " "):
				if old[pos] != lines[i][1:] {
					t.Fatalf("context line %q is %q in the original", lines[i][1:], old[pos])
				}
				out = append(out, old[pos])
				pos++
				gotOld++
				gotNew++
			}
		}
		i--
		if gotOld != oldCount || gotNew != newCount {
			t.Fatalf("hunk %q counts %d/%d, body has %d/%d", lines[0], oldCount, newCount, gotOld, gotNew)
		}
	}
	out = append(out, old[pos:]...)
	return strings.Join(out, "\n") + "\n"
}

func TestUnifiedDiffHunksApplyWithCorrectStarts(t *testing.T) {
	// The case that exposed it: two hunks where the first adds lines, so the
	// second's new-side start must move down. gotextdiff said +10 where +17 is right.
	before := "// v1\nconst G = {\n  en: 1,\n  es: 2,\n  fr: 3,\n};\n\nfunction pick(l) {\n  return G[l] || G.en;\n}\n\nmodule.exports.h = async (e) => {\n  const n = 1;\n  const l = 2;\n  return {\n    status: 200,\n    headers: {},\n    body: n + l,\n  };\n};\n"
	after := strings.NewReplacer("// v1", "// v2", "  fr: 3,\n", "  fr: 3,\n  de: 4,\n  it: 5,\n", "return G[l] || G.en;", "const g = G[l];\n  return g || G.en;", "status: 200,", "status: 200,\n    // cache").Replace(before)
	u := unifiedDiff("handler.js", before, after)
	if !strings.Contains(u, "+17,7 @@") {
		t.Fatalf("second hunk header should end +17,7:\n%s", u)
	}
	if got := applyUnified(t, before, u); got != after {
		t.Fatalf("applying the diff did not reproduce the new file:\n%s", got)
	}
}

func TestUnifiedDiffHunksApplyOnRandomEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for round := 0; round < 300; round++ {
		n := 1 + rng.Intn(60)
		var a []string
		for i := 0; i < n; i++ {
			a = append(a, fmt.Sprintf("line %d", i))
		}
		var b []string
		for _, l := range a {
			switch r := rng.Intn(12); {
			case r == 0: // delete
			case r == 1:
				b = append(b, l, "added after "+l)
			case r == 2:
				b = append(b, "changed "+l)
			case r == 3:
				b = append(b, "added before "+l, l)
			default:
				b = append(b, l)
			}
		}
		before, after := strings.Join(a, "\n")+"\n", strings.Join(b, "\n")+"\n"
		if before == after {
			continue
		}
		u := unifiedDiff("f.js", before, after)
		if got := applyUnified(t, before, u); got != after {
			t.Fatalf("round %d: applying the diff did not reproduce the new file\n--- diff\n%s", round, u)
		}
	}
}
