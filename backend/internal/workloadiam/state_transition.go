package workloadiam

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
