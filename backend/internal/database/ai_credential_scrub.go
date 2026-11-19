package database

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/onetime"
)

// Rows rewritten per transaction, and the ceiling for one boot; the persisted
// cursor resumes whatever is left on the next boot. Variables so tests can force
// several batches.
var (
	chatScrubBatchRows  = 100
	chatScrubMaxBatches = 200
)

const (
	chatScrubMarkerKey = "chat_credential_scrub_done"
	chatScrubCursorKey = "chat_credential_scrub_cursor"

	// Free-text copies are located by value; refuse values short enough to
	// match ordinary prose.
	chatScrubMinValueLen = 16
)

// scrubChatCredentials is the third explicit carve-out to additive-only
// migrations (CONTRACT.md §9). Before Chat redacted one-time credentials at the
// source, the result of create_api_key / create_webhook / create_inbound_webhook
// was stored in full. This replaces, per credential-minting tool call:
//
//   - ai_tool_calls.result  (the audit row)
//   - ai_messages.content of the role=tool message holding that same result
//   - every other copy of the credential value in the same conversation:
//     ai_messages.content and .parts of any role (user text, assistant text and
//     thinking, other tool messages), ai_tool_calls.args and .result of later
//     calls, and the conversation title
//
// with the placeholder. Rows that do not contain the credential are never
// written, and other conversations are never touched. Each batch
// is one transaction that also advances a persisted rowid cursor, so an
// interrupted boot resumes; the marker is written only after the walk completes.
// Nothing is dropped and the schema is unchanged.
func (db *Database) scrubChatCredentials() {
	if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
		return
	}
	var names []string
	var marks []string
	for name := range onetime.Fields {
		names = append(names, name)
		marks = append(marks, "?")
	}
	cursor := int64(0)
	if v := db.GetSystemConfigString(chatScrubCursorKey, ""); v != "" {
		if n, ok := parseCursor(v); ok {
			cursor = n
		}
	}
	var scrubbed int64
	for range chatScrubMaxBatches {
		n, next, done, err := db.scrubChatBatch(cursor, names, marks)
		if err != nil {
			slog.Warn("chat credential scrub failed", "err", err)
			return
		}
		scrubbed += n
		cursor = next
		if done {
			if scrubbed > 0 {
				slog.Info("scrubbed stored Chat credentials", "tool_calls", scrubbed)
			}
			if err := db.SetSystemConfig(chatScrubMarkerKey, "true"); err != nil {
				slog.Warn("chat credential scrub marker write failed", "err", err)
			}
			return
		}
	}
	slog.Info("Chat credential scrub continues next boot", "tool_calls_scrubbed", scrubbed)
}

func parseCursor(v string) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= 0
}

type chatScrubRow struct {
	rowid                                 int64
	id, conversationID, tool, callID, res string
}

func (db *Database) scrubChatBatch(cursor int64, names, marks []string) (scrubbed, next int64, done bool, err error) {
	args := []any{cursor}
	for _, n := range names {
		args = append(args, n)
	}
	args = append(args, chatScrubBatchRows)
	rows, err := db.write.Query(`SELECT rowid, id, conversation_id, tool_name, COALESCE(call_id,''), result
		FROM ai_tool_calls WHERE rowid > ? AND tool_name IN (`+strings.Join(marks, ",")+`)
		AND result IS NOT NULL AND result <> '' ORDER BY rowid LIMIT ?`, args...)
	if err != nil {
		return 0, cursor, false, err
	}
	var batch []chatScrubRow
	for rows.Next() {
		var r chatScrubRow
		if err := rows.Scan(&r.rowid, &r.id, &r.conversationID, &r.tool, &r.callID, &r.res); err != nil {
			_ = rows.Close()
			return 0, cursor, false, err
		}
		batch = append(batch, r)
	}
	if err := rows.Close(); err != nil {
		return 0, cursor, false, err
	}
	if rows.Err() != nil {
		return 0, cursor, false, rows.Err()
	}
	if len(batch) == 0 {
		return 0, cursor, true, nil
	}

	tx, err := db.write.Begin()
	if err != nil {
		return 0, cursor, false, err
	}
	defer func() { _ = tx.Rollback() }()
	next = cursor
	for _, r := range batch {
		changed, err := scrubChatToolCall(tx, r)
		if err != nil {
			return 0, cursor, false, err
		}
		if changed {
			scrubbed++
		}
		next = r.rowid
	}
	if _, err := tx.Exec(`INSERT INTO system_config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		chatScrubCursorKey, formatCursor(next)); err != nil {
		return 0, cursor, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, cursor, false, err
	}
	return scrubbed, next, len(batch) < chatScrubBatchRows, nil
}

func formatCursor(n int64) string { return strconv.FormatInt(n, 10) }

func jsonString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// scrubChatToolCall rewrites one minting call's stored copies. Messages are
// handled before the audit row because the credential value is read from it.
func scrubChatToolCall(tx *sql.Tx, r chatScrubRow) (bool, error) {
	redacted, changed := onetime.Redact(r.res, onetime.Fields[r.tool])
	if !changed {
		return false, nil
	}
	// The tool-role message stores the identical result string, correlated by
	// call id; matching content as well keeps a reused provider call id from
	// reaching another tool's message. Done before the by-value pass, which would
	// otherwise change the content this match depends on. A call id the provider
	// never assigned is omitted from parts entirely.
	if _, err := tx.Exec(`UPDATE ai_messages SET content = ?1
		WHERE conversation_id = ?2 AND role = 'tool' AND content = ?3
		AND (?4 = '' OR instr(parts, ?4) > 0)`, redacted, r.conversationID, r.res,
		callIDPart(r.callID)); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE ai_tool_calls SET result = ? WHERE id = ?`, redacted, r.id); err != nil {
		return false, err
	}
	// Every other copy of the credential in the same conversation: the user's
	// own messages, the model's replies and thinking, later tool arguments and
	// results. Rows that do not contain the value are never written.
	if values, ok := onetime.Values(r.res, onetime.Fields[r.tool]); ok {
		for _, v := range values {
			if len(v) < chatScrubMinValueLen {
				continue
			}
			if err := scrubConversationCopies(tx, r.conversationID, v); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// scrubConversationCopies replaces one credential value, in its plain and
// JSON-escaped forms, in all stored content of one conversation.
func scrubConversationCopies(tx *sql.Tx, conversationID, value string) error {
	forms := []string{value}
	if esc := jsonString(value); esc[1:len(esc)-1] != value {
		forms = append(forms, esc[1:len(esc)-1])
	}
	for _, f := range forms {
		for _, stmt := range []string{
			`UPDATE ai_messages SET content = replace(content, ?1, ?2), parts = replace(parts, ?1, ?2)
			 WHERE conversation_id = ?3 AND (instr(content, ?1) > 0 OR instr(parts, ?1) > 0)`,
			`UPDATE ai_tool_calls SET args = replace(args, ?1, ?2), result = replace(result, ?1, ?2)
			 WHERE conversation_id = ?3 AND (instr(args, ?1) > 0 OR instr(COALESCE(result,''), ?1) > 0)`,
			`UPDATE ai_conversations SET title = replace(title, ?1, ?2)
			 WHERE id = ?3 AND instr(title, ?1) > 0`,
		} {
			if _, err := tx.Exec(stmt, f, onetime.Placeholder, conversationID); err != nil {
				return err
			}
		}
	}
	return nil
}

func callIDPart(callID string) string {
	if callID == "" {
		return ""
	}
	return `"call_id":` + jsonString(callID)
}
