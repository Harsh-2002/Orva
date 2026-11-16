// Package workloadiam provides inert, default-deny workload policy evaluation.
package workloadiam

const (
	SchemaVersion      = 1
	MaxPolicyBytes     = 32 * 1024
	MaxStatements      = 64
	MaxTargets         = 256
	MaxKeyRunes        = 256 // Matches the existing KV key contract; matching remains byte-literal.
	MaxBatchOperations = 100
)

type Action string

const (
	KVGet           Action = "kv.get"
	KVList          Action = "kv.list"
	KVPut           Action = "kv.put"
	KVDelete        Action = "kv.delete"
	KVIncr          Action = "kv.incr"
	KVCAS           Action = "kv.cas"
	FunctionsInvoke Action = "functions.invoke"
	JobsEnqueue     Action = "jobs.enqueue"
	CronsUpsert     Action = "crons.upsert"
	SpansEmit       Action = "spans.emit"
)

type Phase string

const (
	Execution Phase = "execution"
	Bootstrap Phase = "bootstrap"
)

type Trigger string

const (
	F2F  Trigger = "f2f"
	Job  Trigger = "job"
	Cron Trigger = "cron"
)

// Policy is user-authored. Ownership, combined revision and approval are external.
type Policy struct {
	SchemaVersion int          `json:"schema_version"`
	Allow         []Statement  `json:"allow"`
	Inbound       []Acceptance `json:"inbound"`
}

type Statement struct {
	Actions  []Action `json:"actions"`
	Resource Resource `json:"resource"`
	Context  Phase    `json:"context"`
}

// Resource is a closed union: exactly one pointer must be non-nil.
type Resource struct {
	OwnKV        *KVResource        `json:"own_kv,omitempty"`
	Function     *FunctionResource  `json:"function,omitempty"`
	OwnCron      *CronResource      `json:"own_cron,omitempty"`
	OwnExecution *ExecutionResource `json:"own_execution,omitempty"`
}

type KVResource struct {
	Key    *string `json:"key,omitempty"`
	Prefix *string `json:"prefix,omitempty"`
}

type FunctionResource struct {
	ID string `json:"id"`
}

type CronResource struct {
	Name     *string `json:"name,omitempty"`
	Prefix   *string `json:"prefix,omitempty"`
	TargetID string  `json:"target_id"`
}

type ExecutionResource struct{}

type Acceptance struct {
	CallerID string    `json:"caller_id"`
	Triggers []Trigger `json:"triggers"`
}

// Metadata is supplied by trusted storage, never decoded from a policy document.
type Metadata struct {
	OwnerID          string
	Revision         uint64
	ApprovedArtifact string
}

type Principal struct {
	FunctionID       string
	WorkerID         string
	ArtifactRevision string
}

// Lease is the host's live validation result, not an SDK claim or cached token.
type Lease struct {
	ID          string
	WorkerID    string
	ExecutionID string
	Active      bool
}

// Context must be host-owned. Ready is monotonic for a worker's lifetime.
type Context struct {
	Principal Principal
	Phase     Phase
	Ready     bool
	Lease     Lease
}

type Operation string

const (
	OpKVGet       Operation = "kv.get"
	OpKVList      Operation = "kv.list"
	OpKVPut       Operation = "kv.put"
	OpKVDelete    Operation = "kv.delete"
	OpKVIncr      Operation = "kv.incr"
	OpKVCAS       Operation = "kv.cas"
	OpInvoke      Operation = "invoke"
	OpStreamOpen  Operation = "invoke.stream.open"
	OpStreamNext  Operation = "invoke.stream.next"
	OpJobsEnqueue Operation = "jobs.enqueue"
	OpCronsUpsert Operation = "crons.upsert"
	OpSpansEmit   Operation = "spans.emit"
)

// Request.FunctionID addresses the owning namespace; ExecutionID is only for spans.
// Non-list KV requests and cron requests require exact selectors, not prefixes.
type Request struct {
	Operation   Operation
	FunctionID  string
	Resource    Resource
	ExecutionID string
}

// RequiredActions is the fixed operation mapping. The returned slice is caller-owned.
func RequiredActions(op Operation) []Action {
	switch op {
	case OpKVGet:
		return []Action{KVGet}
	case OpKVList:
		return []Action{KVList}
	case OpKVPut:
		return []Action{KVPut}
	case OpKVDelete:
		return []Action{KVDelete}
	case OpKVIncr:
		return []Action{KVIncr, KVGet}
	case OpKVCAS:
		return []Action{KVCAS, KVGet}
	case OpInvoke, OpStreamOpen, OpStreamNext:
		return []Action{FunctionsInvoke}
	case OpJobsEnqueue:
		return []Action{JobsEnqueue}
	case OpCronsUpsert:
		return []Action{CronsUpsert}
	case OpSpansEmit:
		return []Action{SpansEmit}
	default:
		return nil
	}
}
