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
