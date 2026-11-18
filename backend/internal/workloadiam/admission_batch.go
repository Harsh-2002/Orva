package workloadiam

// DoBatchObserved authorizes every bounded KV request against one snapshot
// before any effect, retaining admission through the synchronous transaction.
// Its observer and effect obey DoObserved's non-reentry and lock-order contract.
func (a *Authority) DoBatchObserved(observe func() Context, requests []Request, effect func() error) error {
	if a == nil || observe == nil || effect == nil {
		return ErrDenied
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	c := observe()
	if !a.admissionPolicy(c.Principal.FunctionID).AllowsBatch(c, requests) {
		return ErrDenied
	}
	return effect()
}
