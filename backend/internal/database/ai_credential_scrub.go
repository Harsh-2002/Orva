package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/onetime"
)

// Minting rows read per transaction, and the work one boot may spend before it
// stops and resumes on the next. Variables so tests can force small budgets. The
// first batch of a boot always runs, so every boot makes progress.
var (
	chatScrubBatchRows    = 50
	chatScrubMaxScanned   = 10000 // rows read: minting rows plus conversation rows examined
	chatScrubMaxRewritten = 10000 // rows written
)

const (
	chatScrubMarkerKey = "chat_credential_scrub_done"
	chatScrubCursorKey = "chat_credential_scrub_cursor"

	// Copies are located by value; refuse values short enough to match prose.
	chatScrubMinValueLen = 16
)

// scrubChatCredentials is the third explicit carve-out to additive-only
// migrations (CONTRACT.md §9): best-effort removal of one-time credentials that
// Chat stored before it redacted them at the source.
//
// Credential values are taken only from the current rows of succeeded
// create_api_key / create_webhook / create_inbound_webhook calls. Within each
// affected conversation the exact value is then replaced with the placeholder:
//
//   - the minting call's own result (the credential field; wholesale if the
//     result is not valid JSON) and the role=tool message holding that result
//   - in every other ai_messages content/parts, ai_tool_calls args/result and
//     the conversation title: inside decoded JSON string leaves for JSON
//     columns (re-marshalled, never textually patched), and on token boundaries
//     (neighbours not in [A-Za-z0-9_-]) for plain text. JSON columns that do not
//     parse are left alone.
//
// Each batch is one transaction that applies the union of its conversations'
// values in one pass, re-reading each row immediately before writing it, and
// advances a persisted rowid cursor. Rows without the value, other
// conversations and other tools' minting results are not written. A per-boot
// budget on rows scanned and rewritten stops the pass and resumes next boot; the
// marker is set only after the walk completes. Nothing is dropped.
func (db *Database) scrubChatCredentials() {
	if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
		return
	}
	cursor := int64(0)
	if v := db.GetSystemConfigString(chatScrubCursorKey, ""); v != "" {
		if n, ok := parseCursor(v); ok {
			cursor = n
		}
	}
	var total scrubStats
	for first := true; ; first = false {
		if !first && (total.scanned >= chatScrubMaxScanned || total.rewritten >= chatScrubMaxRewritten) {
			slog.Info("Chat credential scrub continues next boot", "scanned", total.scanned, "rewritten", total.rewritten)
			return
		}
		st, next, done, err := db.scrubChatBatch(cursor)
		if err != nil {
			slog.Warn("chat credential scrub failed", "err", err)
			return
		}
		total.scanned += st.scanned
		total.rewritten += st.rewritten
		total.minted += st.minted
		cursor = next
		if done {
			if total.rewritten > 0 {
				slog.Info("scrubbed stored Chat credentials", "minting_calls", total.minted, "rows_rewritten", total.rewritten)
			}
			if err := db.SetSystemConfig(chatScrubMarkerKey, "true"); err != nil {
				slog.Warn("chat credential scrub marker write failed", "err", err)
			}
			return
		}
	}
}

func parseCursor(v string) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= 0
}

type scrubStats struct{ scanned, rewritten, minted int }

type mintRow struct {
	rowid                                    int64
	id, conversationID, tool, callID, result string
}

// convWork is everything one conversation's pass needs, derived from freshly
// read minting rows before any write of the batch.
type convWork struct {
	values []string           // longest first
	mints  map[string]mintRow // audit row id -> row, those whose result changes
	corr   []resultCorrelation
}

// resultCorrelation finds the role=tool message that stores a minting result.
type resultCorrelation struct{ orig, redacted, callPart string }

func (db *Database) scrubChatBatch(cursor int64) (st scrubStats, next int64, done bool, err error) {
	tx, err := db.write.Begin()
	if err != nil {
		return st, cursor, false, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := fetchMintRows(tx, cursor, chatScrubBatchRows)
	if err != nil {
		return st, cursor, false, err
	}
	if len(rows) == 0 {
		return st, cursor, true, nil
	}
	st.scanned += len(rows)
	next = cursor
	work := map[string]*convWork{}
	for _, r := range rows {
		if r.rowid > next {
			next = r.rowid
		}
		paths := onetime.Fields[r.tool]
		redacted, changed := onetime.Redact(r.result, paths)
		if !changed {
			continue // already scrubbed, or nothing to find
		}
		st.minted++
		w := work[r.conversationID]
		if w == nil {
			w = &convWork{mints: map[string]mintRow{}}
			work[r.conversationID] = w
		}
		w.mints[r.id] = r
		w.corr = append(w.corr, resultCorrelation{orig: r.result, redacted: redacted, callPart: callIDPart(r.callID)})
		if values, ok := onetime.Values(r.result, paths); ok {
			for _, v := range values {
				if len(v) >= chatScrubMinValueLen {
					w.values = append(w.values, v)
				}
			}
		}
	}
	convs := make([]string, 0, len(work))
	for c := range work {
		convs = append(convs, c)
	}
	sort.Strings(convs)
	for _, c := range convs {
		w := work[c]
		w.values = uniqueLongestFirst(w.values)
		if err := scrubConversation(tx, c, w, &st); err != nil {
			return scrubStats{}, cursor, false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO system_config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		chatScrubCursorKey, strconv.FormatInt(next, 10)); err != nil {
		return scrubStats{}, cursor, false, err
	}
	if err := tx.Commit(); err != nil {
		return scrubStats{}, cursor, false, err
	}
	return st, next, false, nil
}

// fetchMintRows returns up to limit succeeded minting rows after the cursor in
// rowid order, one indexed range per tool name (idx_ai_tc_tool).
func fetchMintRows(tx *sql.Tx, cursor int64, limit int) ([]mintRow, error) {
	names := make([]string, 0, len(onetime.Fields))
	for n := range onetime.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	var all []mintRow
	for _, name := range names {
		rows, err := tx.Query(`SELECT rowid, id, conversation_id, tool_name, COALESCE(call_id,''), result
			FROM ai_tool_calls WHERE tool_name = ? AND rowid > ? AND status = 'succeeded'
			AND result IS NOT NULL AND result <> '' ORDER BY rowid LIMIT ?`, name, cursor, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r mintRow
			if err := rows.Scan(&r.rowid, &r.id, &r.conversationID, &r.tool, &r.callID, &r.result); err != nil {
				_ = rows.Close()
				return nil, err
			}
			all = append(all, r)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].rowid < all[j].rowid })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func uniqueLongestFirst(values []string) []string {
	seen := map[string]bool{}
	out := values[:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// scrubConversation applies every value of the conversation in a single pass.
// Candidates are found by scanning; each is then re-read by id inside the
// transaction and rewritten from that fresh copy, so no write is computed from
// a snapshot older than another write to the same row.
func scrubConversation(tx *sql.Tx, convID string, w *convWork, st *scrubStats) error {
	// Messages.
	ids, err := scanIDs(tx, `SELECT id, role, content, parts FROM ai_messages WHERE conversation_id = ?`, convID, st,
		func(sc func(...any) error) (string, bool, error) {
			var id, role, content, parts string
			if err := sc(&id, &role, &content, &parts); err != nil {
				return "", false, err
			}
			_, _, changed := w.message(role, content, parts)
			return id, changed, nil
		})
	if err != nil {
		return err
	}
	for _, id := range ids {
		var role, content, parts string
		if err := tx.QueryRow(`SELECT role, content, parts FROM ai_messages WHERE id = ?`, id).Scan(&role, &content, &parts); err != nil {
			return err
		}
		c, p, changed := w.message(role, content, parts)
		if !changed {
			continue
		}
		if _, err := tx.Exec(`UPDATE ai_messages SET content = ?, parts = ? WHERE id = ?`, c, p, id); err != nil {
			return err
		}
		st.rewritten++
	}

	// Tool calls.
	ids, err = scanIDs(tx, `SELECT id, tool_name, status, args, result FROM ai_tool_calls WHERE conversation_id = ?`, convID, st,
		func(sc func(...any) error) (string, bool, error) {
			var id, tool, status, args string
			var result sql.NullString
			if err := sc(&id, &tool, &status, &args, &result); err != nil {
				return "", false, err
			}
			_, _, changed := w.toolCall(id, tool, status, args, result)
			return id, changed, nil
		})
	if err != nil {
		return err
	}
	for _, id := range ids {
		var tool, status, args string
		var result sql.NullString
		if err := tx.QueryRow(`SELECT tool_name, status, args, result FROM ai_tool_calls WHERE id = ?`, id).Scan(&tool, &status, &args, &result); err != nil {
			return err
		}
		a, r, changed := w.toolCall(id, tool, status, args, result)
		if !changed {
			continue
		}
		if _, err := tx.Exec(`UPDATE ai_tool_calls SET args = ?, result = ? WHERE id = ?`, a, nullStr(r), id); err != nil {
			return err
		}
		st.rewritten++
	}

	// Title.
	var title string
	if err := tx.QueryRow(`SELECT title FROM ai_conversations WHERE id = ?`, convID).Scan(&title); err == nil {
		st.scanned++
		if t, changed := w.text(title); changed {
			if _, err := tx.Exec(`UPDATE ai_conversations SET title = ? WHERE id = ?`, t, convID); err != nil {
				return err
			}
			st.rewritten++
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	return nil
}

// scanIDs drains the rows (a transaction has one connection, so nothing may run
// while they are open) and returns the ids the callback flags as needing a write.
func scanIDs(tx *sql.Tx, query, convID string, st *scrubStats, each func(func(...any) error) (string, bool, error)) ([]string, error) {
	rows, err := tx.Query(query, convID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		st.scanned++
		id, changed, err := each(rows.Scan)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if changed {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return ids, rows.Err()
}

// message computes one message's scrubbed content and parts.
func (w *convWork) message(role, content, parts string) (string, string, bool) {
	c := content
	if role == "tool" {
		for _, k := range w.corr {
			if c == k.orig && (k.callPart == "" || strings.Contains(parts, k.callPart)) {
				c = k.redacted
				break
			}
		}
	}
	if t, ok := w.text(c); ok {
		c = t
	}
	p, pch := w.json(parts)
	return c, p, c != content || pch
}

// toolCall computes one tool call's scrubbed args and result. A succeeded
// minting call of this batch first has its credential field redacted.
func (w *convWork) toolCall(id, tool, status, args string, result sql.NullString) (string, string, bool) {
	a, changed := w.json(args)
	res := result.String
	if result.Valid {
		if _, minted := w.mints[id]; minted && status == "succeeded" {
			if red, ch := onetime.Redact(res, onetime.Fields[tool]); ch {
				res, changed = red, true
			}
		}
		if r, ch := w.json(res); ch {
			res, changed = r, true
		}
	}
	return a, res, changed
}

// text scrubs a text column: decoded JSON leaves when the text is valid JSON,
// otherwise token-bounded replacement.
func (w *convWork) text(s string) (string, bool) {
	if s == "" || len(w.values) == 0 {
		return s, false
	}
	if json.Valid([]byte(s)) {
		return w.json(s)
	}
	return w.leaf(s)
}

// json scrubs a JSON column inside its decoded string leaves and re-marshals it.
// Input that is not exactly one JSON value is returned untouched.
func (w *convWork) json(s string) (string, bool) {
	if s == "" || len(w.values) == 0 || !json.Valid([]byte(s)) {
		return s, false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return s, false
	}
	doc, changed := w.walk(doc)
	if !changed {
		return s, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return s, false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}

func (w *convWork) walk(node any) (any, bool) {
	switch n := node.(type) {
	case string:
		return w.leaf(n)
	case []any:
		changed := false
		for i, el := range n {
			if v, ch := w.walk(el); ch {
				n[i], changed = v, true
			}
		}
		return n, changed
	case map[string]any:
		changed := false
		for k, el := range n {
			if v, ch := w.walk(el); ch {
				n[k], changed = v, true
			}
		}
		return n, changed
	}
	return node, false
}

// leaf replaces each value in decoded text where it stands alone: the byte
// before and after must not be in [A-Za-z0-9_-], so a longer unrelated token
// that merely contains the credential is left as it is.
func (w *convWork) leaf(s string) (string, bool) {
	changed := false
	for _, v := range w.values {
		var b strings.Builder
		i, did := 0, false
		for {
			j := strings.Index(s[i:], v)
			if j < 0 {
				break
			}
			start, end := i+j, i+j+len(v)
			if (start == 0 || !tokenByte(s[start-1])) && (end == len(s) || !tokenByte(s[end])) {
				b.WriteString(s[i:start])
				b.WriteString(onetime.Placeholder)
				i, did = end, true
				continue
			}
			b.WriteString(s[i : start+1])
			i = start + 1
		}
		if did {
			b.WriteString(s[i:])
			s, changed = b.String(), true
		}
	}
	return s, changed
}

func tokenByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func jsonString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func callIDPart(callID string) string {
	if callID == "" {
		return ""
	}
	return `"call_id":` + jsonString(callID)
}
