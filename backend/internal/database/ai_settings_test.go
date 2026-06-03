package database

import "testing"

// TestSetAISelectionRoundTrip covers the cross-device selection memory: a saved
// provider/model is read back intact, and switching providers remembers a model
// per provider (the provider_models map accumulates rather than replaces).
func TestSetAISelectionRoundTrip(t *testing.T) {
	db := newTestDB(t)

	if _, err := db.SetAISelection("default", "prov-1", "openai", "gpt-x", "standard"); err != nil {
		t.Fatalf("SetAISelection: %v", err)
	}
	s, ok, err := db.GetSettings("default")
	if err != nil || !ok {
		t.Fatalf("GetSettings: ok=%v err=%v", ok, err)
	}
	if s.ActiveProviderID != "prov-1" || s.Model != "gpt-x" || s.Provider != "openai" {
		t.Errorf("got active=%q provider=%q model=%q", s.ActiveProviderID, s.Provider, s.Model)
	}
	if s.ProviderModels["prov-1"] != "gpt-x" {
		t.Errorf("provider_models = %v, want prov-1→gpt-x", s.ProviderModels)
	}
	if s.ThinkingLevel != "standard" {
		t.Errorf("thinking_level = %q, want standard", s.ThinkingLevel)
	}

	// Switch to a second provider — the first provider's model must still be
	// remembered (per-provider memory, not a single overwrite). Empty thinking
	// leaves the prior level untouched.
	if _, err := db.SetAISelection("default", "prov-2", "anthropic", "claude-y", ""); err != nil {
		t.Fatalf("SetAISelection 2: %v", err)
	}
	s, _, _ = db.GetSettings("default")
	if s.ActiveProviderID != "prov-2" || s.Model != "claude-y" {
		t.Errorf("active selection not updated: active=%q model=%q", s.ActiveProviderID, s.Model)
	}
	if s.ProviderModels["prov-1"] != "gpt-x" || s.ProviderModels["prov-2"] != "claude-y" {
		t.Errorf("per-provider memory lost: %v", s.ProviderModels)
	}
	if s.ThinkingLevel != "standard" {
		t.Errorf("empty thinking should preserve prior level, got %q", s.ThinkingLevel)
	}
}

// TestUpsertSettingsPreservesSelectionColumns confirms a general settings write
// that carries the selection fields persists them, and that GetSettings returns
// a non-nil ProviderModels map even when never set.
func TestUpsertSettingsPreservesSelectionColumns(t *testing.T) {
	db := newTestDB(t)

	// Fresh row: map is empty (non-nil) and active id blank.
	s, _, err := db.GetSettings("default")
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if s.ProviderModels == nil {
		t.Error("ProviderModels should decode to a non-nil empty map")
	}

	s.ID = "default"
	s.ActiveProviderID = "p9"
	s.ProviderModels = map[string]string{"p9": "m9"}
	s.ApprovalPolicy = "auto"
	if err := db.UpsertSettings(s); err != nil {
		t.Fatalf("UpsertSettings: %v", err)
	}
	got, _, _ := db.GetSettings("default")
	if got.ActiveProviderID != "p9" || got.ProviderModels["p9"] != "m9" || got.ApprovalPolicy != "auto" {
		t.Errorf("round-trip mismatch: active=%q map=%v policy=%q",
			got.ActiveProviderID, got.ProviderModels, got.ApprovalPolicy)
	}
}
