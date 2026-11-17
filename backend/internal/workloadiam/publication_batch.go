package workloadiam

import "context"

const MaxPublicationOwners = 25

type Publication struct {
	Expected  uint64
	Candidate *Evaluator
}

// PublishBatch commits once, installs every snapshot, and marks touching calls
// revoked before unlocking. Context callbacks run only after the gate is released.
func (a *Authority) PublishBatch(batch []Publication, persist func() error) error {
	cancel, err := a.PublishBatchDeferred(batch, persist)
	if err != nil {
		return err
	}
	cancel()
	return nil
}

// PublishBatchDeferred separates marking revocation from signalling contexts so
// a host can release earlier owner locks before callbacks. Invoke cancel before ACK.
func (a *Authority) PublishBatchDeferred(batch []Publication, persist func() error) (cancel func(), err error) {
	if a == nil || persist == nil || len(batch) > MaxPublicationOwners {
		return nil, ErrInvalid
	}
	frozen := make(map[string]*Evaluator, len(batch))
	expected := make(map[string]uint64, len(batch))
	for _, item := range batch {
		e, err := copyEvaluator(item.Candidate)
		if err != nil {
			return nil, err
		}
		if _, duplicate := frozen[e.metadata.OwnerID]; duplicate {
			return nil, ErrInvalid
		}
		if item.Expected == ^uint64(0) || e.metadata.Revision != item.Expected+1 {
			return nil, ErrConflict
		}
		frozen[e.metadata.OwnerID] = e
		expected[e.metadata.OwnerID] = item.Expected
	}
	a.gate.Lock()
	locked := true
	defer func() {
		if locked {
			a.gate.Unlock()
		}
	}()
	if a.policies == nil {
		return nil, ErrInvalid
	}
	for owner, revision := range expected {
		if a.policies[owner].Metadata().Revision != revision {
			return nil, ErrConflict
		}
	}
	if err := persist(); err != nil {
		return nil, err
	}
	for owner, e := range frozen {
		a.policies[owner] = e
	}
	a.registrationMu.Lock()
	cancelled := make([]context.CancelFunc, 0, len(a.registrations))
	for call := range a.registrations {
		if frozen[call.caller.Principal.FunctionID] != nil || frozen[call.targetID] != nil {
			call.revoked = true
			cancelled = append(cancelled, call.cancel)
		}
	}
	for call := range a.asyncRegistrations {
		if frozen[call.origin.CreatorID] != nil || frozen[call.origin.TargetID] != nil {
			call.revoked = true
			cancelled = append(cancelled, call.cancel)
		}
	}
	a.registrationMu.Unlock()
	locked = false
	a.gate.Unlock()
	return func() {
		for _, cancel := range cancelled {
			cancel()
		}
	}, nil
}
