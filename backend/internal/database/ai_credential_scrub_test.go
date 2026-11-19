package database

import (
	"encoding/json"
	"errors"
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

// seedChatTurn stores a minting call the way the agent does: an assistant message
// requests it (the audit row carries that message's id), the tool message holds
// the result, and an optional assistant reply echoes the credential.
func seedChatTurn(t *testing.T, db *Database, s scrubSeed, echo string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT OR IGNORE INTO ai_conversations (id) VALUES (?)`, s.conv)
	req := "req-" + s.conv + s.callID
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, ?, 'assistant', 'calling the tool', '[]', ?)`, req, s.conv, seqOf(t, db, s.conv))
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, message_id, call_id, tool_name, result, status) VALUES (?, ?, ?, ?, ?, ?, 'succeeded')`,
		"tc-"+s.conv+"-"+s.callID+s.tool, s.conv, req, s.callID, s.tool, s.result)
	exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, ?, 'tool', ?, ?, ?)`,
		"m-tool-"+s.conv+s.callID+s.tool, s.conv, s.result, fmt.Sprintf(`[{"type":"tool_result","call_id":%q,"status":"succeeded"}]`, s.callID), seqOf(t, db, s.conv))
	if echo != "" {
		exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, ?, 'assistant', ?, '[]', ?)`,
			"m-asst-"+s.conv+s.callID+s.tool, s.conv, "Here is your key: "+echo, seqOf(t, db, s.conv))
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
	_ = db.scrubChatCredentials()

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
		seedChatTurn(t, db, c, "")
	}
	resetScrub(t, db)
	_ = db.scrubChatCredentials()

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
	_ = db.scrubChatCredentials()
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

func TestScrubChatCredentialsIdempotentAndMarkerGated(t *testing.T) {
	db := newTestDB(t)
	seedChatCall(t, db, scrubSeed{"i", "c", "create_webhook", `{"secret":"` + secretOf(1) + `","n":1}`}, secretOf(1))
	resetScrub(t, db)
	_ = db.scrubChatCredentials()
	first := chatText(t, db)

	// A second pass with no marker changes nothing.
	resetScrub(t, db)
	_ = db.scrubChatCredentials()
	if chatText(t, db) != first {
		t.Fatal("second pass changed already scrubbed rows")
	}

	// With the marker set the pass is skipped, even for a row it would match.
	seedChatCall(t, db, scrubSeed{"late", "c", "create_api_key", `{"key":"` + secretOf(2) + `"}`}, "")
	_ = db.scrubChatCredentials()
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
	_ = db.scrubChatCredentials()

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
	_ = db.scrubChatCredentials()
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
	_ = db.scrubChatCredentials()

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
	_ = db.scrubChatCredentials()
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

// scrubUntilDone runs boots until the marker is set, asserting the hard bounds on
// every boot, and returns the per-boot stats.
func scrubUntilDone(t *testing.T, db *Database, maxBoots, reads, writes int) []scrubStats {
	t.Helper()
	return scrubUntilDoneSlack(t, db, maxBoots, reads, writes, 0)
}

// scrubUntilDoneSlack allows each boot to exceed the bound by at most slack rows
// read and written: the one documented exception, a resumed final phase that
// rewrites a batch's minting rows together.
func scrubUntilDoneSlack(t *testing.T, db *Database, maxBoots, reads, writes, slack int) []scrubStats {
	t.Helper()
	var boots []scrubStats
	for db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		if len(boots) >= maxBoots {
			t.Fatalf("scrub still unfinished after %d boots", maxBoots)
		}
		st := db.scrubChatCredentials()
		if st.scanned > reads+slack || st.rewritten > writes+slack {
			t.Fatalf("boot %d exceeded the hard bound: read %d/%d rewritten %d/%d (slack %d)", len(boots)+1, st.scanned, reads, st.rewritten, writes, slack)
		}
		if st.scanned == 0 && db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
			t.Fatalf("boot %d made no progress", len(boots)+1)
		}
		boots = append(boots, st)
	}
	return boots
}

func rowsHolding(t *testing.T, db *Database, secret string) int {
	t.Helper()
	n := 0
	for _, q := range []string{
		`SELECT COUNT(*) FROM ai_messages WHERE instr(content, ?1) > 0 OR instr(parts, ?1) > 0`,
		`SELECT COUNT(*) FROM ai_tool_calls WHERE instr(args, ?1) > 0 OR instr(COALESCE(result,''), ?1) > 0`,
		`SELECT COUNT(*) FROM ai_conversations WHERE instr(title, ?1) > 0`,
	} {
		var c int
		if err := db.read.QueryRow(q, secret).Scan(&c); err != nil {
			t.Fatal(err)
		}
		n += c
	}
	return n
}

// One conversation three times the budget finishes over several boots; no boot
// exceeds the budget and nothing is rewritten or scanned twice.
func TestScrubChatCredentialsHardBoundLargeConversation(t *testing.T) {
	db := newTestDB(t)
	const readBudget, writeBudget = 40, 25
	setScrubBudget(t, 50, readBudget, writeBudget)
	secret := secretOf(30)
	seedChatCall(t, db, scrubSeed{"big", "c1", "create_api_key", `{"key":"` + secret + `"}`}, secret)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 90; i++ { // 90 echoes: well over three budgets of writes
		exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, 'big', 'assistant', ?, '[]', ?)`,
			fmt.Sprintf("echo%03d", i), "again "+secret, 1000+i)
	}
	for i := 0; i < 60; i++ { // and clean rows, which are only read
		exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, 'big', 'user', 'hello', '[]', ?)`, fmt.Sprintf("clean%03d", i), 2000+i)
	}
	for i := 0; i < 30; i++ {
		exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES (?, 'big', ?, 'set_secret', ?, '{}', 'succeeded')`,
			fmt.Sprintf("later%03d", i), fmt.Sprintf("l%d", i), `{"value":"`+secret+`"}`)
	}
	exec(`UPDATE ai_conversations SET title = ? WHERE id = 'big'`, "k "+secret)
	holding := rowsHolding(t, db, secret) // rows that must each be written exactly once
	var rowsInConv int
	for _, q := range []string{`SELECT COUNT(*) FROM ai_messages WHERE conversation_id = 'big'`, `SELECT COUNT(*) FROM ai_tool_calls WHERE conversation_id = 'big'`} {
		var c int
		if err := db.read.QueryRow(q).Scan(&c); err != nil {
			t.Fatal(err)
		}
		rowsInConv += c
	}
	if holding < 3*writeBudget {
		t.Fatalf("seed too small to need several boots: %d rows", holding)
	}
	resetScrub(t, db)
	boots := scrubUntilDone(t, db, 60, readBudget, writeBudget)
	if len(boots) < 4 {
		t.Fatalf("expected several boots, got %d", len(boots))
	}
	reads, writes := 0, 0
	for _, b := range boots {
		reads += b.scanned
		writes += b.rewritten
	}
	if writes != holding {
		t.Fatalf("rewrote %d rows over %d boots, want exactly %d (each once)", writes, len(boots), holding)
	}
	// Each row is scanned once, each written row is read once more, and every boot
	// pays only a few reads to re-fetch its minting row and the title.
	if limit := rowsInConv + holding + 6*len(boots); reads > limit {
		t.Fatalf("read %d rows over %d boots, want at most %d: rows were rescanned", reads, len(boots), limit)
	}
	if n := rowsHolding(t, db, secret); n != 0 {
		t.Fatalf("%d rows still hold the credential", n)
	}
	for range 2 { // finished: further boots do nothing
		if st := db.scrubChatCredentials(); st != (scrubStats{}) {
			t.Fatalf("work after the marker: %+v", st)
		}
	}
}

// Sparse histories (few minting rows among many unrelated calls) and dense ones
// (many minting rows in one conversation) both stay inside the bound.
func TestScrubChatCredentialsHardBoundSparseAndDense(t *testing.T) {
	db := newTestDB(t)
	setScrubBudget(t, 3, 20, 6)
	for i := 0; i < 70; i++ {
		seedChatCall(t, db, scrubSeed{fmt.Sprintf("sp%d", i), "c", "list_functions", `{"items":[]}`}, "")
	}
	for i := 0; i < 4; i++ {
		seedChatCall(t, db, scrubSeed{fmt.Sprintf("sp%d", i*17), "m", "create_api_key", `{"key":"` + secretOf(60+i) + `"}`}, secretOf(60+i))
	}
	for i := 0; i < 12; i++ {
		seedChatCall(t, db, scrubSeed{"dense", fmt.Sprintf("d%d", i), "create_webhook", `{"secret":"` + secretOf(70+i) + `"}`}, secretOf(70+i))
	}
	// Failed and non-minting rows interleaved are read (and counted) but never sources.
	seedChatCallStatus(t, db, scrubSeed{"dense", "bad", "create_webhook", `{"secret":"` + secretOf(99) + `"}`}, "", "failed")
	resetScrub(t, db)
	boots := scrubUntilDone(t, db, 400, 20, 6)
	if len(boots) < 6 {
		t.Fatalf("expected several boots, got %d", len(boots))
	}
	text := chatText(t, db)
	for _, i := range append([]int{60, 61, 62, 63}, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81) {
		if strings.Contains(text, secretOf(i)) {
			t.Fatalf("credential %d survived", i)
		}
	}
	if !strings.Contains(text, secretOf(99)) {
		t.Fatal("a failed call's value was taken as a source")
	}
}

// An interruption stops where the work stopped, a failed batch rolls back whole,
// and later boots complete without repeating finished conversations.
func TestScrubChatCredentialsResumesAndIsAtomicPerBatch(t *testing.T) {
	db := newTestDB(t)
	setScrubBudget(t, 50, 16, 1_000_000)
	for i := 1; i <= 5; i++ {
		seedChatCall(t, db, scrubSeed{fmt.Sprintf("r%d", i), "c", "create_api_key", `{"key":"` + secretOf(i) + `"}`}, secretOf(i))
	}
	resetScrub(t, db)
	state := func() string {
		return db.GetSystemConfigString(chatScrubCursorKey, "") + "|" + db.GetSystemConfigString(chatScrubPosKey, "")
	}
	db.scrubChatCredentials()
	if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" || state() == "|" {
		t.Fatalf("a 16-row budget cannot finish five conversations in one boot (state %q)", state())
	}
	if rowsHolding(t, db, secretOf(5)) == 0 {
		t.Fatal("the last conversation was reached too early")
	}
	// r4's assistant message cannot be written: its batch must roll back whole.
	if _, err := db.write.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON ai_messages WHEN NEW.id = 'm-asst-r4ccreate_api_key'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	var last string
	for i := 0; i < 20; i++ {
		before := state()
		db.scrubChatCredentials()
		if state() == before && last == before {
			break // stalled at the failing batch
		}
		last = before
	}
	if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
		t.Fatal("marker set although a batch failed")
	}
	for _, q := range []string{
		`SELECT result FROM ai_tool_calls WHERE conversation_id = 'r4'`,
		`SELECT content FROM ai_messages WHERE conversation_id = 'r4' AND role = 'tool'`,
		`SELECT content FROM ai_messages WHERE conversation_id = 'r4' AND role = 'assistant'`,
	} {
		if !strings.Contains(column(t, db, q), secretOf(4)) {
			t.Fatalf("the failed batch was partly committed: %s", q)
		}
	}
	for i := 1; i <= 3; i++ {
		if rowsHolding(t, db, secretOf(i)) != 0 {
			t.Fatalf("conversation r%d, finished before the failure, was left unscrubbed", i)
		}
	}
	if _, err := db.write.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	scrubUntilDone(t, db, 20, 16, 1_000_000)
	for i := 1; i <= 5; i++ {
		if rowsHolding(t, db, secretOf(i)) != 0 {
			t.Fatalf("credential %d survived the resumed boots", i)
		}
	}
}

// An iteration error aborts the batch: nothing is written, the cursor does not
// advance and the marker stays unset; clearing it lets the next boot finish.
func TestScrubChatCredentialsIterationErrorAbortsBatch(t *testing.T) {
	for _, stage := range []string{"mint", "messages", "tools"} {
		t.Run(stage, func(t *testing.T) {
			db := newTestDB(t)
			seedChatCall(t, db, scrubSeed{"e1", "c", "create_api_key", `{"key":"` + secretOf(1) + `"}`}, secretOf(1))
			if _, err := db.write.Exec(`INSERT INTO ai_tool_calls (id, conversation_id, call_id, tool_name, args, result, status) VALUES ('t-e1','e1','z','set_secret',?,'{}','succeeded')`, `{"v":"`+secretOf(1)+`"}`); err != nil {
				t.Fatal(err)
			}
			resetScrub(t, db)
			before := chatText(t, db)
			chatScrubInjectErr = func(s string) error {
				if s == stage {
					return errors.New("injected iteration error")
				}
				return nil
			}
			t.Cleanup(func() { chatScrubInjectErr = nil })
			db.scrubChatCredentials()
			if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
				t.Fatal("marker set after an iteration error")
			}
			if db.GetSystemConfigString(chatScrubCursorKey, "") != "" || db.GetSystemConfigString(chatScrubPosKey, "") != "" {
				t.Fatal("cursor or position advanced after an iteration error")
			}
			if chatText(t, db) != before {
				t.Fatal("rows were written by an aborted batch")
			}
			chatScrubInjectErr = nil
			scrubUntilDone(t, db, 10, 10000, 10000)
			if rowsHolding(t, db, secretOf(1)) != 0 {
				t.Fatal("recovery boot did not scrub")
			}
		})
	}
}

// The candidate and conversation reads are keyset ranges over existing indexes,
// not sorts of the whole conversation.
func TestScrubQueriesUseIndexes(t *testing.T) {
	db := newTestDB(t)
	for name, q := range map[string]string{
		"mint":     `SELECT rowid FROM ai_tool_calls WHERE tool_name = 'create_api_key' AND rowid > 0 AND rowid <= 99 ORDER BY rowid LIMIT 5`,
		"messages": `SELECT rowid, seq, id FROM ai_messages WHERE conversation_id = 'c' AND (seq, rowid) > (0, 0) ORDER BY seq, rowid LIMIT 5`,
		"tools":    `SELECT rowid, id FROM ai_tool_calls WHERE conversation_id = 'c' AND rowid > 0 ORDER BY rowid LIMIT 5`,
	} {
		rows, err := db.read.Query(`EXPLAIN QUERY PLAN ` + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var a, b, c int
			var detail string
			_ = rows.Scan(&a, &b, &c, &detail)
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		if !strings.Contains(joined, "USING INDEX") && !strings.Contains(joined, "USING COVERING INDEX") || strings.Contains(joined, "TEMP B-TREE") {
			t.Errorf("%s: plan = %s", name, joined)
		}
	}
}

// The reviewer's scenario: two minting calls in one conversation, the second
// result repeating the first credential, and enough other rows that a boot runs
// out of budget around the final phase. Whatever boot that happens in, the next
// one must leave no copy of either credential, and the marker may only appear
// once none is left.
func seedFinalPhaseScenario(t *testing.T, db *Database, echoes int) (string, string) {
	t.Helper()
	a, b := secretOf(11), secretOf(12)
	seedChatCall(t, db, scrubSeed{"x", "a", "create_api_key", `{"key":"` + a + `"}`}, "")
	seedChatCall(t, db, scrubSeed{"x", "b", "create_webhook", `{"subscription":{"name":"copy of ` + a + `"},"secret":"` + b + `"}`}, "")
	if _, err := db.write.Exec(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq)
		SELECT 'echo'||i, 'x', 'assistant', 'again `+a+`', '[]', 1000+i FROM n`, echoes-1); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func checkFinalPhaseScenario(t *testing.T, db *Database, a, b string, maxBoots, reads, writes int) int {
	t.Helper()
	resetScrub(t, db)
	boots := 0
	for db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		if boots++; boots > maxBoots {
			t.Fatalf("unfinished after %d boots", maxBoots)
		}
		st := db.scrubChatCredentials()
		if st.scanned > reads+4 || st.rewritten > writes+4 {
			t.Fatalf("boot %d beyond bound plus the 4-row final phase (2 minting rows and their tool messages): %+v", boots, st)
		}
		if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
			for _, v := range []string{a, b} {
				if n := rowsHolding(t, db, v); n != 0 {
					t.Fatalf("marker set after boot %d with %d rows still holding a credential", boots, n)
				}
			}
		}
	}
	return boots
}

func TestScrubChatCredentialsFinalPhaseIsNeverSplitReducedBudget(t *testing.T) {
	multi := 0
	for reads := 16; reads <= 70; reads++ {
		db := newTestDB(t)
		setScrubBudget(t, 50, reads, 12)
		a, b := seedFinalPhaseScenario(t, db, 9)
		if boots := checkFinalPhaseScenario(t, db, a, b, 40, reads, 12); boots > 1 {
			multi++
		}
		if rowsHolding(t, db, a)+rowsHolding(t, db, b) != 0 {
			t.Fatalf("reads=%d: a credential survived", reads)
		}
	}
	if multi == 0 {
		t.Fatal("no budget in the sweep exhausted the first boot; the scenario is not exercised")
	}
}

func TestScrubChatCredentialsFinalPhaseIsNeverSplitDefaultBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds ~5000 rows per run")
	}
	multi := 0
	// Around 4,997 echoed messages the first boot's 10,000 reads run out right at
	// the final phase.
	for echoes := 4990; echoes <= 5000; echoes++ {
		db := newTestDB(t)
		a, b := seedFinalPhaseScenario(t, db, echoes)
		if boots := checkFinalPhaseScenario(t, db, a, b, 10, chatScrubMaxScanned, chatScrubMaxRewritten); boots > 1 {
			multi++
		}
		if rowsHolding(t, db, a)+rowsHolding(t, db, b) != 0 {
			t.Fatalf("echoes=%d: a credential survived", echoes)
		}
	}
	if multi == 0 {
		t.Fatal("no run exhausted the default budget in the first boot")
	}
}

// A later batch's succeeded minting result is malformed JSON. An earlier batch
// may already have rewritten the text of its tool message, so it no longer equals
// the audit result: the tool message is correlated by call id and replaced
// wholesale with the audit row, and no plaintext remains when the marker is set.
func TestScrubChatCredentialsMalformedResultToolMessageByCallID(t *testing.T) {
	for _, batch := range []int{1, 50} {
		t.Run(fmt.Sprintf("batch%d", batch), func(t *testing.T) {
			db := newTestDB(t)
			setScrubBudget(t, batch, 10000, 10000)
			a, b := secretOf(13), secretOf(14)
			seedChatTurn(t, db, scrubSeed{"m", "a", "create_api_key", `{"key":"` + a + `"}`}, "")
			seedChatTurn(t, db, scrubSeed{"m", "b", "create_webhook", `{"secret":"` + b + `"} trailing copy ` + a}, "")
			resetScrub(t, db)
			for boots := 0; db.GetSystemConfigString(chatScrubMarkerKey, "") != "true"; boots++ {
				if boots > 20 {
					t.Fatal("unfinished")
				}
				db.scrubChatCredentials()
			}
			text := chatText(t, db)
			for _, v := range []string{a, b} {
				if strings.Contains(text, v) {
					t.Fatalf("plaintext %s remains with the marker set:\n%s", v[:9], text)
				}
			}
			want := `"` + scrubPlaceholder + `"`
			if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE call_id = 'b'`); got != want {
				t.Fatalf("audit result = %s", got)
			}
			if got := column(t, db, `SELECT content FROM ai_messages WHERE conversation_id = 'm' AND role = 'tool' AND parts LIKE '%"b"%'`); got != want {
				t.Fatalf("tool message = %s, want a wholesale replacement", got)
			}
		})
	}
}

// Correlation by call id takes the first tool message after the requesting
// assistant message, so a provider that reuses a call id elsewhere in the
// conversation does not lose another call's message.
func TestScrubChatCredentialsCallIDReuseDoesNotReachOtherMessages(t *testing.T) {
	db := newTestDB(t)
	secret := secretOf(15)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO ai_conversations (id) VALUES ('d')`)
	part := `[{"type":"tool_result","call_id":"dup","status":"succeeded"}]`
	msg := func(id, role, content, parts string, seq int) {
		exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, 'd', ?, ?, ?, ?)`, id, role, content, parts, seq)
	}
	msg("a0", "assistant", "first call", "[]", 1)
	msg("t0", "tool", `{"items":["keepme"]}`, part, 2) // an earlier call that used the same id
	msg("a1", "assistant", "mint", "[]", 3)
	bad := `{"key":"` + secret + `"} trailing`
	msg("t1", "tool", bad, part, 4)
	msg("a2", "assistant", "later", "[]", 5)
	msg("t2", "tool", `{"items":["alsokeep"]}`, part, 6) // a later call that reuses the id
	exec(`INSERT INTO ai_tool_calls (id, conversation_id, message_id, call_id, tool_name, args, result, status) VALUES ('mint','d','a1','dup','create_api_key','{}',?,'succeeded')`, bad)
	resetScrub(t, db)
	db.scrubChatCredentials()
	if db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
		t.Fatal("not finished")
	}
	if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 't1'`); got != `"`+scrubPlaceholder+`"` {
		t.Fatalf("the minting call's own tool message = %s", got)
	}
	for id, want := range map[string]string{"t0": `{"items":["keepme"]}`, "t2": `{"items":["alsokeep"]}`} {
		if got := column(t, db, `SELECT content FROM ai_messages WHERE id = ?`, id); got != want {
			t.Errorf("%s was altered through a reused call id: %s", id, got)
		}
	}
}

// Nothing is changed that cannot be proven. The reviewer's two reachable cases:
// (1) the same call id twice in one turn, (2) an id reused in a later turn after
// the call's own tool message was deleted. An unrelated tool result that happens
// to have a "key" field stays byte-identical, and so does every other message.
func TestScrubChatCredentialsNeverChangesWhatItCannotProve(t *testing.T) {
	secret := secretOf(16)
	unrelated := `{"key":"unrelated-setting"}`
	setup := func(t *testing.T) (*Database, func(string, string, string, string, int)) {
		db := newTestDB(t)
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := db.write.Exec(q, args...); err != nil {
				t.Fatal(err)
			}
		}
		exec(`INSERT INTO ai_conversations (id) VALUES ('p')`)
		msg := func(id, role, content, call string, seq int) {
			parts := "[]"
			if role == "tool" {
				parts = fmt.Sprintf(`[{"type":"tool_result","call_id":%q,"status":"succeeded"}]`, call)
			}
			exec(`INSERT INTO ai_messages (id, conversation_id, role, content, parts, seq) VALUES (?, 'p', ?, ?, ?, ?)`, id, role, content, parts, seq)
		}
		return db, func(id, role, content, call string, seq int) { msg(id, role, content, call, seq) }
	}
	audit := func(t *testing.T, db *Database, id, msgID, call, tool, result string) {
		t.Helper()
		if _, err := db.write.Exec(`INSERT INTO ai_tool_calls (id, conversation_id, message_id, call_id, tool_name, args, result, status) VALUES (?, 'p', ?, ?, ?, '{}', ?, 'succeeded')`,
			id, msgID, call, tool, result); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(t *testing.T, db *Database) scrubStats {
		t.Helper()
		resetScrub(t, db)
		var total scrubStats
		for i := 0; i < 5 && db.GetSystemConfigString(chatScrubMarkerKey, "") != "true"; i++ {
			st := db.scrubChatCredentials()
			total.ambiguous += st.ambiguous
		}
		if db.GetSystemConfigString(chatScrubMarkerKey, "") != "true" {
			t.Fatal("not finished")
		}
		return total
	}

	t.Run("same call id twice in one turn", func(t *testing.T) {
		db, msg := setup(t)
		msg("a1", "assistant", "two calls", "", 1)
		bad := `{"key":"` + secret + `"} trailing`
		msg("t-mint", "tool", bad, "call_0", 2)
		msg("t-kv", "tool", unrelated, "call_0", 3) // a kv_put whose id the provider repeated
		audit(t, db, "mint", "a1", "call_0", "create_api_key", bad)
		audit(t, db, "kv", "a1", "call_0", "kv_put", unrelated)
		st := finish(t, db)
		if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 't-kv'`); got != unrelated {
			t.Fatalf("unrelated tool message changed: %s", got)
		}
		if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE id = 'kv'`); got != unrelated {
			t.Fatalf("unrelated audit row changed: %s", got)
		}
		if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 't-mint'`); got != bad {
			t.Fatalf("ambiguous message was changed: %s", got)
		}
		if st.ambiguous == 0 {
			t.Fatal("the skipped message was not counted")
		}
		// The audit row of the minting call itself is the source and is redacted.
		if got := column(t, db, `SELECT result FROM ai_tool_calls WHERE id = 'mint'`); got != `"`+scrubPlaceholder+`"` {
			t.Fatalf("minting audit row = %s", got)
		}
	})

	t.Run("id reused in a later turn after the result was deleted", func(t *testing.T) {
		db, msg := setup(t)
		msg("a1", "assistant", "mint", "", 1)
		// the minting call's own tool message (seq 2) was deleted
		msg("a2", "assistant", "later", "", 3)
		msg("t-kv", "tool", unrelated, "call_0", 4)
		bad := `{"key":"` + secret + `"} trailing`
		audit(t, db, "mint", "a1", "call_0", "create_api_key", bad)
		audit(t, db, "kv", "a2", "call_0", "kv_put", unrelated)
		finish(t, db)
		if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 't-kv'`); got != unrelated {
			t.Fatalf("a later turn's result was rewritten: %s", got)
		}
	})

	t.Run("a parseable result redacts only a proven value", func(t *testing.T) {
		db, msg := setup(t)
		msg("a1", "assistant", "mint", "", 1)
		good := `{"key":"` + secret + `"}`
		msg("t-mint", "tool", good, "call_0", 2)
		msg("a2", "assistant", "later", "", 3)
		msg("t-kv", "tool", unrelated, "call_0", 4) // reuses the id later
		audit(t, db, "mint", "a1", "call_0", "create_api_key", good)
		audit(t, db, "kv", "a2", "call_0", "kv_put", unrelated)
		// Even when the lookup lands on the wrong message (the real one deleted),
		// a field is replaced only if it holds the credential.
		if _, err := db.write.Exec(`DELETE FROM ai_messages WHERE id = 't-mint'`); err != nil {
			t.Fatal(err)
		}
		finish(t, db)
		if got := column(t, db, `SELECT content FROM ai_messages WHERE id = 't-kv'`); got != unrelated {
			t.Fatalf("an unproven field was redacted by name: %s", got)
		}
		if strings.Contains(chatText(t, db), secret) {
			t.Fatal("credential survived")
		}
	})
}
