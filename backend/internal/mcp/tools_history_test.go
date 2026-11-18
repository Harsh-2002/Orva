package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadhistory"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type historyTransportFixture struct {
	db                              *database.Database
	sql                             *sql.DB
	service                         *workloadhistory.Service
	registry                        *registry.Registry
	agent                           *Registry
	server                          *httptest.Server
	adminID, readerID, oldID, fresh string
	jobID, cronID                   string
	admin, reader                   string
}

func historyTransportSetup(t *testing.T) *historyTransportFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, init := range []func(context.Context) error{db.InitAsyncSchemaForFixture, db.InitCronSchemaForFixture, db.InitHistorySchemaForFixture} {
		if err = init(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	f := &historyTransportFixture{db: db, sql: conn, adminID: ids.New(), readerID: ids.New(), oldID: ids.New(), fresh: ids.New(),
		admin: "orva_history_test_admin", reader: "orva_history_test_reader"}
	seedStatelessAPIKey(t, db, f.admin, f.adminID, "history-admin", `["read","admin"]`)
	seedStatelessAPIKey(t, db, f.reader, f.readerID, "history-reader", `["read"]`)
	f.registry = registry.New(db)
	var evaluators []*workloadiam.Evaluator
	for i, id := range []string{f.oldID, f.fresh} {
		if err = f.registry.Set(&database.Function{ID: id, Name: fmt.Sprintf("history-target-%d", i), Runtime: "node", Entrypoint: "handler.js", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "fixture-artifact"},
			workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}})
		if err != nil {
			t.Fatal(err)
		}
		evaluators = append(evaluators, e)
	}
	authority, err := workloadiam.NewAuthority(32, evaluators)
	if err != nil {
		t.Fatal(err)
	}
	f.service = &workloadhistory.Service{DB: db, Authority: authority, CurrentArtifact: func(string) string { return "fixture-artifact" }}
	f.registry.DeletionHook = f.service
	async := &workloadasync.Service{DB: db, Authority: authority, CurrentArtifact: f.service.CurrentArtifact}
	ref := auth.OperatorReference{Kind: "api_key", ID: f.adminID}
	job := &database.AsyncJob{Job: database.Job{FunctionID: f.oldID, Payload: []byte(`{"private":"unavailable-old-body"}`), MaxAttempts: 2},
		AsyncProvenance: database.AsyncProvenance{OriginKind: "operator", OperatorKind: "api_key", OperatorID: f.adminID}}
	if err = async.EnqueueOperator(t.Context(), ref, job); err != nil {
		t.Fatal(err)
	}
	f.jobID = job.ID
	cron := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: f.oldID, Name: "old-schedule", CronExpr: "@every 1h", Timezone: "UTC", Payload: `{"private":"unavailable-old-body"}`},
		AsyncProvenance: database.AsyncProvenance{OriginKind: "operator", OperatorKind: "api_key", OperatorID: f.adminID}}
	if err = async.CreateCronOperator(t.Context(), ref, cron); err != nil {
		t.Fatal(err)
	}
	f.cronID = cron.ID
	if err = f.registry.Delete(f.ctx(t, f.adminID), f.oldID); err != nil {
		t.Fatal(err)
	}
	deps := Deps{DB: db, Registry: f.registry, History: f.service}
	f.agent = BuildAgentRegistry(deps, permSet{permRead: true, permAdmin: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", NewHandler(deps))
	(&workloadhistory.HTTPAdapter{Service: f.service, Authenticate: func(r *http.Request) (*auth.Principal, error) {
		p, ok := authenticateRequest(db, r)
		if !ok {
			return nil, database.ErrOperatorDenied
		}
		return p, nil
	}}).Register(mux)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *historyTransportFixture) ctx(t *testing.T, id string) context.Context {
	t.Helper()
	return auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: id})
}

func (f *historyTransportFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.sql.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *historyTransportFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.sql.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func historyEnvelope(raw []byte) []byte {
	return append(append([]byte(`{"request":`), raw...), '}')
}

// Arguments are spliced unchanged, not marshaled through a lossy JSON value.
func (f *historyTransportFixture) rpc(t *testing.T, key, method, name string, args []byte) (int, rpcResponse, []byte) {
	t.Helper()
	meta, err := json.Marshal(newProtocolMeta(true))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":` + fmt.Sprintf("%q", method) + `,"params":{"_meta":` + string(meta))
	if method == "tools/call" {
		body = append(body, []byte(`,"name":`+fmt.Sprintf("%q", name)+`,"arguments":`)...)
		body = append(body, args...)
	}
	body = append(body, []byte(`}}`)...)
	req, err := http.NewRequestWithContext(t.Context(), "POST", f.server.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
	req.Header.Set(hdrMethod, method)
	if name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	res, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode == 401 {
		return res.StatusCode, rpcResponse{}, raw
	}
	if res.StatusCode == http.StatusBadRequest && !json.Valid(body) {
		return res.StatusCode, rpcResponse{}, raw
	}
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	_, _ = rec.Write(raw)
	return res.StatusCode, decodeRPC(t, rec), raw
}

func (f *historyTransportFixture) call(t *testing.T, key, name string, args []byte, denied bool) *mcpsdk.CallToolResult {
	t.Helper()
	status, rpc, _ := f.rpc(t, key, "tools/call", name, args)
	if rpc.Error != nil {
		if denied && key == f.reader && status == http.StatusBadRequest &&
			(name == "purge_work_history" || name == "reissue_work_history") &&
			rpc.Error.Code == jsonrpc.CodeInvalidParams && rpc.Error.Message == fmt.Sprintf("unknown tool %q", name) {
			return nil
		}
		t.Fatalf("%s: unexpected protocol error %d (HTTP %d): %s", name, rpc.Error.Code, status, rpc.Error.Message)
	}
	if status != 200 {
		t.Fatalf("%s: unexpected HTTP status %d", name, status)
	}
	result := decodeResult[mcpsdk.CallToolResult](t, rpc)
	if result.IsError != denied {
		t.Fatalf("%s: denied=%v, want %v", name, result.IsError, denied)
	}
	return result
}

func (f *historyTransportFixture) malformed(t *testing.T, name string, raw []byte) {
	t.Helper()
	status, _, body := f.rpc(t, f.admin, "tools/call", name, raw)
	const prefix = "malformed payload: unmarshaling jsonrpc message: json: expected ',' after object field value but found '{':"
	if json.Valid(raw) || status != http.StatusBadRequest || !bytes.HasPrefix(body, []byte(prefix)) {
		t.Fatal("expected SDK malformed-envelope rejection for trailing JSON")
	}
}

func equalHistoryJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	decode := func(raw []byte) any {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal("decode history comparison:", err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			t.Fatal("history comparison expected EOF")
		}
		return value
	}
	return reflect.DeepEqual(decode(a), decode(b))
}

func (f *historyTransportFixture) http(t *testing.T, op string, raw []byte, want int) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "POST", f.server.URL+"/api/v1/work-history/"+op, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.admin)
	res, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != want {
		t.Fatalf("HTTP %s: got %d want %d", op, res.StatusCode, want)
	}
}

func (f *historyTransportFixture) reissue(kind string) []byte {
	id := f.jobID
	config := `"scheduled_at":"2099-01-01T00:00:00Z","max_attempts":2`
	if kind == "cron" {
		id, config = f.cronID, `"name":"fresh-schedule","expression":"@every 1h","timezone":"UTC","enabled":false`
	}
	return []byte(fmt.Sprintf(`{"source":{"kind":%q,"id":%q},"target_id":%q,"payload":{},"reviewed":true,"confirm":true,%s}`, kind, id, f.fresh, config))
}

func TestHistoryCatalogAndRawTransport(t *testing.T) {
	f := historyTransportSetup(t)
	for _, enabled := range []bool{false, true} {
		for _, perm := range []string{permRead, permAdmin, permWrite} {
			deps := Deps{}
			if enabled {
				deps.History = f.service
			}
			reg := BuildAgentRegistry(deps, permSet{perm: true})
			for _, spec := range []struct {
				name string
				read bool
			}{
				{"list_work_history", true}, {"get_work_history", true}, {"get_work_history_capacity", true},
				{"purge_work_history", false}, {"reissue_work_history", false},
			} {
				want := enabled && (spec.read && perm == permRead || !spec.read && perm == permAdmin)
				if (reg.Get(spec.name) != nil) != want {
					t.Fatalf("enabled=%v perm=%s tool=%s", enabled, perm, spec.name)
				}
			}
		}
	}
	ctx := f.ctx(t, f.adminID)
	good := []byte(`{"request":{}}`)
	result := f.call(t, f.admin, "get_work_history_capacity", good, false)
	structured, _ := json.Marshal(result.StructuredContent)
	out, err := f.agent.Dispatch(ctx, "get_work_history_capacity", good)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := json.Marshal(out)
	if !equalHistoryJSON(t, structured, shared) || !bytes.Contains(shared, []byte(`"max_rows":"100000"`)) ||
		!bytes.Contains(shared, []byte(`"max_bytes":"67108864"`)) || !bytes.Contains(shared, []byte(`"rows_reserved":"0"`)) {
		t.Fatal("capacity parity/decimal string contract differs")
	}
	for _, spec := range []struct {
		tool string
		raw  []byte
	}{
		{"list_work_history", []byte(`{"limit":100}`)},
		{"get_work_history", []byte(fmt.Sprintf(`{"kind":"cron","id":%q}`, f.cronID))},
	} {
		external := f.call(t, f.admin, spec.tool, historyEnvelope(spec.raw), false)
		local, err := f.agent.Dispatch(ctx, spec.tool, historyEnvelope(spec.raw))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(external.StructuredContent)
		b, _ := json.Marshal(local)
		if !equalHistoryJSON(t, a, b) || bytes.Contains(a, []byte("unavailable-old-body")) {
			t.Fatal("metadata parity failed or retained original payload")
		}
		if spec.tool == "get_work_history" && !bytes.Contains(a, []byte(`"config_revision":"1"`)) {
			t.Fatal("archive revision lost decimal string")
		}
	}
	bad := [][]byte{
		[]byte(`{"request":{"limit":1,"limit":2}}`),
		[]byte(`{"request":{"limit":1,"actor_id":"forged"}}`),
		[]byte(`{"request":{"limit":1},"request":{"limit":2}}`),
		[]byte(`{"request":{"limit":1},"principal":{}}`),
		[]byte(`{"request":{"Limit":1}}`),
		[]byte("{\"request\":{\"limit\":1,\"bad\":\"\xff\"}}"),
		[]byte(`{"request":{"limit":1,"after":{"kind":"job","id":"x","id":"y"}}}`),
		[]byte(`{"request":` + strings.Repeat(" ", workloadhistory.MaxRequestBytes) + `{"limit":1}}`),
		[]byte(`{"request":{"limit":1}} {}`),
	}
	for i, raw := range bad {
		t.Run(fmt.Sprintf("raw-%d", i), func(t *testing.T) {
			if _, err := f.agent.Dispatch(ctx, "list_work_history", raw); err == nil {
				t.Fatal("shared registry accepted malformed raw")
			}
			if json.Valid(raw) {
				f.call(t, f.admin, "list_work_history", raw, true)
			} else {
				f.malformed(t, "list_work_history", raw)
			}
		})
	}
	// Each mutated payload would create work if a transport normalized it first.
	valid := f.reissue("job")
	for i, raw := range [][]byte{
		bytes.Replace(valid, []byte(`"payload":{}`), []byte(`"payload":{"x":1,"x":2}`), 1),
		bytes.Replace(valid, []byte(`"payload":{}`), []byte("\"payload\":{\"x\":\"\xff\"}"), 1),
		bytes.Replace(valid, []byte(`"payload":{}`), []byte(`"payload":"`+strings.Repeat("x", workloadhistory.MaxRequestBytes)+`"`), 1),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		bytes.Replace(valid, []byte(`"payload":{}`), []byte(`"payload":{},"operator_id":"forged"`), 1),
	} {
		t.Run(fmt.Sprintf("mutation-raw-%d", i), func(t *testing.T) {
			if _, err := f.agent.Dispatch(ctx, "reissue_work_history", historyEnvelope(raw)); err == nil {
				t.Fatal("shared registry normalized malformed payload")
			}
			if json.Valid(raw) {
				f.call(t, f.admin, "reissue_work_history", historyEnvelope(raw), true)
			} else {
				f.malformed(t, "reissue_work_history", historyEnvelope(raw))
			}
			f.http(t, "reissue", raw, 400)
			if f.count(t, `SELECT count(*) FROM jobs`) != 0 {
				t.Fatal("malformed request created work")
			}
		})
	}
}

func TestHistoryTransportLiveAuthorizationAndMutations(t *testing.T) {
	f := historyTransportSetup(t)
	good := []byte(`{"request":{}}`)
	for _, key := range []string{"", "history-test-only", "orva_unknown"} {
		status, _, _ := f.rpc(t, key, "tools/call", "get_work_history_capacity", good)
		if status != 401 {
			t.Fatal("invalid bearer authenticated")
		}
	}
	_, catalog, _ := f.rpc(t, f.reader, "tools/list", "", nil)
	tools := decodeResult[mcpsdk.ListToolsResult](t, catalog)
	for _, tool := range tools.Tools {
		if tool.Name == "purge_work_history" || tool.Name == "reissue_work_history" {
			t.Fatal("read-only catalog exposed mutations")
		}
	}
	result := f.call(t, f.reader, "get_work_history_capacity", good, false)
	serialized, _ := json.Marshal(result.StructuredContent)
	if !bytes.Contains(serialized, []byte(`"can_manage":false`)) {
		t.Fatal("readonly capacity claimed management")
	}
	purge := []byte(fmt.Sprintf(`{"selected":[{"kind":"job","id":%q}],"confirm":true}`, f.jobID))
	for _, spec := range []struct {
		name string
		raw  []byte
	}{
		{"purge_work_history", purge}, {"reissue_work_history", f.reissue("job")},
	} {
		f.call(t, f.reader, spec.name, historyEnvelope(spec.raw), true)
		// Force a cached admin registry with a freshly authenticated read-only reference.
		if _, err := f.agent.Dispatch(f.ctx(t, f.readerID), spec.name, historyEnvelope(spec.raw)); err == nil {
			t.Fatal("live service trusted catalog permissions instead of current credential")
		}
	}
	for _, flag := range []string{"confirm", "reviewed"} {
		raw := bytes.Replace(f.reissue("job"), []byte(`"`+flag+`":true`), []byte(`"`+flag+`":false`), 1)
		f.call(t, f.admin, "reissue_work_history", historyEnvelope(raw), true)
		if _, err := f.agent.Dispatch(f.ctx(t, f.adminID), "reissue_work_history", historyEnvelope(raw)); err == nil {
			t.Fatal("shared registry waived review/confirmation")
		}
	}
	f.call(t, f.admin, "purge_work_history", historyEnvelope(bytes.Replace(purge, []byte(`true`), []byte(`false`), 1)), true)
	f.call(t, f.admin, "purge_work_history", historyEnvelope(purge), true)
	f.exec(t, `UPDATE work_history SET eligible_at=? WHERE kind='job'`, time.Now().UTC().Add(-time.Hour))
	f.exec(t, `UPDATE work_history SET reserved=1 WHERE kind='job'`)
	f.call(t, f.admin, "purge_work_history", historyEnvelope(purge), true)
	f.exec(t, `UPDATE work_history SET reserved=0 WHERE kind='job'`)
	for _, kind := range []string{"job", "cron"} {
		raw := f.reissue(kind)
		external := f.call(t, f.admin, "reissue_work_history", historyEnvelope(raw), false)
		data, _ := json.Marshal(external.StructuredContent)
		var out struct {
			ID     string `json:"new_id"`
			Origin string `json:"origin"`
		}
		if json.Unmarshal(data, &out) != nil || out.ID == "" || out.ID == f.jobID || out.ID == f.cronID || out.Origin != "operator" {
			t.Fatal("reissue reused old identity")
		}
		if kind == "job" {
			job, err := f.db.GetAsyncJob(t.Context(), out.ID)
			if err != nil || job.FunctionID != f.fresh || job.OperatorID != f.adminID || job.OriginKind != "operator" || string(job.Payload) != "{}" {
				t.Fatal("external reissue did not persist fresh operator work", err)
			}
		} else {
			cron, err := f.db.GetAsyncCron(t.Context(), out.ID)
			if err != nil || cron.FunctionID != f.fresh || cron.OperatorID != f.adminID || cron.ConfigRevision != 1 || cron.Payload != "{}" {
				t.Fatal("fresh cron contract", err)
			}
		}
		if f.count(t, `SELECT count(*) FROM work_history WHERE kind='reissue' AND original_id=?`, out.ID) != 1 {
			t.Fatal("missing old-to-new audit")
		}
	}
	local, err := f.agent.Dispatch(f.ctx(t, f.adminID), "reissue_work_history", historyEnvelope(f.reissue("job")))
	if err != nil || local.(map[string]string)["new_id"] == "" {
		t.Fatal("shared registry reissue failed", err)
	}
	f.call(t, f.admin, "purge_work_history", historyEnvelope(purge), false)
	if f.count(t, `SELECT count(*) FROM work_history WHERE kind='job'`) != 0 || f.count(t, `SELECT count(*) FROM work_history WHERE kind='purge'`) != 1 {
		t.Fatal("purge did not commit audited removal")
	}
	if err := f.db.DeleteAPIKey(f.adminID); err != nil {
		t.Fatal(err)
	}
	status, _, _ := f.rpc(t, f.admin, "tools/call", "get_work_history_capacity", good)
	if status != 401 {
		t.Fatal("revoked bearer authenticated")
	}
	if _, err := f.agent.Dispatch(f.ctx(t, f.adminID), "get_work_history_capacity", good); err == nil {
		t.Fatal("cached registry ignored live revocation")
	}
}

func TestHistoryTransportPendingRevocation(t *testing.T) {
	for _, transport := range []string{"external", "shared"} {
		t.Run(transport, func(t *testing.T) {
			f := historyTransportSetup(t)
			entered, release := make(chan struct{}), make(chan struct{})
			f.service.CurrentArtifact = func(string) string {
				close(entered)
				<-release
				return "fixture-artifact"
			}
			done := make(chan bool, 1)
			go func() {
				raw := historyEnvelope(f.reissue("job"))
				if transport == "shared" {
					_, err := f.agent.Dispatch(f.ctx(t, f.adminID), "reissue_work_history", raw)
					done <- err != nil
					return
				}
				f.call(t, f.admin, "reissue_work_history", raw, true)
				done <- true
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("mutation did not reach observed admission")
			}
			if err := f.db.DeleteAPIKey(f.adminID); err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			select {
			case denied := <-done:
				if !denied {
					t.Fatal("pending mutation ignored revoked credential")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not finish")
			}
			if f.count(t, `SELECT count(*) FROM jobs`) != 0 || f.count(t, `SELECT count(*) FROM work_history WHERE kind='reissue'`) != 0 {
				t.Fatal("denied pending mutation persisted work/audit")
			}
		})
	}
}
