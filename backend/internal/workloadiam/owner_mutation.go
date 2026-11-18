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
