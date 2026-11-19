//go:build linux

package artifactfs

import "context"

// InstanceID is the verified store association, not a cgroup epoch or proof.
func (o *Owner) InstanceID(ctx context.Context) (string, error) {
	if _, err := o.Bindings(ctx); err != nil {
		return "", err
	}
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	if o.s.closing || o.s.closed {
		return "", ErrClosed
	}
	return o.s.disk.ID, nil
}
