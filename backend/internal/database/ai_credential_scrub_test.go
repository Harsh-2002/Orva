package database

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/onetime"
)

const scrubPlaceholder = onetime.Placeholder

type scrubSeed struct {
	conv, callID, tool, result string
}

// seedChatCall stores a minting call the way a build before the redaction did:
// the audit row, the tool-role message holding the identical result, and an
// assistant reply that repeats the credential.
func seedChatCall(t *testing.T, db *Database, s scrubSeed, echo string) {
	t.Helper()
	if _, err := db.write.Exec(`INSERT OR IGNORE INTO ai_conversations (id) VALUES (?)`, s.conv); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, result, status)
		VALUES (?, ?, ?, ?, ?, 'succeeded')`, "tc-"+s.conv+"-"+s.callID+s.tool, s.conv, s.callID, s.tool, s.result); err != nil {
		t.Fatal(err)
	}
	parts := fmt.Sprintf(`[{"type":"tool_result","call_id":%q,"status":"succeeded"}]`, s.callID)
	if s.callID == "" {
		parts = `[{"type":"tool_result","status":"succeeded"}]`
	}
	if _, err := db.write.Exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, ?, 'tool', ?, ?, ?)`,
		"m-tool-"+s.conv+s.callID+s.tool, s.conv, s.result, parts, seqOf(t, db, s.conv)); err != nil {
		t.Fatal(err)
	}
	if echo != "" {
		if _, err := db.write.Exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, ?, 'assistant', ?, ?, ?)`,
			"m-asst-"+s.conv+s.callID+s.tool, s.conv, "Here is your key: "+echo,
			fmt.Sprintf(`[{"type":"text","text":%q}]`, "Here is your key: "+echo), seqOf(t, db, s.conv)); err != nil {
			t.Fatal(err)
		}
	}
}

func seqOf(t *testing.T, db *Database, conv string) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM ai_messages WHERE conversation_id = ?`, conv).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func resetScrub(t *testing.T, db *Database) {
	t.Helper()
	for _, k := range []string{chatScrubMarkerKey, chatScrubCursorKey} {
		if _, err := db.write.Exec(`DELETE FROM system_config WHERE key = ?`, k); err != nil {
			t.Fatal(err)
		}
	}
}

// chatText is every stored text column the scrub may touch, for leak checks.
func chatText(t *testing.T, db *Database) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{`SELECT id||args||COALESCE(result,'') FROM ai_tool_calls`, `SELECT id||role||content||parts FROM ai_messages`} {
		rows, err := db.read.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			b.WriteString(s + "\n")
		}
		rows.Close()
	}
	return b.String()
}

func column(t *testing.T, db *Database, q string, args ...any) string {
	t.Helper()
	var s string
	if err := db.read.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func secretOf(n int) string { return fmt.Sprintf("secret-%02d-0123456789abcdef0123456789abcdef", n) }

func TestScrubChatCredentialsEveryTool(t *testing.T) {
	db := newTestDB(t)
	cases := []scrubSeed{
		{"c1", "call_a", "create_api_key", `{"id":"k1","key":"` + secretOf(1) + `","name":"ci"}`},
		{"c2", "call_b", "create_webhook", `{"subscription":{"id":"s1","secret_preview":"secret-0…"},"secret":"` + secretOf(2) + `"}`},
		{"c3", "call_c", "create_inbound_webhook", `{"inbound_webhook":{"id":"w1"},"secret":"` + secretOf(3) + `"}`},
		{"c4", "", "create_inbound_webhook", `{"secret":"` + secretOf(4) + `"}`}, // no provider call id
	}
	for i, c := range cases {
		seedChatCall(t, db, c, secretOf(i+1))
	}
	resetScrub(t, db)
	db.scrubChatCredentials()

	text := chatText(t, db)
	for i := range cases {
		if strings.Contains(text, secretOf(i+1)) {
			t.Fatalf("credential %d survived the scrub", i+1)
		}
	}
	// Non-secret structure is preserved; the credential field alone is replaced.
	got := column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'c1'`)
	if got != `{"id":"k1","key":"`+scrubPlaceholder+`","name":"ci"}` {
		t.Fatalf("audit row = %s", got)
	}
	if tool := column(t, db, `SELECT content FROM ai_messages WHERE id = 'm-tool-c1call_acreate_api_key'`); tool != got {
		t.Fatalf("tool message = %s, want the audit row's redacted result", tool)
	}
	if a := column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = 'c1' AND role = 'assistant'`); a != "Here is your key: "+scrubPlaceholder {
		t.Fatalf("assistant content = %q", a)
	}
	if p := column(t, db, `SELECT parts FROM ai_messages WHERE conversation_id = 'c1' AND role = 'assistant'`); !strings.Contains(p, scrubPlaceholder) {
		t.Fatalf("assistant parts = %s", p)
	}
	if column(t, db, `SELECT content FROM ai_messages WHERE id = 'm-tool-c4create_inbound_webhook'`) != `{"secret":"`+scrubPlaceholder+`"}` {
		t.Fatal("tool message without a call id was not scrubbed")
	}
	if db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		t.Fatal("marker not set after a complete walk")
	}
}

func TestScrubChatCredentialsShapesAndUnparseable(t *testing.T) {
	db := newTestDB(t)
	for _, c := range []scrubSeed{
		{"a", "1", "create_api_key", `[{"key":"` + secretOf(1) + `"},{"key":"` + secretOf(2) + `","extra":{"keep":1}}]`},
		{"b", "2", "create_webhook", `not json ` + secretOf(3)},
		{"c", "3", "create_api_key", `{"key":"` + secretOf(4) + `"} {"trailing":"` + secretOf(5) + `"}`},
		{"d", "4", "create_inbound_webhook", `{"error":"denied"}`}, // a failure: nothing to redact
	} {
		seedChatCall(t, db, c, "")
	}
	resetScrub(t, db)
	db.scrubChatCredentials()

	if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'a'`); got !=
		`[{"key":"`+scrubPlaceholder+`"},{"extra":{"keep":1},"key":"`+scrubPlaceholder+`"}]` {
		t.Fatalf("array shape = %s", got)
	}
	for _, conv := range []string{"b", "c"} {
		want := `"` + scrubPlaceholder + `"`
		if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = ?`, conv); got != want {
			t.Fatalf("unparseable %s result = %s, want wholesale replacement", conv, got)
		}
		if got := column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = ? AND role = 'tool'`, conv); got != want {
			t.Fatalf("unparseable %s tool message = %s", conv, got)
		}
	}
	if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'd'`); got != `{"error":"denied"}` {
		t.Fatalf("failure result changed: %s", got)
	}
	for i := 1; i <= 5; i++ {
		if strings.Contains(chatText(t, db), secretOf(i)) {
			t.Fatalf("secret %d survived", i)
		}
	}
}

func TestScrubChatCredentialsLeavesOtherRowsAlone(t *testing.T) {
	db := newTestDB(t)
	secret := secretOf(7)
	seedChatCall(t, db, scrubSeed{"mine", "x1", "create_api_key", `{"key":"` + secret + `"}`}, secret)
	// Other tools with fields named like a credential, in the same conversation,
	// reusing the same provider call id; and a second conversation and a user
	// message that happen to contain the value.
	other := `{"key":"not-a-credential-` + strings.Repeat("z", 20) + `","secret":"s"}`
	seedChatCall(t, db, scrubSeed{"mine", "x1", "list_secrets", other}, "")
	seedChatCall(t, db, scrubSeed{"elsewhere", "x1", "invoke_function", `{"echo":"` + secret + `"}`}, secret)
	if _, err := db.write.Exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('u1','mine','user',?, '[]', 99)`, "my key is "+secret); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{
		"other tool row":         column(t, db, `SELECT result FROM ai_tool_calls WHERE tool_name = 'list_secrets'`),
		"other tool message":     column(t, db, `SELECT content FROM ai_messages WHERE id = 'm-tool-minex1list_secrets'`),
		"other conversation":     column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'elsewhere'`),
		"other conversation msg": column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = 'elsewhere' AND role = 'assistant'`),
		"user message":           column(t, db, `SELECT content FROM ai_messages WHERE id = 'u1'`),
	}
	resetScrub(t, db)
	db.scrubChatCredentials()
	after := map[string]string{
		"other tool row":         column(t, db, `SELECT result FROM ai_tool_calls WHERE tool_name = 'list_secrets'`),
		"other tool message":     column(t, db, `SELECT content FROM ai_messages WHERE id = 'm-tool-minex1list_secrets'`),
		"other conversation":     column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'elsewhere'`),
		"other conversation msg": column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = 'elsewhere' AND role = 'assistant'`),
		"user message":           column(t, db, `SELECT content FROM ai_messages WHERE id = 'u1'`),
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s was modified: %q -> %q", k, v, after[k])
		}
	}
	if strings.Contains(column(t, db, `SELECT result FROM ai_tool_calls WHERE tool_name = 'create_api_key'`), secret) {
		t.Fatal("minting row not scrubbed")
	}
}

func TestScrubChatCredentialsResumesAndIsAtomicPerBatch(t *testing.T) {
	db := newTestDB(t)
	oldRows, oldMax := chatScrubBatchRows, chatScrubMaxBatches
	t.Cleanup(func() { chatScrubBatchRows, chatScrubMaxBatches = oldRows, oldMax })
	chatScrubBatchRows, chatScrubMaxBatches = 2, 1
	for i := 1; i <= 5; i++ {
		seedChatCall(t, db, scrubSeed{fmt.Sprintf("r%d", i), "c", "create_api_key", `{"key":"` + secretOf(i) + `"}`}, secretOf(i))
	}
	resetScrub(t, db)

	leaked := func() int {
		n := 0
		for i := 1; i <= 5; i++ {
			if strings.Contains(chatText(t, db), secretOf(i)) {
				n++
			}
		}
		return n
	}
	// One boot is capped at one batch of two rows: interrupted mid-way.
	db.scrubChatCredentials()
	if leaked() != 3 || db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
		t.Fatalf("after first capped boot: leaked=%d marker=%q", leaked(), db.GetSystemConfigString(chatScrubMarkerKey, ""))
	}
	if db.GetSystemConfigString(chatScrubCursorKey, "") == "" {
		t.Fatal("cursor not persisted for the next boot")
	}
	// A batch that fails rolls back whole: nothing from it is half-scrubbed.
	if _, err := db.write.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON ai_messages WHEN NEW.id = 'm-asst-r4ccreate_api_key'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	db.scrubChatCredentials() // batch {r3, r4}: r3 would succeed, r4 aborts
	if leaked() != 3 {
		t.Fatalf("failed batch was partly committed: leaked=%d", leaked())
	}
	if _, err := db.write.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	// Reboots finish the walk.
	for range 4 {
		db.scrubChatCredentials()
	}
	if leaked() != 0 || db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		t.Fatalf("resume did not complete: leaked=%d marker=%q", leaked(), db.GetSystemConfigString(chatScrubMarkerKey, ""))
	}
}

func TestScrubChatCredentialsIdempotentAndMarkerGated(t *testing.T) {
	db := newTestDB(t)
	seedChatCall(t, db, scrubSeed{"i", "c", "create_webhook", `{"secret":"` + secretOf(1) + `","n":1}`}, secretOf(1))
	resetScrub(t, db)
	db.scrubChatCredentials()
	first := chatText(t, db)

	// A second pass with no marker changes nothing.
	resetScrub(t, db)
	db.scrubChatCredentials()
	if chatText(t, db) != first {
		t.Fatal("second pass changed already scrubbed rows")
	}

	// With the marker set the pass is skipped, even for a row it would match.
	seedChatCall(t, db, scrubSeed{"late", "c", "create_api_key", `{"key":"` + secretOf(2) + `"}`}, "")
	db.scrubChatCredentials()
	if !strings.Contains(chatText(t, db), secretOf(2)) {
		t.Fatal("scrub ran although the marker was set")
	}

	// Boot runs it: clearing the marker and re-running Migrate scrubs the row.
	resetScrub(t, db)
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(chatText(t, db), secretOf(2)) || db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		t.Fatal("Migrate did not run the scrub")
	}
}

func TestScrubUsesTheSharedCredentialToolList(t *testing.T) {
	// The migration, the live redaction and the registry all read onetime.Fields.
	want := map[string]string{"create_api_key": "key", "create_webhook": "secret", "create_inbound_webhook": "secret"}
	if len(onetime.Fields) != len(want) {
		t.Fatalf("onetime.Fields = %v", onetime.Fields)
	}
	for name, path := range want {
		if got := onetime.Fields[name]; len(got) != 1 || got[0] != path {
			t.Fatalf("%s paths = %v, want [%s]", name, got, path)
		}
	}
}
