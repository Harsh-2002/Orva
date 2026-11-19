//go:build linux

package mcp

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerManagedServerTools(rc *regCtx) {
	registerManagedOperationTools(rc)
	registerIAMTools(rc)
	registerConfigurationTools(rc)
	registerWorkloadLifecycleTools(rc)
	registerHistoryTools(rc)
	if rc.deps.AsyncEnabled && rc.deps.AsyncIAM != nil {
		registerAsyncJobTools(rc)
	}
	if rc.deps.CronEnabled && rc.deps.CronIAM != nil {
		registerIAMCronTools(rc)
	}
	if rc.deps.Triggers != nil {
		registerManagedExposureTools(rc)
	}
	if rc.deps.DeployIAM == nil {
		return
	}
	rc.group = "deployments"
	registerPublicationDeploy(rc)
	registerPublicationRollback(rc)
	regAddTool(rc, permAdmin, &mcpsdk.Tool{
		Name: "install_managed_deployment", Description: "Install the exact managed deployment UUID without approving policy or restarting suspended standing work.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"deployment_id"},
			"properties": map[string]any{"deployment_id": map[string]string{"type": "string", "format": "uuid"}}},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args managedInstallArgs) (*mcpsdk.CallToolResult, map[string]any, error) {
		artifact, err := rc.deps.DeployIAM.Install(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"artifact": artifact, "review_required": true}, nil
	})
}

type managedInstallArgs struct{ ID string }

func (*managedInstallArgs) UseStrictRawJSON() {}
func (a *managedInstallArgs) UnmarshalJSON(raw []byte) error {
	m, err := workloadpolicy.StrictObject(raw)
	if err != nil || len(m) != 1 || json.Unmarshal(m["deployment_id"], &a.ID) != nil || a.ID == "" {
		return database.ErrWorkloadArtifactInvalid
	}
	return nil
}
