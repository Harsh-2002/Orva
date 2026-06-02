package ai

import "testing"

// TestTryLockConv covers the per-conversation turn guard: one turn per
// conversation, independent across conversations, re-lockable after release.
// This is what prevents overlapping turns (double-send / chat-while-approving)
// from interleaving and corrupting message ordering.
func TestTryLockConv(t *testing.T) {
	m := &Manager{}

	if !m.tryLockConv("c1") {
		t.Fatal("first lock on c1 should succeed")
	}
	if m.tryLockConv("c1") {
		t.Fatal("second lock on c1 should fail while a turn is in flight")
	}
	if !m.tryLockConv("c2") {
		t.Fatal("a different conversation should lock independently of c1")
	}

	m.unlockConv("c1")
	if !m.tryLockConv("c1") {
		t.Fatal("c1 should be lockable again after unlock")
	}

	// c2 is still held; releasing it should also free it.
	m.unlockConv("c2")
	if !m.tryLockConv("c2") {
		t.Fatal("c2 should be lockable again after unlock")
	}
}
