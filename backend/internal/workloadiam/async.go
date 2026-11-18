package workloadiam

import "context"

// AsyncOrigin is host-persisted provenance, not an execution lease or a grant.
type AsyncOrigin struct {
	Kind                    string
	CreatorID               string
	OperatorKind            string
	OperatorID              string
	TargetID                string
	Trigger                 Trigger
	ScheduleName            string
	ExpectedCreatorRevision uint64
	ExpectedTargetRevision  uint64
}

// StandingState comes from the live managed-pool observer under the IAM gate.
type StandingState struct {
	CreatorArtifact string
	TargetArtifact  string
}

type AsyncRevisions struct{ Creator, Target uint64 }

func (a *Authority) allowsStanding(o AsyncOrigin, s StandingState) bool {
	if !validUUID(o.TargetID) || !validHostID(s.TargetArtifact) || (o.Trigger != Job && o.Trigger != Cron) {
		return false
	}
	target := a.admissionPolicy(o.TargetID)
	if target == nil || !target.valid || target.metadata.ApprovedArtifact != s.TargetArtifact {
		return false
	}
	if o.ExpectedTargetRevision != 0 && target.metadata.Revision != o.ExpectedTargetRevision {
		return false
	}
	switch o.Kind {
	case "operator":
		if o.CreatorID != "" || !validUUID(o.OperatorID) {
			return false
		}
		return o.OperatorKind == "api_key" || o.OperatorKind == "session" || o.OperatorKind == "oauth"
	case "function":
		if o.OperatorID != "" || o.OperatorKind != "" || !validUUID(o.CreatorID) {
			return false
		}
		caller := a.admissionPolicy(o.CreatorID)
		if caller == nil || !caller.valid || caller.metadata.ApprovedArtifact != s.CreatorArtifact {
			return false
		}
		if o.ExpectedCreatorRevision != 0 && caller.metadata.Revision != o.ExpectedCreatorRevision {
			return false
		}
		r := Resource{Function: &FunctionResource{ID: o.TargetID}}
		action := JobsEnqueue
		if o.Trigger == Cron {
			if o.CreatorID != o.TargetID || o.ScheduleName == "" {
				return false
			}
			r = Resource{OwnCron: &CronResource{Name: &o.ScheduleName, TargetID: o.TargetID}}
			action = CronsUpsert
		}
		return validResource(r) && caller.grants(Execution, action, r) && target.Accepts(o.CreatorID, o.Trigger)
	default:
		return false
	}
}

// DoStandingObserved is for short host transactions (claim/resume), never SDK
// registration. It deliberately has no Context, WorkerID or ExecutionID.
func (a *Authority) DoStandingObserved(o AsyncOrigin, observe func() StandingState, effect func(AsyncRevisions) error) error {
	if a == nil || observe == nil || effect == nil {
		return ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	if !a.allowsStanding(o, observe()) {
		return ErrDenied
	}
	return effect(AsyncRevisions{Creator: a.policies[o.CreatorID].Metadata().Revision, Target: a.policies[o.TargetID].Metadata().Revision})
}

// DoAsyncCreateObserved requires the actual SDK caller lease and captures policy
// revisions in the same gate interval as the bounded persistence transaction.
func (a *Authority) DoAsyncCreateObserved(observe func() Context, currentTarget func() string, r Request, effect func(AsyncRevisions) error) error {
	if a == nil || observe == nil || currentTarget == nil || effect == nil || (r.Operation != OpJobsEnqueue && r.Operation != OpCronsUpsert) {
		return ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	c := observe()
	if !a.allows(c, r) {
		return ErrDenied
	}
	target := r.FunctionID
	if r.Resource.Function != nil {
		target = r.Resource.Function.ID
	}
	if a.policies[target].Metadata().ApprovedArtifact != currentTarget() {
		return ErrDenied
	}
	return effect(AsyncRevisions{Creator: a.policies[c.Principal.FunctionID].Metadata().Revision, Target: a.policies[target].Metadata().Revision})
}

// BeginAsyncObserved registers standing authority before pool wait. The origin
// must be loaded by the host, never decoded from SDK arguments.
func (a *Authority) BeginAsyncObserved(parent context.Context, o AsyncOrigin, observe func() StandingState) (*AsyncAdmission, error) {
	if a == nil || parent == nil || parent.Err() != nil || observe == nil {
		return nil, ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	if !a.allowsStanding(o, observe()) {
		return nil, ErrDenied
	}
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	if a.maxRegistered <= 0 || len(a.registrations)+len(a.asyncRegistrations) >= a.maxRegistered {
		return nil, ErrCapacity
	}
	ctx, cancel := context.WithCancel(parent)
	call := &AsyncAdmission{authority: a, origin: o, ctx: ctx, cancel: cancel}
	if a.asyncRegistrations == nil {
		a.asyncRegistrations = make(map[*AsyncAdmission]struct{})
	}
	a.asyncRegistrations[call] = struct{}{}
	return call, nil
}

type AsyncAdmission struct {
	authority         *Authority
	origin            AsyncOrigin
	ctx               context.Context
	cancel            context.CancelFunc
	admitted, revoked bool
}

func (c *AsyncAdmission) Context() context.Context {
	if c != nil && c.ctx != nil {
		return c.ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// AdmitObserved checks fresh standing grants and the actual acquired generation.
// commit must CAS persisted row generation/state before recording an attempt.
func (c *AsyncAdmission) AdmitObserved(observe func() (StandingState, TargetState), commit func() error) error {
	if c == nil || c.authority == nil || observe == nil || commit == nil {
		return ErrDenied
	}
	a := c.authority
	a.gate.RLock()
	defer a.gate.RUnlock()
	s, target := observe()
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	_, registered := a.asyncRegistrations[c]
	if !registered || c.revoked || c.admitted || c.ctx.Err() != nil || !a.allowsStanding(c.origin, s) || !a.validTarget(c.origin.TargetID, target) {
		return ErrDenied
	}
	if err := commit(); err != nil {
		return err
	}
	c.admitted = true
	return nil
}

func (c *AsyncAdmission) Finish() {
	if c == nil || c.authority == nil {
		return
	}
	a := c.authority
	a.registrationMu.Lock()
	c.revoked = true
	delete(a.asyncRegistrations, c)
	a.registrationMu.Unlock()
	c.cancel()
}
