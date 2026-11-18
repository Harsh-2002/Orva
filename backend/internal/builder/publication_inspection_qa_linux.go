//go:build linux && orva_publication_qa

package builder

// InspectionPendingForQA is compiled only by the explicit disposable QA profile.
func (s *PublicationQueue) InspectionPendingForQA(id string) (int, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	_, ok := s.pending[id]
	return len(s.pending), ok
}

// ReleaseFailedPinForQA drops only a terminal failed job's inspection pin.
// It neither retires output nor installs a Factory or approval.
func (s *PublicationQueue) ReleaseFailedPinForQA(id string) bool {
	dep, err := s.queue.db.GetDeployment(id)
	if err != nil || dep.Status != "failed" {
		return false
	}
	s.pendingMu.Lock()
	paths := s.pending[id]
	delete(s.pending, id)
	s.pendingMu.Unlock()
	if paths == nil {
		return false
	}
	paths.Release()
	return true
}
