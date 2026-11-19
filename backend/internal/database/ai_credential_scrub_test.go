package database

import (
	"encoding/json"
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
	seedChatCallStatus(t, db, s, echo, "succeeded")
}

func seedChatCallStatus(t *testing.T, db *Database, s scrubSeed, echo, status string) {
	t.Helper()
	if _, err := db.write.Exec(`INSERT OR IGNORE INTO ai_conversations (id) VALUES (?)`, s.conv); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, result, status)
		VALUES (?, ?, ?, ?, ?, ?)`, "tc-"+s.conv+"-"+s.callID+s.tool, s.conv, s.callID, s.tool, s.result, status); err != nil {
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

func setScrubBudget(t *testing.T, batch, scanned, rewritten int) {
	t.Helper()
	ob, os, or := chatScrubBatchRows, chatScrubMaxScanned, chatScrubMaxRewritten
	t.Cleanup(func() { chatScrubBatchRows, chatScrubMaxScanned, chatScrubMaxRewritten = ob, os, or })
	chatScrubBatchRows, chatScrubMaxScanned, chatScrubMaxRewritten = batch, scanned, rewritten
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
	// reusing the same provider call id; and a second conversation that happens
	// to contain the same string.
	other := `{"key":"not-a-credential-` + strings.Repeat("z", 20) + `","secret":"s"}`
	seedChatCall(t, db, scrubSeed{"mine", "x1", "list_secrets", other}, "")
	seedChatCall(t, db, scrubSeed{"elsewhere", "x1", "invoke_function", `{"echo":"` + secret + `"}`}, secret)
	if _, err := db.write.Exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('u1','elsewhere','user',?, '[]', 99)`, "my key is "+secret); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{
		"other tool row":          column(t, db, `SELECT result FROM ai_tool_calls WHERE tool_name = 'list_secrets'`),
		"other tool message":      column(t, db, `SELECT content FROM ai_messages WHERE id = 'm-tool-minex1list_secrets'`),
		"other conversation":      column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'elsewhere'`),
		"other conversation msg":  column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = 'elsewhere' AND role = 'assistant'`),
		"other conv user message": column(t, db, `SELECT content FROM ai_messages WHERE id = 'u1'`),
	}
	resetScrub(t, db)
	db.scrubChatCredentials()
	after := map[string]string{
		"other tool row":          column(t, db, `SELECT result FROM ai_tool_calls WHERE tool_name = 'list_secrets'`),
		"other tool message":      column(t, db, `SELECT content FROM ai_messages WHERE id = 'm-tool-minex1list_secrets'`),
		"other conversation":      column(t, db, `SELECT result FROM ai_tool_calls WHERE conversation_id = 'elsewhere'`),
		"other conversation msg":  column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = 'elsewhere' AND role = 'assistant'`),
		"other conv user message": column(t, db, `SELECT content FROM ai_messages WHERE id = 'u1'`),
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
	setScrubBudget(t, 2, 1, 1_000_000) // two minting rows per batch, one batch per boot
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

// The credential is replaced wherever it was copied within the conversation:
// user text, assistant text and thinking, other tool messages, later tool-call
// arguments and results, and the title. A row without the value is not written.
func TestScrubChatCredentialsEveryCopyInTheConversation(t *testing.T) {
	db := newTestDB(t)
	secret := secretOf(9)
	escaped := `quote"inside-` + strings.Repeat("q", 20)
	seedChatCall(t, db, scrubSeed{"k", "m1", "create_api_key", `{"key":"` + secret + `"}`}, secret)
	seedChatCall(t, db, scrubSeed{"k", "m2", "create_webhook", `{"secret":` + jsonString(escaped) + `}`}, "")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE ai_conversations SET title = ? WHERE id = 'k'`, "rotate "+secret)
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('user1','k','user',?,'[]',50)`, "please store "+secret)
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('think1','k','assistant','done',?,51)`,
		`[{"type":"thinking","text":"the key is `+secret+` and the other is `+jsonString(escaped)[1:len(jsonString(escaped))-1]+`"},{"type":"text","text":"done"}]`)
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES ('later1','k','m3','set_secret',?,?, 'succeeded')`,
		`{"key":"API_KEY","value":"`+secret+`"}`, `{"stored":"`+secret+`"}`)
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('toolmsg2','k','tool',?,'[{"type":"tool_result","call_id":"m3"}]',52)`, `{"stored":"`+secret+`"}`)
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES ('clean1','k','m4','list_functions','{}','{"items":[]}','succeeded')`)
	seedChatCall(t, db, scrubSeed{"other", "m1", "create_api_key", `{"key":"` + secretOf(10) + `"}`}, "")
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('user2','other','user',?,'[]',50)`, "same string "+secret)
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES ('later2','other','m3','set_secret',?,'{}', 'succeeded')`, `{"value":"`+secret+`"}`)
	cleanBefore := column(t, db, `SELECT id||args||result FROM ai_tool_calls WHERE id = 'clean1'`)
	otherBefore := column(t, db, `SELECT content FROM ai_messages WHERE id = 'user2'`) + column(t, db, `SELECT args FROM ai_tool_calls WHERE id = 'later2'`)

	resetScrub(t, db)
	db.scrubChatCredentials()

	for _, probe := range []struct{ name, q string }{
		{"user message", `SELECT content FROM ai_messages WHERE id = 'user1'`},
		{"thinking part", `SELECT parts FROM ai_messages WHERE id = 'think1'`},
		{"later tool args", `SELECT args FROM ai_tool_calls WHERE id = 'later1'`},
		{"later tool result", `SELECT result FROM ai_tool_calls WHERE id = 'later1'`},
		{"other tool message", `SELECT content FROM ai_messages WHERE id = 'toolmsg2'`},
		{"title", `SELECT title FROM ai_conversations WHERE id = 'k'`},
	} {
		got := column(t, db, probe.q)
		if strings.Contains(got, secret) || !strings.Contains(got, scrubPlaceholder) {
			t.Errorf("%s not scrubbed: %s", probe.name, got)
		}
	}
	// The JSON-escaped credential of the second call is gone too, structure intact.
	if parts := column(t, db, `SELECT parts FROM ai_messages WHERE id = 'think1'`); strings.Contains(parts, "quote") ||
		!strings.Contains(parts, `{"text":"done","type":"text"}`) {
		t.Errorf("JSON-escaped copy or surrounding structure mishandled: %s", parts)
	}
	for _, q := range []string{`SELECT parts FROM ai_messages WHERE id = 'think1'`, `SELECT args FROM ai_tool_calls WHERE id = 'later1'`, `SELECT result FROM ai_tool_calls WHERE id = 'later1'`} {
		var v any
		if err := json.Unmarshal([]byte(column(t, db, q)), &v); err != nil {
			t.Errorf("%s is no longer valid JSON: %v", q, err)
		}
	}
	if got := column(t, db, `SELECT id||args||result FROM ai_tool_calls WHERE id = 'clean1'`); got != cleanBefore {
		t.Errorf("row without the value was rewritten: %s", got)
	}
	// Another conversation holding the same string is a different conversation.
	if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 'user2'`) + column(t, db, `SELECT args FROM ai_tool_calls WHERE id = 'later2'`); got != otherBefore {
		t.Errorf("other conversation was modified: %s", got)
	}
}

// A later row must never restore a credential that an earlier step removed:
// the second result's metadata repeats the first credential.
func TestScrubChatCredentialsNeverRestoresFromStaleSnapshot(t *testing.T) {
	db := newTestDB(t)
	a, b := secretOf(1), secretOf(2)
	seedChatCall(t, db, scrubSeed{"s", "a", "create_api_key", `{"key":"` + a + `","name":"first"}`}, "")
	seedChatCall(t, db, scrubSeed{"s", "b", "create_webhook", `{"subscription":{"id":"s1","name":"copy of ` + a + `"},"secret":"` + b + `"}`}, "")
	resetScrub(t, db)
	db.scrubChatCredentials()
	text := chatText(t, db)
	for _, v := range []string{a, b} {
		if strings.Contains(text, v) {
			t.Fatalf("%s reappeared:\n%s", v, text)
		}
	}
	got := column(t, db, `SELECT result FROM ai_tool_calls WHERE call_id = 'b'`)
	if !strings.Contains(got, `"name":"copy of `+scrubPlaceholder+`"`) || !strings.Contains(got, `"secret":"`+scrubPlaceholder+`"`) {
		t.Fatalf("second result = %s", got)
	}
	// Same outcome when the two calls land in different batches.
	db2 := newTestDB(t)
	setScrubBudget(t, 1, 1, 1_000_000)
	seedChatCall(t, db2, scrubSeed{"s", "a", "create_api_key", `{"key":"` + a + `"}`}, "")
	seedChatCall(t, db2, scrubSeed{"s", "b", "create_webhook", `{"subscription":{"name":"copy of ` + a + `"},"secret":"` + b + `"}`}, "")
	resetScrub(t, db2)
	for range 4 {
		db2.scrubChatCredentials()
	}
	if t2 := chatText(t, db2); strings.Contains(t2, a) || strings.Contains(t2, b) {
		t.Fatalf("split batches left a credential:\n%s", t2)
	}
}

// JSON columns are rewritten through their decoded strings, and text matches
// must sit on token boundaries.
func TestScrubChatCredentialsJSONIntegrityAndBoundaries(t *testing.T) {
	db := newTestDB(t)
	secret := secretOf(3)
	seedChatCall(t, db, scrubSeed{"j", "m1", "create_api_key", `{"key":"` + secret + `"}`}, "")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	longer := secret + "-and-more"
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES ('a1','j','x1','set_secret',?,?, 'succeeded')`,
		// escaped unicode neighbours, markup characters, a number that must keep its digits, surrounding text
		`{"note":"é\u00e9 `+secret+`\u00e9 <b>&","n":12345678901234567890,"text":"use `+secret+`, then stop","other":"`+longer+`","dash":"pre\u002d`+secret+`"}`,
		`{"ok":true}`)
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('p1','j','assistant',?,?,60)`,
		"prefix"+secret+" and "+secret+"; and "+longer+" and x"+secret, `not json `+secret)
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES ('p2','j','assistant','ok',?,61)`,
		`[{"type":"text","text":"`+secret+`"}]  `)
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES ('bad1','j','x2','set_secret','{"v":"` + secret + `" broken','not json ` + secret + `', 'failed')`)

	resetScrub(t, db)
	db.scrubChatCredentials()

	args := column(t, db, `SELECT args FROM ai_tool_calls WHERE id = 'a1'`)
	var doc map[string]any
	if err := json.Unmarshal([]byte(args), &doc); err != nil {
		t.Fatalf("args are no longer valid JSON: %v\n%s", err, args)
	}
	if !strings.Contains(args, "12345678901234567890") || !strings.Contains(args, "<b>&") {
		t.Errorf("unrelated JSON content changed: %s", args)
	}
	if doc["note"] != "é\u00e9 "+scrubPlaceholder+"\u00e9 <b>&" {
		t.Errorf("unicode-adjacent credential not replaced on its boundary: %q", doc["note"])
	}
	if doc["text"] != "use "+scrubPlaceholder+", then stop" {
		t.Errorf("credential inside surrounding text: %q", doc["text"])
	}
	if doc["other"] != longer {
		t.Errorf("a longer value containing the credential was altered: %q", doc["other"])
	}
	if doc["dash"] != "pre-"+secret {
		t.Errorf("escaped hyphen is a token character, so this must stay: %q", doc["dash"])
	}
	// Plain text: boundaries decide, in the decoded text.
	if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 'p1'`); got != "prefix"+secret+" and "+scrubPlaceholder+"; and "+longer+" and x"+secret {
		t.Errorf("plain text boundaries wrong: %q", got)
	}
	// JSON columns that do not parse are left alone.
	if got := column(t, db, `SELECT parts FROM ai_messages WHERE id = 'p1'`); got != "not json "+secret {
		t.Errorf("unparseable parts were touched: %q", got)
	}
	if got := column(t, db, `SELECT parts FROM ai_messages WHERE id = 'p2'`); strings.Contains(got, secret) {
		t.Errorf("valid JSON with trailing whitespace not scrubbed: %q", got)
	}
	if got := column(t, db, `SELECT args||result FROM ai_tool_calls WHERE id = 'bad1'`); !strings.Contains(got, secret) {
		t.Errorf("unparseable non-minting JSON columns must be left untouched: %q", got)
	}
}

// Credentials come only from succeeded minting calls, and a failed call's result
// is never replaced wholesale.
func TestScrubChatCredentialsOnlySucceededCallsAreSources(t *testing.T) {
	db := newTestDB(t)
	good, failedSecret := secretOf(4), secretOf(5)
	seedChatCall(t, db, scrubSeed{"f", "ok", "create_api_key", `{"key":"` + good + `"}`}, "")
	seedChatCallStatus(t, db, scrubSeed{"f", "bad1", "create_api_key", `{"key":"` + failedSecret + `"}`}, "", "failed")
	seedChatCallStatus(t, db, scrubSeed{"f", "bad2", "create_webhook", `plain failure text`}, "", "failed")
	seedChatCallStatus(t, db, scrubSeed{"f", "bad3", "create_webhook", `{"error":"dup of ` + good + `"}`}, "", "failed")
	resetScrub(t, db)
	db.scrubChatCredentials()
	if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE call_id = 'bad1'`); !strings.Contains(got, failedSecret) {
		t.Errorf("value taken from a failed call: %s", got)
	}
	if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE call_id = 'bad2'`); got != "plain failure text" {
		t.Errorf("failed result replaced wholesale: %s", got)
	}
	if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE call_id = 'bad3'`); got != `{"error":"dup of `+scrubPlaceholder+`"}` {
		t.Errorf("failed result copy of an extracted value: %s", got)
	}
	if strings.Contains(column(t, db, `SELECT result FROM ai_tool_calls WHERE call_id = 'ok'`), good) {
		t.Error("succeeded call not scrubbed")
	}
}

// Work per boot is bounded on rows scanned and rewritten, from an indexed query,
// and the marker waits for completion across sparse and dense histories.
func TestScrubChatCredentialsBoundedWorkAcrossBoots(t *testing.T) {
	db := newTestDB(t)
	var plan []string
	rows, err := db.read.Query(`EXPLAIN QUERY PLAN SELECT rowid FROM ai_tool_calls WHERE tool_name = ? AND rowid > ? AND status = 'succeeded' ORDER BY rowid LIMIT 5`, "create_api_key", 0)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c int
		var detail string
		_ = rows.Scan(&a, &b, &c, &detail)
		plan = append(plan, detail)
	}
	rows.Close()
	if !strings.Contains(strings.Join(plan, " "), "idx_ai_tc_tool") {
		t.Fatalf("candidate query does not use the tool-name index: %v", plan)
	}

	// Sparse: many unrelated tool calls with a few minting rows among them.
	for i := 0; i < 60; i++ {
		seedChatCall(t, db, scrubSeed{fmt.Sprintf("sp%d", i), "c", "list_functions", `{"items":[]}`}, "")
	}
	for i := 0; i < 4; i++ {
		seedChatCall(t, db, scrubSeed{fmt.Sprintf("sp%d", i*15), "m", "create_api_key", `{"key":"` + secretOf(20+i) + `"}`}, secretOf(20+i))
	}
	// Dense: one conversation with many minting rows, each echoed in later messages.
	for i := 0; i < 12; i++ {
		seedChatCall(t, db, scrubSeed{"dense", fmt.Sprintf("d%d", i), "create_webhook", `{"secret":"` + secretOf(40+i) + `"}`}, secretOf(40+i))
	}
	leaked := func() int {
		n, text := 0, chatText(t, db)
		for _, i := range append([]int{20, 21, 22, 23}, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51) {
			if strings.Contains(text, secretOf(i)) {
				n++
			}
		}
		return n
	}
	setScrubBudget(t, 2, 1, 1) // two minting rows per batch; one batch per boot
	resetScrub(t, db)
	boots, previous := 0, leaked()
	for db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		if boots++; boots > 40 {
			t.Fatal("scrub never completed under a tiny budget")
		}
		db.scrubChatCredentials()
		if now := leaked(); now > previous {
			t.Fatalf("leak count grew from %d to %d", previous, now)
		} else {
			previous = now
		}
		if db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" && leaked() == 0 && boots < 3 {
			t.Fatalf("finished too fast to exercise resumption (boot %d)", boots)
		}
	}
	if leaked() != 0 {
		t.Fatalf("%d credentials left after completion", leaked())
	}
	if boots < 5 {
		t.Fatalf("expected several boots under the tiny budget, got %d", boots)
	}
	// Every boot but the last stopped on the budget, each doing one batch only.
	if db.GetSystemConfigString(chatScrubCursorKey, "") == "" {
		t.Fatal("cursor missing")
	}
}
