//go:build linux

package workloadrecovery

import (
	"context"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/testutil/iamruntime"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func startupSDKSource(lang string) string {
	if lang == "node" {
		return `const { kv } = require("orva")
exports.handler = async () => {
  try { await kv.get("retained"); return {statusCode: 500} }
  catch (e) { return {statusCode: e.status === 403 ? 200 : 500, body: "sealed-node:" + e.status} }
}`
	}
	return `import orva
def handler(event):
    try:
        orva.kv.get("retained")
        return {"statusCode": 500}
    except orva.OrvaError as err:
        return {"statusCode": 200 if err.status == 403 else 500, "body": "sealed-python:" + str(err.status)}
`
}

func probeStartupWorkers(t *testing.T, ctx context.Context, db *database.Database, domain *workloadfence.Domain,
	factories []*workloadspawn.Factory, evaluators []*workloadiam.Evaluator, runtimeDir string,
) {
	t.Helper()
	jail := iamruntime.Nsjail(t, runtimeDir)
	a, err := workloadiam.NewAuthority(16, evaluators)
	must(t, err)
	active := make(map[string]string, len(factories))
	for _, f := range factories {
		active[f.Owner()] = f.ArtifactRevision()
	}
	host := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool {
		return active[owner] != "" && active[owner] == artifact
	}))
	for i, f := range factories {
		lang := []string{"node", "python"}[i]
		t.Run(lang, func(t *testing.T) {
			plan, err := f.Prepare(ctx)
			must(t, err)
			defer plan.Close()
			w, err := plan.Spawn(ctx, sandbox.ExecConfig{
				NsjailBin: jail, Domain: domain, SDKHandler: host.Handle,
				SeccompPolicy: sandbox.BuildSeccompPolicy("default", nil, nil),
				WorkloadTransitionHook: func(change func()) {
					if err := a.ChangeHostState(change); err != nil {
						t.Error(err)
					}
				},
			})
			must(t, err)
			defer func() { must(t, w.Quit(time.Second)) }()
			must(t, w.AwaitReady(ctx))
			state, ok := w.WorkloadState()
			if !ok || state.Identity.ArtifactRevision != f.ArtifactRevision() || state.Identity.FunctionID != f.Owner() || !state.Ready {
				t.Fatal("worker lacks its reconstructed identity")
			}
			res, err := w.DispatchEx(sandbox.WithExecution(ctx, sandbox.ExecInfo{ExecutionID: ids.New()}), []byte(`{}`))
			must(t, err)
			if res.StatusCode != 200 || res.Body != "sealed-"+lang+":403" {
				t.Fatalf("recovered worker did not exercise private SDK denial: status=%d body=%q", res.StatusCode, res.Body)
			}
		})
	}
	for _, stat := range host.Stats() {
		if stat.Op == "kv.get" && (stat.Count != uint64(len(factories)) || stat.Errors != stat.Count) {
			t.Fatal("workers did not reach the IAM-protected SDK host")
		}
	}
}
