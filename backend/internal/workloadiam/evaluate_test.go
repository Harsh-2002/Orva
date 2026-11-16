package workloadiam

import (
	"reflect"
	"sync"
	"testing"
)

const (
	owner  = "019a8dd0-0000-7000-8000-000000000001"
	other  = "019a8dd0-0000-7000-8000-000000000002"
	third  = "019a8dd0-0000-7000-8000-000000000003"
	execID = "019a8dd0-0000-7000-8000-000000000004"
)

func str(s string) *string { return &s }
func emptyPolicy() Policy {
	return Policy{SchemaVersion: 1, Allow: []Statement{}, Inbound: []Acceptance{}}
}
func metadata(id string) Metadata {
	return Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact-1"}
}
func executionContext() Context {
	return Context{Principal: Principal{FunctionID: owner, WorkerID: "worker-1", ArtifactRevision: "artifact-1"}, Phase: Execution, Ready: true, Lease: Lease{ID: "lease-1", WorkerID: "worker-1", ExecutionID: execID, Active: true}}
}
func bootstrapContext() Context {
	c := executionContext()
	c.Phase = Bootstrap
	c.Ready = false
	c.Lease = Lease{}
	return c
}
func key(s string) Resource       { return Resource{OwnKV: &KVResource{Key: str(s)}} }
func prefix(s string) Resource    { return Resource{OwnKV: &KVResource{Prefix: str(s)}} }
func function(id string) Resource { return Resource{Function: &FunctionResource{ID: id}} }
func cron(name, target string) Resource {
	return Resource{OwnCron: &CronResource{Name: str(name), TargetID: target}}
}
func cronPrefix(name, target string) Resource {
	return Resource{OwnCron: &CronResource{Prefix: str(name), TargetID: target}}
}
func activeExecution() Resource { return Resource{OwnExecution: &ExecutionResource{}} }
func statement(phase Phase, resource Resource, actions ...Action) Statement {
	return Statement{Actions: actions, Resource: resource, Context: phase}
}
func request(op Operation, resource Resource) Request {
	return Request{Operation: op, FunctionID: owner, Resource: resource}
}
func compiled(t *testing.T, p Policy) *Evaluator {
	t.Helper()
	e, err := Compile(metadata(owner), p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func withStatements(ss ...Statement) Policy {
	p := emptyPolicy()
	p.Allow = append(p.Allow, ss...)
	return p
}

func TestOperationMapping(t *testing.T) {
	cases := []struct {
		op      Operation
		actions []Action
	}{
		{OpKVGet, []Action{KVGet}}, {OpKVList, []Action{KVList}}, {OpKVPut, []Action{KVPut}}, {OpKVDelete, []Action{KVDelete}},
		{OpKVIncr, []Action{KVIncr, KVGet}}, {OpKVCAS, []Action{KVCAS, KVGet}},
		{OpInvoke, []Action{FunctionsInvoke}}, {OpStreamOpen, []Action{FunctionsInvoke}}, {OpStreamNext, []Action{FunctionsInvoke}},
		{OpJobsEnqueue, []Action{JobsEnqueue}}, {OpCronsUpsert, []Action{CronsUpsert}}, {OpSpansEmit, []Action{SpansEmit}},
		{"", nil}, {"kv.batch", nil}, {"invoke.stream.close", nil}, {"functions.*", nil}, {"KV.get", nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			if got := RequiredActions(tc.op); !reflect.DeepEqual(got, tc.actions) {
				t.Fatalf("got %v, want %v", got, tc.actions)
			}
		})
	}
	a := RequiredActions(OpKVGet)
	a[0] = KVPut
	if RequiredActions(OpKVGet)[0] != KVGet {
		t.Fatal("mapping aliases returned slice")
	}
}

func TestLiteralKVSelectors(t *testing.T) {
	cases := []struct {
		name         string
		grant        Resource
		op           Operation
		wantResource Resource
		want         bool
	}{
		{"exact", key("config"), OpKVGet, key("config"), true},
		{"exact-not-prefix", key("config"), OpKVGet, key("config/x"), false},
		{"case", prefix("Config/"), OpKVGet, key("config/x"), false},
		{"percent-literal", prefix("%_"), OpKVGet, key("%_one"), true},
		{"percent-not-like", prefix("%_"), OpKVGet, key("abc"), false},
		{"star-literal", prefix("*"), OpKVGet, key("*one"), true},
		{"star-not-glob", prefix("*"), OpKVGet, key("one"), false},
		{"unicode", prefix("状态/"), OpKVGet, key("状态/甲"), true},
		{"no-normalization", prefix("é"), OpKVGet, key("e\u0301"), false},
		{"no-path-normalization", prefix("a/../"), OpKVGet, key("a/../b"), true},
		{"no-path-shortcut", prefix("a/../"), OpKVGet, key("b"), false},
		{"empty-prefix", prefix(""), OpKVGet, key("anything"), true},
		{"list-within", prefix("cfg/"), OpKVList, prefix("cfg/nested/"), true},
		{"list-same", prefix("cfg/"), OpKVList, prefix("cfg/"), true},
		{"list-wider", prefix("cfg/"), OpKVList, prefix("cfg"), false},
		{"list-empty-denied", prefix("cfg/"), OpKVList, prefix(""), false},
		{"list-empty-authorized", prefix(""), OpKVList, prefix(""), true},
		{"list-case", prefix("cfg/"), OpKVList, prefix("CFG/"), false},
		{"get-cannot-request-range", prefix(""), OpKVGet, prefix(""), false},
		{"list-cannot-request-key", prefix(""), OpKVList, key("cfg"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := KVGet
			if tc.op == OpKVList {
				a = KVList
			}
			e := compiled(t, withStatements(statement(Execution, tc.grant, a)))
			if got := e.Allows(executionContext(), request(tc.op, tc.wantResource)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEveryActionAndReadDependencies(t *testing.T) {
	cases := []struct {
		op       Operation
		resource Resource
		actions  []Action
	}{
		{OpKVGet, key("k"), []Action{KVGet}}, {OpKVList, prefix("k"), []Action{KVList}},
		{OpKVPut, key("k"), []Action{KVPut}}, {OpKVDelete, key("k"), []Action{KVDelete}},
		{OpKVIncr, key("k"), []Action{KVIncr, KVGet}}, {OpKVCAS, key("k"), []Action{KVCAS, KVGet}},
		{OpInvoke, function(other), []Action{FunctionsInvoke}}, {OpStreamOpen, function(other), []Action{FunctionsInvoke}}, {OpStreamNext, function(other), []Action{FunctionsInvoke}},
		{OpJobsEnqueue, function(other), []Action{JobsEnqueue}}, {OpCronsUpsert, cron("tick", owner), []Action{CronsUpsert}}, {OpSpansEmit, activeExecution(), []Action{SpansEmit}},
	}
	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			r := request(tc.op, tc.resource)
			if tc.op == OpSpansEmit {
				r.ExecutionID = execID
			}
			if compiled(t, emptyPolicy()).Allows(executionContext(), r) {
				t.Fatal("empty policy allowed")
			}
			for _, e := range []*Evaluator{nil, {}} {
				if e.Allows(executionContext(), r) {
					t.Fatal("zero evaluator allowed")
				}
			}
			e := compiled(t, withStatements(statement(Execution, tc.resource, tc.actions...)))
			if !e.Allows(executionContext(), r) {
				t.Fatal("exact grant denied")
			}
			r.FunctionID = other
			if e.Allows(executionContext(), r) {
				t.Fatal("cross-namespace allowed")
			}
			r.FunctionID = owner
			if len(tc.actions) == 2 {
				for _, a := range tc.actions {
					one := compiled(t, withStatements(statement(Execution, tc.resource, a)))
					if one.Allows(executionContext(), r) {
						t.Fatal("partial dependency allowed")
					}
				}
				wrongRead := compiled(t, withStatements(statement(Execution, tc.resource, tc.actions[0]), statement(Execution, key("different"), KVGet)))
				if wrongRead.Allows(executionContext(), r) {
					t.Fatal("read on another key satisfied dependency")
				}
				union := compiled(t, withStatements(statement(Execution, tc.resource, tc.actions[0]), statement(Execution, prefix(""), KVGet)))
				if !union.Allows(executionContext(), r) {
					t.Fatal("union failed")
				}
			}
		})
	}
}

func TestContextAndRequestDenials(t *testing.T) {
	e := compiled(t, withStatements(statement(Execution, prefix(""), KVGet), statement(Bootstrap, prefix(""), KVGet)))
	r := request(OpKVGet, key("k"))
	if !e.Allows(executionContext(), r) || !e.Allows(bootstrapContext(), r) {
		t.Fatal("valid contexts denied")
	}
	cases := []struct {
		name   string
		mutate func(*Context)
	}{
		{"zero", func(c *Context) { *c = Context{} }},
		{"wrong-owner", func(c *Context) { c.Principal.FunctionID = other }},
		{"empty-worker", func(c *Context) { c.Principal.WorkerID = "" }},
		{"obsolete-artifact", func(c *Context) { c.Principal.ArtifactRevision = "old" }},
		{"unknown-phase", func(c *Context) { c.Phase = "background" }},
		{"empty-phase", func(c *Context) { c.Phase = "" }},
		{"unready-execution", func(c *Context) { c.Ready = false }},
		{"empty-lease", func(c *Context) { c.Lease = Lease{} }},
		{"revoked-lease", func(c *Context) { c.Lease.Active = false }},
		{"lease-worker", func(c *Context) { c.Lease.WorkerID = "other-worker" }},
		{"empty-lease-id", func(c *Context) { c.Lease.ID = "" }},
		{"invalid-execution", func(c *Context) { c.Lease.ExecutionID = "not-uuid" }},
		{"bootstrap-after-readiness", func(c *Context) { c.Phase = Bootstrap; c.Lease = Lease{} }},
		{"stale-lease-not-bootstrap", func(c *Context) { c.Phase = Bootstrap; c.Ready = false; c.Lease.Active = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := executionContext()
			tc.mutate(&c)
			if e.Allows(c, r) {
				t.Fatal("invalid context allowed")
			}
		})
	}
	badRequests := []Request{
		{}, request("unknown", key("k")), request(OpKVGet, Resource{}),
		request(OpKVGet, function(other)), request(OpKVGet, key("")),
		request(OpKVGet, Resource{OwnKV: &KVResource{Key: str("k"), Prefix: str("")}}),
		request(OpKVGet, Resource{OwnKV: &KVResource{Key: str("k")}, Function: &FunctionResource{ID: other}}),
		{Operation: OpKVGet, FunctionID: owner, Resource: key("k"), ExecutionID: execID},
	}
	for i, bad := range badRequests {
		if e.Allows(executionContext(), bad) {
			t.Fatalf("bad request %d allowed", i)
		}
	}
}

func TestBootstrapAndBatch(t *testing.T) {
	e := compiled(t, withStatements(statement(Bootstrap, prefix("cfg/"), KVGet, KVList), statement(Execution, prefix(""), KVGet, KVPut, KVDelete, KVIncr, KVCAS)))
	for _, op := range []Operation{OpKVGet, OpKVList, OpKVPut, OpKVDelete, OpKVIncr, OpKVCAS, OpInvoke, OpJobsEnqueue, OpCronsUpsert, OpSpansEmit} {
		r := request(op, key("cfg/key"))
		if op == OpKVList {
			r.Resource = prefix("cfg/")
		}
		want := op == OpKVGet || op == OpKVList
		if e.Allows(bootstrapContext(), r) != want {
			t.Fatalf("bootstrap op %s", op)
		}
	}
	for _, c := range []Context{bootstrapContext(), executionContext()} {
		gets := []Request{request(OpKVGet, key("cfg/a")), request(OpKVGet, key("cfg/b"))}
		if !e.AllowsBatch(c, gets) {
			t.Fatal("get batch denied")
		}
		mixed := append(append([]Request{}, gets...), request(OpKVPut, key("cfg/c")))
		if e.AllowsBatch(c, mixed) != (c.Phase == Execution) {
			t.Fatal("mixed batch phase mismatch")
		}
	}
	getOnly := compiled(t, withStatements(statement(Execution, prefix("cfg/"), KVGet)))
	cases := [][]Request{
		nil, {}, {request(OpKVGet, key("cfg/a")), request(OpKVGet, key("secret"))},
		{request(OpKVGet, key("cfg/a")), request(OpKVPut, key("cfg/b"))},
		{request(OpKVList, prefix("cfg/"))}, {request(OpKVIncr, key("cfg/a"))},
		{request(OpInvoke, function(other))}, {request("kv.batch", key("cfg/a"))},
		make([]Request, MaxBatchOperations+1),
	}
	for i, batch := range cases {
		if getOnly.AllowsBatch(executionContext(), batch) {
			t.Fatalf("invalid batch %d allowed", i)
		}
	}
	full := make([]Request, MaxBatchOperations)
	for i := range full {
		full[i] = request(OpKVGet, key("cfg/a"))
	}
	if !getOnly.AllowsBatch(executionContext(), full) {
		t.Fatal("batch boundary denied")
	}
	if (new(Evaluator)).AllowsBatch(executionContext(), full) {
		t.Fatal("zero evaluator batch allowed")
	}
	// A caller can perform accesses only after the single whole-batch decision.
	accesses := 0
	if getOnly.AllowsBatch(executionContext(), cases[2]) {
		accesses++
	}
	if accesses != 0 {
		t.Fatal("partial batch access")
	}
	if compiled(t, withStatements(statement(Execution, prefix(""), KVGet))).Allows(bootstrapContext(), request(OpKVGet, key("k"))) {
		t.Fatal("execution grant implied bootstrap")
	}
	if compiled(t, withStatements(statement(Bootstrap, prefix(""), KVGet))).Allows(executionContext(), request(OpKVGet, key("k"))) {
		t.Fatal("bootstrap grant implied execution")
	}
	c := bootstrapContext()
	c.Principal.ArtifactRevision = "old"
	if e.Allows(c, request(OpKVGet, key("cfg/a"))) {
		t.Fatal("old bootstrap code granted")
	}
}

func TestCronAndSpanResources(t *testing.T) {
	e := compiled(t, withStatements(statement(Execution, cronPrefix("tick/", owner), CronsUpsert), statement(Execution, activeExecution(), SpansEmit)))
	for _, tc := range []struct {
		r    Request
		want bool
	}{
		{request(OpCronsUpsert, cron("tick/a", owner)), true}, {request(OpCronsUpsert, cron("other", owner)), false},
		{request(OpCronsUpsert, cron("tick/a", other)), false}, {request(OpCronsUpsert, cronPrefix("tick/", owner)), false},
		{Request{Operation: OpSpansEmit, FunctionID: owner, Resource: activeExecution(), ExecutionID: execID}, true},
		{Request{Operation: OpSpansEmit, FunctionID: owner, Resource: activeExecution(), ExecutionID: other}, false},
		{request(OpSpansEmit, activeExecution()), false},
	} {
		if e.Allows(executionContext(), tc.r) != tc.want {
			t.Fatalf("resource mismatch: %+v", tc.r)
		}
	}
	if _, err := Compile(metadata(owner), withStatements(statement(Execution, cron("tick", other), CronsUpsert))); err == nil {
		t.Fatal("cross-function cron compiled")
	}
}

func TestDualSidedEdges(t *testing.T) {
	a := compiled(t, withStatements(statement(Execution, function(other), FunctionsInvoke, JobsEnqueue)))
	p := emptyPolicy()
	p.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{F2F}}}
	b, err := Compile(metadata(other), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []Operation{OpInvoke, OpStreamOpen, OpStreamNext, OpJobsEnqueue} {
		r := request(op, function(other))
		want := op != OpJobsEnqueue
		if AllowsEdge(a, b, executionContext(), r) != want {
			t.Fatalf("trigger mismatch %s", op)
		}
		if AllowsEdge(a, nil, executionContext(), r) || AllowsEdge(nil, b, executionContext(), r) {
			t.Fatal("missing side allowed")
		}
		if AllowsEdge(compiled(t, emptyPolicy()), b, executionContext(), r) {
			t.Fatal("inbound alone allowed")
		}
	}
	wrongTarget, err := Compile(metadata(third), p)
	if err != nil {
		t.Fatal(err)
	}
	if AllowsEdge(a, wrongTarget, executionContext(), request(OpInvoke, function(other))) {
		t.Fatal("target identity rebound")
	}
	if b.Accepts(third, F2F) || b.Accepts(owner, Job) || b.Accepts(owner, "") || b.Accepts("", F2F) || new(Evaluator).Accepts(owner, F2F) {
		t.Fatal("invalid acceptance allowed")
	}
	jobPolicy := emptyPolicy()
	jobPolicy.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{Job}}}
	jobTarget, err := Compile(metadata(other), jobPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if !AllowsEdge(a, jobTarget, executionContext(), request(OpJobsEnqueue, function(other))) {
		t.Fatal("job grant denied")
	}
	selfPolicy := withStatements(statement(Execution, function(owner), FunctionsInvoke), statement(Execution, cronPrefix("", owner), CronsUpsert))
	selfPolicy.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{F2F, Cron}}}
	self := compiled(t, selfPolicy)
	if !AllowsEdge(self, self, executionContext(), request(OpInvoke, function(owner))) || !AllowsEdge(self, self, executionContext(), request(OpCronsUpsert, cron("tick", owner))) {
		t.Fatal("explicit self edge denied")
	}
	if AllowsEdge(self, self, executionContext(), request(OpKVGet, key("k"))) {
		t.Fatal("non-edge allowed")
	}
	p.Inbound[0].Triggers = []Trigger{Cron}
	if _, err := Compile(metadata(other), p); err == nil {
		t.Fatal("nonself cron caller compiled")
	}
}

func TestFrozenAndConcurrent(t *testing.T) {
	p := withStatements(statement(Execution, key("k"), KVGet), statement(Execution, function(other), FunctionsInvoke), statement(Execution, cron("tick", owner), CronsUpsert))
	p.Inbound = []Acceptance{{CallerID: other, Triggers: []Trigger{F2F}}}
	m := metadata(owner)
	e, err := Compile(m, p)
	if err != nil {
		t.Fatal(err)
	}
	*p.Allow[0].Resource.OwnKV.Key = "changed"
	p.Allow[0].Actions[0] = KVPut
	p.Allow[1].Resource.Function.ID = third
	*p.Allow[2].Resource.OwnCron.Name = "changed"
	p.Allow[2].Resource.OwnCron.TargetID = other
	p.Inbound[0].CallerID = third
	p.Inbound[0].Triggers[0] = Job
	m.OwnerID = other
	if !e.Allows(executionContext(), request(OpKVGet, key("k"))) || !e.Allows(executionContext(), request(OpInvoke, function(other))) || !e.Allows(executionContext(), request(OpCronsUpsert, cron("tick", owner))) || !e.Accepts(other, F2F) {
		t.Fatal("compiled policy aliases input")
	}
	meta := e.Metadata()
	meta.ApprovedArtifact = "changed"
	if e.Metadata().ApprovedArtifact != "artifact-1" {
		t.Fatal("metadata alias")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				if !e.Allows(executionContext(), request(OpKVGet, key("k"))) || !e.Accepts(other, F2F) {
					t.Error("concurrent decision changed")
				}
			}
		})
	}
	wg.Wait()
	if (*Evaluator)(nil).Metadata() != (Metadata{}) {
		t.Fatal("nil metadata")
	}
}

func TestCalleeDoesNotInheritCallerAuthority(t *testing.T) {
	caller := compiled(t, withStatements(statement(Execution, function(other), FunctionsInvoke), statement(Execution, function(third), FunctionsInvoke)))
	bPolicy := emptyPolicy()
	bPolicy.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{F2F}}}
	b, err := Compile(metadata(other), bPolicy)
	if err != nil {
		t.Fatal(err)
	}
	cPolicy := emptyPolicy()
	cPolicy.Inbound = []Acceptance{{CallerID: other, Triggers: []Trigger{F2F}}}
	callee, err := Compile(metadata(third), cPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if !AllowsEdge(caller, b, executionContext(), request(OpInvoke, function(other))) {
		t.Fatal("A to B denied")
	}
	bContext := executionContext()
	bContext.Principal.FunctionID = other
	bRequest := request(OpInvoke, function(third))
	bRequest.FunctionID = other
	if AllowsEdge(b, callee, bContext, bRequest) {
		t.Fatal("B inherited A to C grant")
	}
	bPolicy.Allow = []Statement{statement(Execution, function(third), FunctionsInvoke)}
	b, err = Compile(metadata(other), bPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if !AllowsEdge(b, callee, bContext, bRequest) {
		t.Fatal("B's own grant denied")
	}
	if AllowsEdge(caller, callee, executionContext(), request(OpInvoke, function(third))) {
		t.Fatal("C implicitly accepted A")
	}
}

func TestFrozenPrefixAndInboundCannotBroaden(t *testing.T) {
	p := withStatements(statement(Execution, prefix("cfg/"), KVGet), statement(Execution, cronPrefix("tick/", owner), CronsUpsert))
	p.Inbound = []Acceptance{{CallerID: other, Triggers: []Trigger{Job}}}
	e := compiled(t, p)
	*p.Allow[0].Resource.OwnKV.Prefix = ""
	*p.Allow[1].Resource.OwnCron.Prefix = ""
	p.Inbound[0].Triggers = append(p.Inbound[0].Triggers, F2F)
	p.Allow = append(p.Allow, statement(Execution, function(third), FunctionsInvoke))
	if e.Allows(executionContext(), request(OpKVGet, key("secret"))) || e.Allows(executionContext(), request(OpCronsUpsert, cron("outside", owner))) || e.Accepts(other, F2F) || e.Allows(executionContext(), request(OpInvoke, function(third))) {
		t.Fatal("source mutation broadened frozen authority")
	}
}
