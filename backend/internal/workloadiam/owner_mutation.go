package workloadiam

// MutateOwners checks installed authority without republishing unchanged policy.
// The caller holds sorted owner locks; persist must not call back into Authority.
func (a *Authority) MutateOwners(expected []Metadata, persist func() error) error {
	if a == nil || persist == nil || len(expected) == 0 || len(expected) > 2 {
		return ErrInvalid
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	seen := map[string]bool{}
	for _, m := range expected {
		e := a.policies[m.OwnerID]
		if e == nil || seen[m.OwnerID] || a.retired[m.OwnerID] || e.Metadata() != m {
			return ErrConflict
		}
		seen[m.OwnerID] = true
	}
	return persist()
}

// DeactivateOwnerDeferred denies admission without deleting policy metadata.
// persist must close the current host mapping after durable commit. The caller
// signals returned cancellations outside the owner lock.
func (a *Authority) DeactivateOwnerDeferred(owner string, revision uint64, persist func() error) (func(), error) {
	if a == nil || !validUUID(owner) || revision == 0 || persist == nil {
		return nil, ErrInvalid
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	e := a.policies[owner]
	if e == nil || a.retired[owner] || e.Metadata().Revision != revision {
		return nil, ErrConflict
	}
	if err := persist(); err != nil {
		return nil, err
	}
	if a.deactivated == nil {
		a.deactivated = make(map[string]bool)
	}
	a.deactivated[owner] = true
	return a.revokeOwnerRegistrationsUnderGate(owner), nil
}

// ResumeOwnerDeferred replaces deactivation only with an exact deny successor
// after durable publication. Signal cancellations outside the owner lock.
func (a *Authority) ResumeOwnerDeferred(expected Metadata, candidate *Evaluator, persist func() error) (func(), error) {
	if a == nil || persist == nil || candidate == nil || !candidate.deny {
		return nil, ErrInvalid
	}
	frozen, err := copyEvaluator(candidate)
	if err != nil {
		return nil, err
	}
	next := expected
	next.Revision++
	if expected.Revision == 0 || next.Revision == 0 || frozen.Metadata() != next {
		return nil, ErrConflict
	}
	owner := expected.OwnerID
	a.gate.Lock()
	defer a.gate.Unlock()
	if a.retired[owner] || !a.deactivated[owner] || a.policies[owner].Metadata() != expected {
		return nil, ErrConflict
	}
	if err := persist(); err != nil {
		return nil, err
	}
	a.policies[owner] = frozen
	delete(a.deactivated, owner)
	return a.revokeOwnerRegistrationsUnderGate(owner), nil
}
