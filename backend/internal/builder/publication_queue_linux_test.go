//go:build linux

package builder

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func publicationQueueFixture(t *testing.T) (*PublicationQueue, context.Context, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	schema := func(name string) {
		src, err := parser.ParseFile(token.NewFileSet(), "../database/"+name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(src, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				body, _ := strconv.Unquote(lit.Value)
				if strings.Contains(body, "CREATE TABLE IF NOT EXISTS") {
					if _, err := conn.Exec(body); err != nil {
						t.Fatal(err)
					}
				}
			}
			return true
		})
	}
	schema("workload_iam_schema.go")
	schema("workload_artifact_schema.go")
	owner, key := ids.New(), ids.New()
	fn := &database.Function{ID: owner, Name: "queued", Runtime: "node", Entrypoint: "handler.js", Status: "active", Version: 1,
		MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, NetworkMode: "none", AuthMode: "none", ConcurrencyPolicy: "queue", EnvVars: map[string]string{}}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	actor := database.IAMActorReference{Kind: "system", ID: ids.New()}
	r, err := db.NewIAMPolicy(t.Context(), owner, false, actor)
	if err != nil {
		t.Fatal(err)
	}
	schema("workload_managed_schema.go")
	if _, err := db.NewWorkloadArtifact(t.Context(), owner, actor); err != nil {
		t.Fatal(err)
	}
	if err := db.InitWorkloadDeploymentSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAPIKey(&database.APIKey{ID: key, Name: "writer", KeyHash: "fixture", Prefix: "fixture", Permissions: `["write"]`}); err != nil {
		t.Fatal(err)
	}
	e, err := r.AdmissionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.DataDir = t.TempDir()
	q := NewQueue(b, db, registry.New(db))
	q.jobs = make(chan BuildJob, 1)
	q.workers = 0
	s := &PublicationQueue{queue: q, authority: a, pending: map[string]*artifactfs.PreparedPaths{}}
	q.publication = s
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: key})
	return s, ctx, owner
}

func TestPublicationQueueFullCancellationAndDisabledLegacy(t *testing.T) {
	s, ctx, owner := publicationQueueFixture(t)
	ref, _ := auth.OperatorFromContext(ctx)
	archive := func() string {
		f, err := os.CreateTemp(t.TempDir(), "archive-")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		return f.Name()
	}
	path := archive()
	ctx, cancel := context.WithCancel(ctx)
	first, err := s.Submit(ctx, owner, path)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("accepted work followed request cancellation")
	}
	secondPath := archive()
	second, err := s.Submit(auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: ref.ID}), owner, secondPath)
	if !errors.Is(err, ErrQueueFull) || second == "" {
		t.Fatalf("queue full not recorded: %v", err)
	}
	dep, err := s.queue.db.GetDeployment(second)
	if err != nil || dep.Status != "failed" {
		t.Fatal("full queue left live work", err)
	}
	if _, err := os.Stat(secondPath); !os.IsNotExist(err) {
		t.Fatal("rejected archive leaked")
	}
	if err := s.queue.Submit(BuildJob{DeploymentID: ids.New(), FunctionID: owner}); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatal("legacy bypass accepted")
	}
	s.Shutdown(context.Background())
	dep, err = s.queue.db.GetDeployment(first)
	if err != nil || dep.Status != "failed" {
		t.Fatal("shutdown left queued work", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("never-started archive leaked")
	}
	missing := archive()
	if _, err := s.Submit(t.Context(), owner, missing); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatal("missing reference fell back")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("untrusted archive leaked")
	}
}
