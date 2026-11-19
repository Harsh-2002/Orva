package workloadiam

import "context"

// RetireOwnerDeferred closes a UUID permanently in this authority after durable
// deletion preparation commits. Signal returned cancellations outside owner locks.
func (a *Authority) RetireOwnerDeferred(owner string, persist func() error) (func(), error) {
	if a == nil || !validUUID(owner) || persist == nil {
		return nil, ErrInvalid
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	if a.policies == nil {
		return nil, ErrInvalid
	}
	if err := persist(); err != nil {
		return nil, err
	}
	if a.retired == nil {
		a.retired = map[string]bool{}
	}
	a.retired[owner] = true
	delete(a.deactivated, owner)
	delete(a.policies, owner)
	return a.revokeOwnerRegistrationsUnderGate(owner), nil
}

func (a *Authority) revokeOwnerRegistrationsUnderGate(owner string) func() {
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	cancels := []context.CancelFunc{}
	for c := range a.registrations {
		if c.caller.Principal.FunctionID == owner || c.targetID == owner {
			c.revoked = true
			cancels = append(cancels, c.cancel)
		}
	}
	for c := range a.asyncRegistrations {
		if c.origin.CreatorID == owner || c.origin.TargetID == owner {
			c.revoked = true
			cancels = append(cancels, c.cancel)
		}
	}
	return func() {
		for _, cancel := range cancels {
			cancel()
		}
	}
}
