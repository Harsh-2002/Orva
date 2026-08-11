package ai

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

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

func TestDeleteAllConversationsRejectsActiveTurn(t *testing.T) {
	m := &Manager{}
	if !m.tryLockConv("c1") {
		t.Fatal("lock c1")
	}
	defer m.unlockConv("c1")

	if _, err := m.DeleteAllConversations(); !errors.Is(err, ErrConversationBusy) {
		t.Fatalf("DeleteAllConversations error = %v, want ErrConversationBusy", err)
	}
}

func TestSaveProviderRequiresOllamaBaseURL(t *testing.T) {
	m := &Manager{}
	for _, baseURL := range []string{"", "   ", "\t\n"} {
		_, err := m.SaveProvider(ProviderInput{Provider: " OLLAMA ", BaseURL: baseURL})
		if err == nil || !strings.Contains(err.Error(), "base_url is required for ollama") {
			t.Errorf("SaveProvider(ollama, %q) error = %v, want required-base-url error", baseURL, err)
		}
	}
}

func TestToolIterationBudgetIsFixedInternally(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m := &Manager{db: db}
	in := database.AISettings{
		ThinkingLevel:     "standard",
		ApprovalPolicy:    "all_writes",
		MaxToolIterations: 999,
	}
	got, err := m.SaveSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxToolIterations != maxToolIterationsPerTurn {
		t.Fatalf("MaxToolIterations = %d, want fixed internal budget %d", got.MaxToolIterations, maxToolIterationsPerTurn)
	}

	stored, _, err := db.GetSettings("default")
	if err != nil {
		t.Fatal(err)
	}
	if stored.MaxToolIterations != maxToolIterationsPerTurn {
		t.Fatalf("stored MaxToolIterations = %d, want %d", stored.MaxToolIterations, maxToolIterationsPerTurn)
	}
}
