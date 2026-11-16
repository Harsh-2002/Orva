package database

import (
	"context"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestKVListLiteralRanges(t *testing.T) {
	db := newTestDB(t)
	const owner, other = "literal-owner", "literal-other"
	kvTestFn(t, db, owner)
	kvTestFn(t, db, other)
	keys := []string{
		"a", "A", "a%one", "a_one", "abone", "a%two", "a_two", "A%one",
		"config/private", "config/public", "config2/", "é", "é/a", "e\u0301/a",
		"零", "零/a", "\x00", "\x00/a", "a\x00x", "a\x00y", "a\x01",
		"\uD7FF", "\uD7FF/a", "\uE000", "\U0010FFFF", "\U0010FFFF/a",
		"\U0010FFFF\U0010FFFF", "a\U0010FFFF", "a\U0010FFFF/x", "b",
	}
	for _, key := range keys {
		if err := db.KVPut(owner, key, []byte(`1`), nil); err != nil {
			t.Fatalf("put %q: %v", key, err)
		}
	}
	if err := db.KVPut(other, "a%foreign", []byte(`1`), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.KVPut(owner, "a%expired", []byte(`1`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec("UPDATE kv_store SET expires_at = ? WHERE function_id = ? AND key = ?", time.Now().UTC().Add(-time.Hour), owner, "a%expired"); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "a", "A", "a%", "a_", "config/", "é", "e\u0301", "零", "\x00", "a\x00", "\uD7FF", "\U0010FFFF", "a\U0010FFFF", "missing"} {
		t.Run(prefix, func(t *testing.T) {
			want := []string{}
			for _, key := range keys {
				if strings.HasPrefix(key, prefix) {
					want = append(want, key)
				}
			}
			sort.Strings(want)
			got := []string{}
			cursor := ""
			for pages := 0; ; pages++ {
				if pages > len(keys) {
					t.Fatal("cursor did not advance")
				}
				page, err := db.KVListLiteralWithCursorContext(context.Background(), owner, prefix, cursor, 1)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range page.Entries {
					got = append(got, entry.Key)
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("literal prefix %q: got %q, want %q", prefix, got, want)
			}
		})
	}
	page, err := db.KVListLiteralWithCursorContext(context.Background(), owner, "a%", "z", 100)
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("cursor cannot widen range: page=%+v err=%v", page, err)
	}
	if _, err := db.KVListLiteralWithCursorContext(context.Background(), owner, string([]byte{255}), "", 1); err == nil {
		t.Fatal("invalid UTF-8 prefix accepted")
	}
	if _, err := db.KVListLiteralWithCursorContext(context.Background(), owner, "", string([]byte{255}), 1); err == nil {
		t.Fatal("invalid UTF-8 cursor accepted")
	}
}

func TestKVLiteralFoundationDoesNotChangeLegacyList(t *testing.T) {
	db := newTestDB(t)
	const owner = "literal-legacy"
	kvTestFn(t, db, owner)
	for _, key := range []string{"a_1", "AB1", "ac1"} {
		if err := db.KVPut(owner, key, []byte(`1`), nil); err != nil {
			t.Fatal(err)
		}
	}
	page, err := db.KVListWithCursor(owner, "a_", "", 100)
	if err != nil || len(page.Entries) != 3 {
		t.Fatalf("legacy LIKE behavior changed before IAM cut-over: page=%+v err=%v", page, err)
	}
}

func TestKVPrefixUpperBoundMatchesLiteralPrefix(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 29))
	alphabet := []rune{0, '%', '_', 'a', 'A', 'z', 'é', '零', 0xD7FF, 0xE000, utf8.MaxRune}
	for range 5000 {
		var text strings.Builder
		for range rng.IntN(8) {
			text.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		prefix := text.String()
		upper := kvPrefixUpperBound(prefix)
		if !utf8.ValidString(upper) {
			t.Fatalf("invalid upper bound %q for %q", upper, prefix)
		}
		for _, suffix := range []string{"", "a", "\x00", "\U0010FFFF", "/x"} {
			key := prefix + suffix
			if key < prefix || (upper != "" && key >= upper) {
				t.Fatalf("matching key %q outside [%q,%q)", key, prefix, upper)
			}
		}
		for range 10 {
			candidate := string(alphabet[rng.IntN(len(alphabet))]) + text.String() + string(alphabet[rng.IntN(len(alphabet))])
			inRange := candidate >= prefix && (upper == "" || candidate < upper)
			if inRange != strings.HasPrefix(candidate, prefix) {
				t.Fatalf("range mismatch: prefix=%q upper=%q candidate=%q", prefix, upper, candidate)
			}
		}
	}
}
