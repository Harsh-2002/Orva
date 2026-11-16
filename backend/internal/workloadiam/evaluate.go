package workloadiam

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Evaluator is immutable after Compile, safe for concurrent reads; its zero value denies.
type Evaluator struct {
	metadata Metadata
	policy   Policy
	valid    bool
}

func Compile(m Metadata, p Policy) (*Evaluator, error) {
	if !validUUID(m.OwnerID) || m.Revision == 0 || !validHostID(m.ApprovedArtifact) {
		return nil, errors.New("invalid host policy metadata")
	}
	if err := Validate(p); err != nil {
		return nil, err
	}
	for _, s := range p.Allow {
		if s.Resource.OwnCron != nil && s.Resource.OwnCron.TargetID != m.OwnerID {
			return nil, errors.New("cron target must be self")
		}
	}
	for _, a := range p.Inbound {
		for _, t := range a.Triggers {
			if t == Cron && a.CallerID != m.OwnerID {
				return nil, errors.New("cron caller must be self")
			}
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	frozen, err := Decode(data)
	if err != nil {
		return nil, err
	}
	return &Evaluator{metadata: m, policy: frozen, valid: true}, nil
}

func (e *Evaluator) Metadata() Metadata {
	if e == nil {
		return Metadata{}
	}
	return e.metadata
}

func (e *Evaluator) validContext(c Context) bool {
	if e == nil || !e.valid || c.Principal.FunctionID != e.metadata.OwnerID || !validHostID(c.Principal.WorkerID) || c.Principal.ArtifactRevision != e.metadata.ApprovedArtifact {
		return false
	}
	switch c.Phase {
	case Bootstrap:
		return !c.Ready && c.Lease == (Lease{})
	case Execution:
		return c.Ready && c.Lease.Active && validHostID(c.Lease.ID) && c.Lease.WorkerID == c.Principal.WorkerID && validUUID(c.Lease.ExecutionID)
	default:
		return false
	}
}

// Allows checks outbound authority only. Calls/jobs/cron additionally require AllowsEdge.
func (e *Evaluator) Allows(c Context, r Request) bool {
	if !e.validContext(c) || r.FunctionID != e.metadata.OwnerID || !validResource(r.Resource) {
		return false
	}
	actions := RequiredActions(r.Operation)
	if len(actions) == 0 || !validRequest(c, r, actions[0]) {
		return false
	}
	for _, a := range actions {
		if !e.grants(c.Phase, a, r.Resource) {
			return false
		}
	}
	return true
}

func validRequest(c Context, r Request, a Action) bool {
	if !actionResource(a, r.Resource) {
		return false
	}
	if a != SpansEmit && r.ExecutionID != "" {
		return false
	}
	if c.Phase == Bootstrap && a != KVGet && a != KVList {
		return false
	}
	if kv := r.Resource.OwnKV; kv != nil {
		if a == KVList {
			return kv.Prefix != nil
		}
		return kv.Key != nil
	}
	if cron := r.Resource.OwnCron; cron != nil {
		return cron.Name != nil && cron.TargetID == r.FunctionID
	}
	if a == SpansEmit {
		return r.ExecutionID == c.Lease.ExecutionID
	}
	return true
}

func (e *Evaluator) grants(phase Phase, action Action, resource Resource) bool {
	for _, s := range e.policy.Allow {
		if s.Context != phase {
			continue
		}
		for _, a := range s.Actions {
			if a == action && resourceFits(s.Resource, resource) {
				return true
			}
		}
	}
	return false
}

func resourceFits(grant, request Resource) bool {
	switch {
	case grant.OwnKV != nil && request.OwnKV != nil:
		return selectorFits(grant.OwnKV.Key, grant.OwnKV.Prefix, request.OwnKV.Key, request.OwnKV.Prefix)
	case grant.Function != nil && request.Function != nil:
		return grant.Function.ID == request.Function.ID
	case grant.OwnCron != nil && request.OwnCron != nil:
		return grant.OwnCron.TargetID == request.OwnCron.TargetID && selectorFits(grant.OwnCron.Name, grant.OwnCron.Prefix, request.OwnCron.Name, request.OwnCron.Prefix)
	case grant.OwnExecution != nil && request.OwnExecution != nil:
		return true
	default:
		return false
	}
}

func selectorFits(exact, prefix, requestExact, requestPrefix *string) bool {
	if requestPrefix != nil {
		return prefix != nil && strings.HasPrefix(*requestPrefix, *prefix)
	}
	return requestExact != nil && (exact != nil && *exact == *requestExact || prefix != nil && strings.HasPrefix(*requestExact, *prefix))
}

// AllowsBatch preauthorizes a KV batch without performing or exposing any accesses.
func (e *Evaluator) AllowsBatch(c Context, requests []Request) bool {
	if !e.validContext(c) || len(requests) == 0 || len(requests) > MaxBatchOperations {
		return false
	}
	for _, r := range requests {
		if r.Operation != OpKVGet && r.Operation != OpKVPut && r.Operation != OpKVDelete {
			return false
		}
		if !e.Allows(c, r) {
			return false
		}
	}
	return true
}

// Accepts checks standing inbound authority, not a worker lease or target status.
func (e *Evaluator) Accepts(callerID string, trigger Trigger) bool {
	if e == nil || !e.valid || !validUUID(callerID) || !validTrigger(trigger) || trigger == Cron && callerID != e.metadata.OwnerID {
		return false
	}
	for _, a := range e.policy.Inbound {
		if a.CallerID != callerID {
			continue
		}
		for _, t := range a.Triggers {
			if t == trigger {
				return true
			}
		}
	}
	return false
}

// AllowsEdge combines both policies without revealing which side denied.
// Target availability and acquired-worker artifact checks remain host obligations.
func AllowsEdge(caller, target *Evaluator, c Context, r Request) bool {
	if !caller.Allows(c, r) || target == nil || !target.valid {
		return false
	}
	var targetID string
	var trigger Trigger
	switch r.Operation {
	case OpInvoke, OpStreamOpen, OpStreamNext:
		targetID, trigger = r.Resource.Function.ID, F2F
	case OpJobsEnqueue:
		targetID, trigger = r.Resource.Function.ID, Job
	case OpCronsUpsert:
		targetID, trigger = r.Resource.OwnCron.TargetID, Cron
	default:
		return false
	}
	return targetID == target.metadata.OwnerID && target.Accepts(c.Principal.FunctionID, trigger)
}

func validHostID(s string) bool { return s != "" && len(s) <= 256 && utf8.ValidString(s) }
