package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
)

func TestPublicationAgentRawAndRequiredUnavailable(t *testing.T) {
	agent := BuildAgentRegistry(Deps{RequireDeployIAM: true}, permSet{permWrite: true})
	for _, raw := range []string{
		`{"function_id":"id","code":"x","wait":false,"epoch":"claim"}`,
		`{"function_id":"id","code":"x","wait":false,"wait":true}`,
		`{"function_id":"id","code":"\ud800","wait":false}`,
	} {
		if _, err := agent.Dispatch(t.Context(), "deploy_function_inline", json.RawMessage(raw)); err == nil || errors.Is(err, builder.ErrPublicationUnavailable) {
			t.Fatalf("raw metadata/ambiguity was not rejected before service: %v", err)
		}
	}
	_, err := agent.Dispatch(t.Context(), "deploy_function_inline", json.RawMessage(`{"function_id":"id","code":"x","wait":false}`))
	if !errors.Is(err, builder.ErrPublicationUnavailable) {
		t.Fatalf("required nil mode fell back: %v", err)
	}
}
