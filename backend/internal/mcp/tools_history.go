package mcp

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadhistory"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type historyArguments struct{ Request json.RawMessage }

func (*historyArguments) UseStrictRawJSON() {}
func (a *historyArguments) UnmarshalJSON(raw []byte) error {
	if !workloadhistory.ValidRequestJSON(raw) {
		return database.ErrHistoryInput
	}
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil || len(outer) != 1 || !workloadhistory.ValidRequestJSON(outer["request"]) {
		return database.ErrHistoryInput
	}
	a.Request = append(json.RawMessage(nil), outer["request"]...)
	return nil
}

func registerHistoryTools(rc *regCtx) {
	if rc.deps.History == nil {
		return
	}
	rc.group = "history"
	for _, spec := range []struct {
		name, op, description string
		read                  bool
	}{
		{"list_work_history", "list", "Inspect non-executable deleted-work metadata. READ required; original payload is unavailable.", true},
		{"get_work_history", "get", "Inspect one exact historical kind/UUID. Inspection is not a grant or executable work.", true},
		{"get_work_history_capacity", "capacity", "Inspect canonical metadata use/reservations and hard caps. Ninety days is purge eligibility, not TTL.", true},
		{"purge_work_history", "purge", "Explicitly purge eligible unreserved metadata with live ADMIN and confirm:true. Audited; no automatic expiry.", false},
		{"reissue_work_history", "reissue", "Create NEW OPERATOR-origin work after live ADMIN review. Requires fresh payload/config, exact NEW target UUID, reviewed:true and confirm:true. Never resumes/adopts an archive.", false},
	} {
		spec := spec
		perm := permAdmin
		if spec.read {
			perm = permRead
		}
		destructive := !spec.read
		regAddTool(rc, perm, &mcpsdk.Tool{Name: spec.name, Description: spec.description,
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"request"},
				"properties": map[string]any{"request": workloadhistory.RequestSchema(spec.op)}},
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: spec.read, DestructiveHint: &destructive}},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in historyArguments) (*mcpsdk.CallToolResult, any, error) {
				out, err := rc.deps.History.Execute(ctx, spec.op, in.Request)
				return nil, out, err
			})
	}
}
