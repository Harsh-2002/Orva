//go:build linux

package workloadspawn_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	orvamcp "github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadhistory"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func historyLiveControlsEnabled() bool {
	return os.Getenv("ORVA_HISTORY_CONTROLS") == "disposable-vm"
}

func registerHistoryLiveControls(t *testing.T, mux *http.ServeMux, db *database.Database, dbPath string, reg *registry.Registry, async *workloadasync.Service, history *workloadhistory.Service, authenticate func(*http.Request) (*auth.Principal, error)) {
	t.Helper()
	read := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			p, err := authenticate(r)
			if err != nil || p == nil {
				http.Error(w, "authentication required", 401)
				return
			}
			if !p.Perms.Has("read") {
				http.Error(w, "read required", 403)
				return
			}
			h(w, r.WithContext(auth.WithOperator(r.Context(), p)))
		}
	}
	jobs := &handlers.JobsHandler{DB: db, Registry: reg, AsyncEnabled: true, AsyncIAM: async}
	crons := &handlers.CronHandler{DB: db, Registry: reg}
	functions := &handlers.FunctionHandler{DB: db, Registry: reg}
	mux.HandleFunc("GET /api/v1/jobs", read(jobs.List))
	mux.HandleFunc("GET /api/v1/jobs/{id}", read(jobs.Get))
	mux.HandleFunc("POST /api/v1/jobs/{id}/resume", read(jobs.Resume))
	mux.HandleFunc("GET /api/v1/cron", read(crons.ListAll))
	mux.HandleFunc("GET /api/v1/functions", read(functions.List))
	mux.Handle("/mcp", orvamcp.NewHandler(orvamcp.Deps{DB: db, Registry: reg, History: history, AsyncEnabled: true, AsyncIAM: async, Version: "history-live-fixture"}))
	mux.Handle("/web/", http.StripPrefix("/web/", http.FileServer(http.Dir(os.Getenv("ORVA_HISTORY_UI_DIR")))))
	mux.HandleFunc("POST /api/scratch/revoke-ui", read(func(w http.ResponseWriter, r *http.Request) {
		ref, ok := auth.OperatorFromContext(r.Context())
		if !ok || !db.CanManageAsync(r.Context(), ref) {
			http.Error(w, "admin required", 403)
			return
		}
		rows, err := db.ListAPIKeys()
		if err != nil {
			http.Error(w, "fixture unavailable", 500)
			return
		}
		for _, k := range rows {
			if k.Name == "history-live-ui" {
				if err := db.DeleteAPIKey(k.ID); err != nil {
					http.Error(w, "fixture revoke failed", 500)
					return
				}
			}
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/scratch/capacity/{state}", read(func(w http.ResponseWriter, r *http.Request) {
		ref, ok := auth.OperatorFromContext(r.Context())
		if !ok || !db.CanManageAsync(r.Context(), ref) {
			http.Error(w, "admin required", 403)
			return
		}
		conn, err := sql.Open("sqlite", dbPath+"?"+historyStagePragmas)
		if err != nil {
			http.Error(w, "fixture unavailable", 500)
			return
		}
		defer conn.Close()
		switch r.PathValue("state") {
		case "block":
			_, err = conn.ExecContext(r.Context(), "UPDATE work_history_capacity SET bytes_used=? WHERE id=1", database.HistoryMaxBytes)
		case "reset":
			_, err = conn.ExecContext(r.Context(), `UPDATE work_history_capacity SET bytes_used=(SELECT COALESCE(SUM(metadata_bytes),0) FROM work_history) WHERE id=1`)
		default:
			http.Error(w, "invalid fixture state", 400)
			return
		}
		if err != nil {
			http.Error(w, "fixture capacity update failed", 500)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/scratch/effect/{kind}/{id}", read(func(w http.ResponseWriter, r *http.Request) {
		kind, id := r.PathValue("kind"), r.PathValue("id")
		if kind != "job" && kind != "cron" {
			http.Error(w, "invalid kind", 400)
			return
		}
		owner := r.URL.Query().Get("target")
		v, err := db.KVGet(owner, "completed/"+kind)
		var value struct{ ID string }
		match := err == nil && json.Unmarshal(v.Value, &value) == nil && value.ID == id
		_ = json.NewEncoder(w).Encode(map[string]bool{"sdk_effect_matches": match})
	}))
}

func liveHistoryControls(t *testing.T, db *database.Database, dbPath string, server *httptest.Server, async *workloadasync.Service, ref auth.OperatorReference, admin, lang, oldID, target, jobID, cronID, activeCron string) {
	t.Helper()
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: ref.Kind, ID: ref.ID})
	c, err := db.GetAsyncCron(t.Context(), activeCron)
	liveMust(t, err)
	liveMust(t, async.MutateCron(ctx, ref, c, "disable"))
	keys := map[string]string{"admin": admin}
	for role, permissions := range map[string]string{"reader": `["read"]`, "writeonly": `["write"]`, "revoked": `["read","admin"]`, "ui": `["read","admin"]`} {
		key := "orva_" + strings.ReplaceAll(ids.New(), "-", "")
		sum := sha256.Sum256([]byte(key))
		liveMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "history-live-" + role, KeyHash: hex.EncodeToString(sum[:]), Prefix: "fixture", Permissions: permissions}))
		keys[role] = key
	}
	httpCall := func(role, path string, raw []byte, want int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+path, bytes.NewReader(raw))
		liveMust(t, err)
		req.Header.Set("Authorization", "Bearer "+keys[role])
		req.Header.Set("Content-Type", "application/json")
		res, err := server.Client().Do(req)
		liveMust(t, err)
		defer res.Body.Close()
		out, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		liveMust(t, err)
		if res.StatusCode != want {
			t.Fatalf("actual HTTP %s role=%s status=%d wanted=%d", path, role, res.StatusCode, want)
		}
		return out
	}
	cli := func(role, op string, raw []byte, denied bool) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), os.Getenv("ORVA_HISTORY_CLI"), "history", op, "--request", "-", "--quiet", "--yes")
		cmd.Env = []string{"HOME=" + t.TempDir(), "ORVA_ENDPOINT=" + server.URL, "ORVA_API_KEY=" + keys[role]}
		cmd.Stdin = bytes.NewReader(raw)
		out, err := cmd.Output()
		if (err != nil) != denied {
			t.Fatalf("actual CLI %s role=%s denied=%v wanted=%v", op, role, err != nil, denied)
		}
		return out
	}
	mcp := func(role, op string, raw []byte, denied bool) []byte {
		t.Helper()
		name := map[string]string{"list": "list_work_history", "get": "get_work_history", "capacity": "get_work_history_capacity", "reissue": "reissue_work_history", "purge": "purge_work_history"}[op]
		params := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":{"request":` + string(raw) + `},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"history-live","version":"1"}}}}`
		req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/mcp", strings.NewReader(params))
		liveMust(t, err)
		req.Header.Set("Authorization", "Bearer "+keys[role])
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", name)
		res, err := server.Client().Do(req)
		liveMust(t, err)
		defer res.Body.Close()
		out, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		liveMust(t, err)
		if res.StatusCode != 200 {
			if !denied {
				t.Fatalf("actual MCP %s role=%s HTTP %d body=%.200s", op, role, res.StatusCode, out)
			}
			var failure struct { Error struct { Code int `json:"code"`; Message string `json:"message"` } `json:"error"` }
			if res.StatusCode != 400 || (role != "reader" && role != "writeonly") || (op != "purge" && op != "reissue") || json.Unmarshal(out, &failure) != nil || failure.Error.Code != -32602 || failure.Error.Message != `unknown tool "`+name+`"` {
				t.Fatal("QA: unexpected non-200 MCP denial")
			}
			return nil
		}
		if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "data: ") {
					out = []byte(strings.TrimPrefix(line, "data: "))
					break
				}
			}
		}
		var reply struct {
			Error  json.RawMessage `json:"error"`
			Result struct {
				IsError bool            `json:"isError"`
				Data    json.RawMessage `json:"structuredContent"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		liveMust(t, json.Unmarshal(out, &reply))
		bad := len(reply.Error) != 0 || reply.Result.IsError || res.StatusCode != 200
		if bad != denied {
			t.Fatalf("actual MCP %s role=%s denied=%v wanted=%v", op, role, bad, denied)
		}
		if len(reply.Result.Data) == 0 && len(reply.Result.Content) > 0 {
			return []byte(reply.Result.Content[0].Text)
		}
		return reply.Result.Data
	}
	selector, _ := json.Marshal(database.HistorySelector{Kind: "job", ID: jobID})
	reissue, _ := json.Marshal(database.HistoryReissue{Source: database.HistorySelector{Kind: "job", ID: jobID}, TargetID: target, Payload: json.RawMessage(`{"tag":"live-transport"}`), ScheduledAt: time.Now().UTC(), MaxAttempts: 1, Reviewed: true, Confirm: true})
	purge, _ := json.Marshal(map[string]any{"selected": []database.HistorySelector{{Kind: "job", ID: jobID}}, "confirm": true})
	inspect := httpCall("admin", "/api/v1/work-history/get", selector, 200)
	if bytes.Contains(inspect, []byte("old-private")) || !bytes.Contains(inspect, []byte(oldID)) {
		t.Fatal("actual archive inspection leaked old payload or lost target identity")
	}
	for _, op := range []string{"list", "get", "capacity"} {
		raw := []byte(`{"limit":100}`)
		if op == "get" {
			raw = selector
		}
		if op == "capacity" {
			raw = []byte(`{}`)
		}
		for _, out := range [][]byte{cli("reader", op, raw, false), mcp("reader", op, raw, false)} {
			if bytes.Contains(out, []byte("old-private")) || (op != "capacity" && !bytes.Contains(out, []byte(jobID))) ||
				(op == "capacity" && !bytes.Contains(out, []byte(`"100000"`))) {
				t.Fatal("transport inspected different archive/capacity or exposed old payload")
			}
		}
	}
	httpCall("writeonly", "/api/v1/work-history/get", selector, 403)
	httpCall("admin", "/api/v1/jobs/"+jobID+"/resume", nil, 503)
	for _, role := range []string{"reader", "writeonly"} {
		httpCall(role, "/api/v1/work-history/reissue", reissue, 403)
		httpCall(role, "/api/v1/work-history/purge", purge, 403)
		cli(role, "reissue", reissue, true)
		cli(role, "purge", purge, true)
		mcp(role, "reissue", reissue, true)
		mcp(role, "purge", purge, true)
	}
	for _, raw := range [][]byte{[]byte(`{"limit":1,"limit":2}`), []byte(`{"limit":1,"actor_id":"author"}`), []byte(`{"payload":{"a":1,"a":2}}`)} {
		httpCall("admin", "/api/v1/work-history/list", raw, 400)
		cli("admin", "list", raw, true)
		mcp("admin", "list", raw, true)
	}
	for _, raw := range [][]byte{
		[]byte(strings.Replace(string(reissue), `{"tag":"live-transport"}`, `{"tag":1,"tag":2}`, 1)),
		[]byte(strings.TrimSuffix(string(reissue), "}") + `,"operator_id":"author"}`),
	} {
		httpCall("admin", "/api/v1/work-history/reissue", raw, 400)
		cli("admin", "reissue", raw, true)
		mcp("admin", "reissue", raw, true)
	}
	conn, err := sql.Open("sqlite", dbPath+"?"+historyStagePragmas)
	liveMust(t, err)
	defer conn.Close()
	var before, after, auditBefore, auditAfter int
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&before))
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT count(*) FROM work_history WHERE kind='reissue'").Scan(&auditBefore))
	_, err = conn.ExecContext(t.Context(), `CREATE TRIGGER history_live_revoke AFTER INSERT ON jobs BEGIN DELETE FROM api_keys WHERE name='history-live-revoked'; END`)
	liveMust(t, err)
	httpCall("revoked", "/api/v1/work-history/reissue", reissue, 403)
	_, err = conn.ExecContext(t.Context(), "DROP TRIGGER history_live_revoke")
	liveMust(t, err)
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&after))
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT count(*) FROM work_history WHERE kind='reissue'").Scan(&auditAfter))
	if before != after || auditBefore != auditAfter {
		t.Fatal("commit-time credential refusal created work")
	}
	for _, transport := range []string{"cli", "mcp"} {
		var out []byte
		if transport == "cli" {
			out = cli("admin", "reissue", reissue, false)
		} else {
			out = mcp("admin", "reissue", reissue, false)
		}
		var reply struct {
			NewID string `json:"new_id"`
		}
		liveMust(t, json.Unmarshal(out, &reply))
		if reply.NewID == "" || reply.NewID == jobID {
			t.Fatal("transport adopted archive ID")
		}
		j, err := db.GetAsyncJob(t.Context(), reply.NewID)
		liveMust(t, err)
		if j.OriginKind != "operator" || j.OperatorID != ref.ID || j.CreatorID != "" || j.FunctionID != target {
			t.Fatal("transport reissue provenance mismatch")
		}
		waitHistorySDK(t, db, target, "job", reply.NewID)
	}
	// QA-only explicit eligibility-time injection before browser; original job/cron stay ineligible.
	var qaPurgeID string
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT original_id FROM work_history WHERE kind='delete' AND target_id=?", oldID).Scan(&qaPurgeID))
	_, err = conn.ExecContext(t.Context(), "UPDATE work_history SET eligible_at=? WHERE kind='delete' AND original_id=?", time.Now().UTC().Add(-time.Hour), qaPurgeID)
	liveMust(t, err)
	private := filepath.Join(os.Getenv("ORVA_HISTORY_PRIVATE"), lang)
	liveMust(t, os.MkdirAll(private, 0700))
	raw, err := json.Marshal(map[string]any{"url": server.URL, "keys": keys, "language": lang, "old_target": oldID, "target": target, "job": jobID, "cron": cronID, "qa_purge_id": qaPurgeID})
	liveMust(t, err)
	liveMust(t, os.WriteFile(filepath.Join(private, "ready.json"), raw, 0600))
	deadline := time.NewTimer(7 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
waiting:
	for {
		select {
		case <-t.Context().Done():
			t.Fatal("live history controls cancelled")
		case <-deadline.C:
			t.Fatal("live history browser did not complete")
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(private, "done")); err == nil {
				break waiting
			}
		}
	}
	rawUI, err := os.ReadFile(filepath.Join(private, "ui-results.json"))
	liveMust(t, err)
	var ui struct {
		JobNewID  string `json:"job_new_id"`
		CronNewID string `json:"cron_new_id"`
		PurgeID string `json:"purge_id"`
	}
	liveMust(t, json.Unmarshal(rawUI, &ui))
	if ui.JobNewID == "" || ui.CronNewID == "" || ui.JobNewID == jobID || ui.CronNewID == cronID {
		t.Fatal("real browser did not report both fresh UI-created work IDs")
	}
	uiJob, err := db.GetAsyncJob(t.Context(), ui.JobNewID)
	liveMust(t, err)
	if uiJob.OriginKind != "operator" || uiJob.OperatorID != ref.ID || uiJob.CreatorID != "" || uiJob.FunctionID != target || strings.Contains(string(uiJob.Payload), "old-private") {
		t.Fatal("UI job provenance/target/payload mismatch")
	}
	uiCron, err := db.GetAsyncCron(t.Context(), ui.CronNewID)
	liveMust(t, err)
	if uiCron.OriginKind != "operator" || uiCron.OperatorID != ref.ID || uiCron.CreatorID != "" || uiCron.FunctionID != target || strings.Contains(uiCron.Payload, "old-private") {
		t.Fatal("UI cron provenance/target/payload mismatch")
	}
	for _, id := range []string{ui.JobNewID, ui.CronNewID} {
		if _, err := db.GetWorkHistory(t.Context(), ref, database.HistorySelector{Kind: "reissue", ID: id}); err != nil {
			t.Fatal("UI reissue audit missing", id)
		}
	}
	if ui.PurgeID != qaPurgeID { t.Fatal("QA: browser did not confirm the eligible exact root") }
	var remain, audited int
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT count(*) FROM work_history WHERE kind='delete' AND original_id=?", qaPurgeID).Scan(&remain))
	liveMust(t, conn.QueryRowContext(t.Context(), "SELECT count(*) FROM work_history WHERE kind='purge' AND json_extract(metadata,'$.selected[0].kind')='delete' AND json_extract(metadata,'$.selected[0].id')=? AND json_extract(metadata,'$.actor.ID')=?", qaPurgeID, ref.ID).Scan(&audited))
	if remain != 0 || audited != 1 { t.Fatal("QA: real UI purge did not remove archive with one actor audit") }
	var rowsUsed, bytesUsed, rowsReserved, bytesReserved, actualRows, actualBytes, actualReservedRows, actualReservedBytes int64
	liveMust(t, conn.QueryRowContext(t.Context(), `SELECT rows_used,bytes_used,rows_reserved,bytes_reserved,
 (SELECT count(*) FROM work_history),(SELECT COALESCE(sum(length(CAST(metadata AS BLOB))),0) FROM work_history),
 (SELECT count(*) FROM work_history WHERE reserved=1),(SELECT COALESCE(sum(metadata_bytes),0) FROM work_history WHERE reserved=1)
 FROM work_history_capacity WHERE id=1`).Scan(&rowsUsed,&bytesUsed,&rowsReserved,&bytesReserved,&actualRows,&actualBytes,&actualReservedRows,&actualReservedBytes))
	if rowsUsed!=actualRows || bytesUsed!=actualBytes || rowsReserved!=actualReservedRows || bytesReserved!=actualReservedBytes { t.Fatal("QA: UI purge ledger mismatch") }
	t.Log("QA: real UI eligible-root purge confirmed; selected archive absent, one acting-admin audit and exact canonical/reserved ledger reconciliation")
	uiCron, err = db.GetAsyncCron(t.Context(), ui.CronNewID)
	liveMust(t, err)
	liveMust(t, async.MutateCron(ctx, ref, uiCron, "disable"))
	// Explicit fixture-time eligibility injection, not a 90-day wall-clock proof.
	_, err = conn.ExecContext(t.Context(), `UPDATE work_history SET eligible_at=? WHERE kind IN ('job','cron')`, time.Now().UTC().Add(-time.Hour))
	liveMust(t, err)
	cli("admin", "purge", purge, false)
	cronPurge, _ := json.Marshal(map[string]any{"selected": []database.HistorySelector{{Kind: "cron", ID: cronID}}, "confirm": true})
	mcp("admin", "purge", cronPurge, false)
	if _, err := db.GetWorkHistory(t.Context(), ref, database.HistorySelector{Kind: "job", ID: jobID}); err == nil {
		t.Fatal("actual CLI purge retained selected job")
	}
	if _, err := db.GetWorkHistory(t.Context(), ref, database.HistorySelector{Kind: "cron", ID: cronID}); err == nil {
		t.Fatal("actual external MCP purge retained selected cron")
	}
	t.Log("real original archive -> actual CLI/MCP reissue and SDK effects -> browser controls completed -> explicit aged-fixture CLI/MCP purge")
}

func waitHistorySDK(t *testing.T, db *database.Database, target, kind, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		v, err := db.KVGet(target, "completed/"+kind)
		var value struct{ ID string }
		if err == nil && json.Unmarshal(v.Value, &value) == nil && value.ID == id {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("actual new operator work did not produce its SDK effect")
}
