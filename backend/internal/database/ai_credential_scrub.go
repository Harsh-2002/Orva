package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/onetime"
)

// Hard per-boot bounds. Rows READ counts every row a query returns (minting
// rows including ones filtered out in Go, conversation rows scanned, and each
// point re-read before a write); rows REWRITTEN counts UPDATEs of chat rows. The
// bounds are checked before every read and write, so a boot never exceeds them.
// They are floored (see newScrubBudget) so that a boot always makes progress.
// Variables so tests can force small budgets.
var (
	chatScrubBatchRows    = 50    // minting rows per batch (at most)
	chatScrubChunkRows    = 100   // conversation rows per read
	chatScrubMaxScanned   = 10000 // rows read per boot
	chatScrubMaxRewritten = 10000 // rows written per boot

	// chatScrubInjectErr, when set, is called after each row iteration with a
	// stage name; a non-nil result is treated like an iteration error. Test seam.
	chatScrubInjectErr func(stage string) error
)

const (
	chatScrubMarkerKey = "chat_credential_scrub_done"
	chatScrubCursorKey = "chat_credential_scrub_cursor"
	chatScrubPosKey    = "chat_credential_scrub_pos"

	// Copies are located by value; refuse values short enough to match prose.
	chatScrubMinValueLen = 16

	// Smallest budgets that still let one batch (up to 3 minting reads) and a
	// useful slice of a conversation fit, so every boot advances.
	chatScrubMinReads  = 16
	chatScrubMinWrites = 1
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
//     parse are left alone. A failed call supplies no value and is never
//     replaced wholesale, but an exact copy of a value inside it is scrubbed.
//
// A conversation is processed in phases (messages, tool calls, title, then the
// minting rows themselves), each row re-read immediately before it is written.
// The minting rows are the only record of the credential values (nothing else
// persists them), so their credential fields are left readable until everything
// else is done and are then rewritten together in one step that is never split:
// if the budget cannot cover them all the pass stops before touching any, and the
// next boot resumes at that phase and always finishes it. That is the one place
// the per-boot bound can be exceeded, by at most one batch's minting rows. Apart
// from it the pass is hard-bounded per boot on rows read and rewritten; at a
// bound it commits what is done and persists the batch's minting cursor plus a
// per-conversation position (phase and rowid), so the next boot continues there.
// The marker is set only after the walk completes. Nothing is dropped.
func (db *Database) scrubChatCredentials() scrubStats {
	var total scrubStats
	if db.GetSystemConfigString(chatScrubMarkerKey, "") == "true" {
		return total
	}
	cursor := int64(0)
	if v := db.GetSystemConfigString(chatScrubCursorKey, ""); v != "" {
		if n, ok := parseCursor(v); ok {
			cursor = n
		}
	}
	var pos *scrubPos
	if v := db.GetSystemConfigString(chatScrubPosKey, ""); v != "" {
		var p scrubPos
		if json.Unmarshal([]byte(v), &p) == nil && p.Conv != "" {
			pos = &p
		}
	}
	b := newScrubBudget()
	for {
		// A new batch needs room for its minting-row read plus real work.
		if (b.readsLeft() < 4 || !b.canWrite()) && b.reads > 0 {
			break
		}
		res, err := db.scrubChatBatch(cursor, pos, b, &total)
		if err != nil {
			slog.Warn("chat credential scrub failed", "err", err)
			return total
		}
		cursor, pos = res.cursor, res.pos
		if res.finished {
			if total.rewritten > 0 {
				slog.Info("scrubbed stored Chat credentials", "minting_calls", total.minted, "rows_rewritten", total.rewritten)
			}
			if err := db.SetSystemConfig(chatScrubMarkerKey, "true"); err != nil {
				slog.Warn("chat credential scrub marker write failed", "err", err)
			}
			return total
		}
		if res.stopped {
			break
		}
	}
	slog.Info("Chat credential scrub continues next boot", "rows_read", total.scanned, "rows_rewritten", total.rewritten)
	return total
}

func parseCursor(v string) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= 0
}

type scrubStats struct{ scanned, rewritten, minted int }

type scrubBudget struct{ reads, writes, maxReads, maxWrites int }

func newScrubBudget() *scrubBudget {
	return &scrubBudget{maxReads: max(chatScrubMaxScanned, chatScrubMinReads), maxWrites: max(chatScrubMaxRewritten, chatScrubMinWrites)}
}
func (b *scrubBudget) readsLeft() int { return b.maxReads - b.reads }
func (b *scrubBudget) canWrite() bool { return b.writes < b.maxWrites }

// chunkRows is how many conversation rows to read next. Every candidate costs a
// point re-read and a write on top of its scan read, so reading more rows than
// half the reads left, or than the writes left, would only be rescanned next
// boot (or leave no budget to write what was found).
func (b *scrubBudget) chunkRows() int {
	return min(chatScrubChunkRows, b.readsLeft()/2, b.maxWrites-b.writes)
}

// scrubPos is where a stopped batch resumes. Hi bounds the batch's minting rows
// so the same set is rebuilt; Conv/Phase/Seq/Rowid locate the work inside it.
type scrubPos struct {
	Hi    int64  `json:"hi"`
	Conv  string `json:"conv"`
	Phase string `json:"phase"` // m messages, t tool calls, title, f minting rows
	Seq   int64  `json:"seq"`
	Rowid int64  `json:"rowid"`
}

const (
	phaseMessages = "m"
	phaseTools    = "t"
	phaseTitle    = "title"
	phaseFinal    = "f"
)

type mintRow struct {
	rowid                                    int64
	id, conversationID, tool, callID, status string
	result                                   sql.NullString
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

type batchResult struct {
	cursor   int64
	pos      *scrubPos
	finished bool // no minting rows remain
	stopped  bool // a bound was reached; resume next boot
}

// rowsDone closes rows and reports any iteration error, including an injected one.
func rowsDone(rows *sql.Rows, stage string) error {
	closeErr := rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if chatScrubInjectErr != nil {
		return chatScrubInjectErr(stage)
	}
	return nil
}

func (db *Database) scrubChatBatch(cursor int64, pos *scrubPos, b *scrubBudget, st *scrubStats) (batchResult, error) {
	tx, err := db.write.Begin()
	if err != nil {
		return batchResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var rows []mintRow
	hi := int64(math.MaxInt64)
	limit := min(chatScrubBatchRows, max(1, b.readsLeft()/12))
	if pos != nil {
		hi, limit = pos.Hi, chatScrubBatchRows
	}
	rows, err = fetchMintRows(tx, cursor, hi, limit, b, st)
	if err != nil {
		return batchResult{}, err
	}
	if len(rows) == 0 {
		if pos != nil { // the interrupted batch's rows are gone; start afresh
			if err := setScrubPos(tx, nil); err != nil {
				return batchResult{}, err
			}
			return batchResult{cursor: cursor}, tx.Commit()
		}
		return batchResult{cursor: cursor, finished: true}, nil
	}
	next := cursor
	work := map[string]*convWork{}
	for _, r := range rows {
		next = max(next, r.rowid)
		// Only a succeeded call is a source of values; failed ones are never
		// redacted structurally (copies of other values in them are still scrubbed
		// by the conversation pass).
		if r.status != "succeeded" || !r.result.Valid || r.result.String == "" {
			continue
		}
		paths := onetime.Fields[r.tool]
		redacted, changed := onetime.Redact(r.result.String, paths)
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
		w.corr = append(w.corr, resultCorrelation{orig: r.result.String, redacted: redacted, callPart: callIDPart(r.callID)})
		if values, ok := onetime.Values(r.result.String, paths); ok {
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
	batchHi := next
	if pos != nil {
		batchHi = pos.Hi
	}
	for _, c := range convs {
		w := work[c]
		w.values = uniqueLongestFirst(w.values)
		start := scrubPos{Phase: phaseMessages, Seq: math.MinInt64, Rowid: math.MinInt64}
		if pos != nil && pos.Conv == c {
			start = *pos
		}
		stop, err := w.scrubConversation(tx, c, start, b, st)
		if err != nil {
			return batchResult{}, err
		}
		if stop != nil {
			stop.Hi, stop.Conv = batchHi, c
			if err := setScrubPos(tx, stop); err != nil {
				return batchResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return batchResult{}, err
			}
			return batchResult{cursor: cursor, pos: stop, stopped: true}, nil
		}
	}
	if err := setScrubPos(tx, nil); err != nil {
		return batchResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO system_config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		chatScrubCursorKey, strconv.FormatInt(next, 10)); err != nil {
		return batchResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return batchResult{}, err
	}
	return batchResult{cursor: next}, nil
}

func setScrubPos(tx *sql.Tx, p *scrubPos) error {
	v := ""
	if p != nil {
		raw, _ := json.Marshal(p)
		v = string(raw)
	}
	_, err := tx.Exec(`INSERT INTO system_config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, chatScrubPosKey, v)
	return err
}

// fetchMintRows returns up to limit minting rows in (cursor, hi] in rowid order,
// one indexed range per tool name (idx_ai_tc_tool). Every row returned counts as
// read, whatever its status: the filter is applied in Go, not hidden in SQL.
func fetchMintRows(tx *sql.Tx, cursor, hi int64, limit int, b *scrubBudget, st *scrubStats) ([]mintRow, error) {
	names := make([]string, 0, len(onetime.Fields))
	for n := range onetime.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	var all []mintRow
	for _, name := range names {
		rows, err := tx.Query(`SELECT rowid, id, conversation_id, tool_name, COALESCE(call_id,''), status, result
			FROM ai_tool_calls WHERE tool_name = ? AND rowid > ? AND rowid <= ? ORDER BY rowid LIMIT ?`, name, cursor, hi, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r mintRow
			if err := rows.Scan(&r.rowid, &r.id, &r.conversationID, &r.tool, &r.callID, &r.status, &r.result); err != nil {
				_ = rows.Close()
				return nil, err
			}
			all = append(all, r)
			b.reads++
			st.scanned++
		}
		if err := rowsDone(rows, "mint"); err != nil {
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

type rowKey struct{ seq, rowid int64 }

// scrubConversation applies the batch's values to one conversation, phase by
// phase from the given position. It returns a non-nil position when a bound was
// reached first; everything before that position is complete.
func (w *convWork) scrubConversation(tx *sql.Tx, conv string, start scrubPos, b *scrubBudget, st *scrubStats) (*scrubPos, error) {
	phase := start.Phase
	if phase == phaseMessages {
		stop, err := w.messagesPhase(tx, conv, rowKey{start.Seq, start.Rowid}, b, st)
		if err != nil || stop != nil {
			return stop, err
		}
		phase, start.Rowid = phaseTools, math.MinInt64
	}
	if phase == phaseTools {
		stop, err := w.toolsPhase(tx, conv, start.Rowid, b, st)
		if err != nil || stop != nil {
			return stop, err
		}
		phase = phaseTitle
	}
	if phase == phaseTitle {
		if b.readsLeft() < 1 || !b.canWrite() {
			return &scrubPos{Phase: phaseTitle}, nil
		}
		var title string
		err := tx.QueryRow(`SELECT title FROM ai_conversations WHERE id = ?`, conv).Scan(&title)
		b.reads++
		st.scanned++
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			if t, changed := w.text(title); changed {
				if _, err := tx.Exec(`UPDATE ai_conversations SET title = ? WHERE id = ?`, t, conv); err != nil {
					return nil, err
				}
				b.writes++
				st.rewritten++
			}
		}
	}
	// The minting rows hold the only copy of the credential values, so they are
	// rewritten together, never split across boots: if the budget cannot cover all
	// of them, stop before touching any (every credential field stays readable) and
	// resume at this phase, which then always finishes. That resumed boot is the one
	// place a bound can be exceeded, by at most this batch's minting rows.
	forced := start.Phase == phaseFinal
	k := len(w.mints)
	if !forced && (b.readsLeft() < k || b.maxWrites-b.writes < k) {
		return &scrubPos{Phase: phaseFinal}, nil
	}
	return nil, w.finalPhase(tx, b, st)
}

type messageCand struct {
	id            string
	key, previous rowKey
}

func (w *convWork) messagesPhase(tx *sql.Tx, conv string, after rowKey, b *scrubBudget, st *scrubStats) (*scrubPos, error) {
	for {
		n := b.chunkRows()
		if n < 1 {
			return &scrubPos{Phase: phaseMessages, Seq: after.seq, Rowid: after.rowid}, nil
		}
		rows, err := tx.Query(`SELECT rowid, seq, id, role, content, parts FROM ai_messages
			WHERE conversation_id = ? AND (seq, rowid) > (?, ?) ORDER BY seq, rowid LIMIT ?`, conv, after.seq, after.rowid, n)
		if err != nil {
			return nil, err
		}
		var cands []messageCand
		prev, got := after, 0
		for rows.Next() {
			var k rowKey
			var id, role, content, parts string
			if err := rows.Scan(&k.rowid, &k.seq, &id, &role, &content, &parts); err != nil {
				_ = rows.Close()
				return nil, err
			}
			got++
			b.reads++
			st.scanned++
			if _, _, changed := w.message(role, content, parts); changed {
				cands = append(cands, messageCand{id: id, key: k, previous: prev})
			}
			prev = k
		}
		if err := rowsDone(rows, "messages"); err != nil {
			return nil, err
		}
		for _, c := range cands {
			if b.readsLeft() < 1 || !b.canWrite() {
				return &scrubPos{Phase: phaseMessages, Seq: c.previous.seq, Rowid: c.previous.rowid}, nil
			}
			var role, content, parts string
			err := tx.QueryRow(`SELECT role, content, parts FROM ai_messages WHERE id = ?`, c.id).Scan(&role, &content, &parts)
			b.reads++
			st.scanned++
			if err != nil {
				return nil, err
			}
			cn, p, changed := w.message(role, content, parts)
			if !changed {
				continue
			}
			if _, err := tx.Exec(`UPDATE ai_messages SET content = ?, parts = ? WHERE id = ?`, cn, p, c.id); err != nil {
				return nil, err
			}
			b.writes++
			st.rewritten++
		}
		after = prev
		if got < n {
			return nil, nil
		}
	}
}

type toolCand struct {
	id       string
	rowid    int64
	previous int64
}

func (w *convWork) toolsPhase(tx *sql.Tx, conv string, after int64, b *scrubBudget, st *scrubStats) (*scrubPos, error) {
	for {
		n := b.chunkRows()
		if n < 1 {
			return &scrubPos{Phase: phaseTools, Rowid: after}, nil
		}
		rows, err := tx.Query(`SELECT rowid, id, tool_name, status, args, result FROM ai_tool_calls
			WHERE conversation_id = ? AND rowid > ? ORDER BY rowid LIMIT ?`, conv, after, n)
		if err != nil {
			return nil, err
		}
		var cands []toolCand
		prev, got := after, 0
		for rows.Next() {
			var rowid int64
			var id, tool, status, args string
			var result sql.NullString
			if err := rows.Scan(&rowid, &id, &tool, &status, &args, &result); err != nil {
				_ = rows.Close()
				return nil, err
			}
			got++
			b.reads++
			st.scanned++
			// The batch's minting rows keep their credential fields until the
			// final phase, so an interrupted pass can still extract their values.
			if _, own := w.mints[id]; !own {
				if _, _, changed := w.toolCall(id, tool, status, args, result); changed {
					cands = append(cands, toolCand{id: id, rowid: rowid, previous: prev})
				}
			}
			prev = rowid
		}
		if err := rowsDone(rows, "tools"); err != nil {
			return nil, err
		}
		for _, c := range cands {
			if b.readsLeft() < 1 || !b.canWrite() {
				return &scrubPos{Phase: phaseTools, Rowid: c.previous}, nil
			}
			if err := w.rewriteToolCall(tx, c.id, b, st); err != nil {
				return nil, err
			}
		}
		after = prev
		if got < n {
			return nil, nil
		}
	}
}

// finalPhase rewrites every minting row of the batch in this conversation, each
// re-read fresh and carrying the full union of the batch's values, in one step.
func (w *convWork) finalPhase(tx *sql.Tx, b *scrubBudget, st *scrubStats) error {
	mints := make([]mintRow, 0, len(w.mints))
	for _, m := range w.mints {
		mints = append(mints, m)
	}
	sort.Slice(mints, func(i, j int) bool { return mints[i].rowid < mints[j].rowid })
	for _, m := range mints {
		if err := w.rewriteToolCall(tx, m.id, b, st); err != nil {
			return err
		}
	}
	return nil
}

// rewriteToolCall re-reads one tool call by id and writes the scrubbed copy.
func (w *convWork) rewriteToolCall(tx *sql.Tx, id string, b *scrubBudget, st *scrubStats) error {
	var tool, status, args string
	var result sql.NullString
	err := tx.QueryRow(`SELECT tool_name, status, args, result FROM ai_tool_calls WHERE id = ?`, id).Scan(&tool, &status, &args, &result)
	b.reads++
	st.scanned++
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	a, r, changed := w.toolCall(id, tool, status, args, result)
	if !changed {
		return nil
	}
	if _, err := tx.Exec(`UPDATE ai_tool_calls SET args = ?, result = ? WHERE id = ?`, a, nullStr(r), id); err != nil {
		return err
	}
	b.writes++
	st.rewritten++
	return nil
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
