package workloadiam

// PublicationRevisionUnderGate is an observation, valid only inside the shared gate.
func (a *Authority) PublicationRevisionUnderGate(owner string) uint64 {
	if a == nil || a.policies == nil || a.retired[owner] {
		return 0
	}
	return a.policies[owner].Metadata().Revision
}

// ChangeHostState serializes bounded, atomic readiness/lease state changes with
// admission. Cancellation, cleanup, I/O and earlier lock acquisition stay outside.
func (a *Authority) ChangeHostState(change func()) error {
	if a == nil || change == nil {
		return ErrInvalid
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	if a.policies == nil {
		return ErrInvalid
	}
	change()
	return nil
}
