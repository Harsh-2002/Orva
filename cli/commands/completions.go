package commands

import (
	"net/url"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

// completionClient returns a client with a short timeout so a tab-completion
// (`__complete`) can never block the user's shell on an unreachable endpoint
// (the normal 120s client timeout would be a terrible completion experience).
func completionClient(cmd *cobra.Command) (*cli.Client, bool) {
	c, err := getClient(cmd)
	if err != nil {
		return nil, false
	}
	c.HTTP.Timeout = 2 * time.Second
	return c, true
}

// This file wires dynamic shell completion onto the command tree. Completion
// functions run in a short-lived `__complete` invocation of the binary, so they
// talk to the live instance (using the same config/flags as normal commands)
// to suggest real resource names — function names, runtimes, models — instead
// of only static subcommand names.
//
// Wiring happens in wireCompletions (called from RegisterClient) rather than in
// per-file init() funcs: that way every command's flags are already registered,
// so RegisterFlagCompletionFunc never races init ordering.

// fixedCompletion returns a completion func that always offers the given static
// values (and disables file completion). Used for enum flags.
func fixedCompletion(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeFunctionNames suggests existing function names for the FIRST
// positional argument only (so it doesn't mis-suggest names for a trailing
// deployment id). Best-effort: any failure yields no suggestions, never an error.
func completeFunctionNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client, ok := completionClient(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	resp, err := client.Get("/api/v1/functions?limit=10000")
	if err != nil || checkResponse(resp) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out struct {
		Functions []struct {
			Name string `json:"name"`
		} `json:"functions"`
	}
	if decodeJSON(resp, &out) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(out.Functions))
	for _, f := range out.Functions {
		names = append(names, f.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeRuntimes suggests the runtime ids the instance supports.
func completeRuntimes(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	client, ok := completionClient(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	resp, err := client.Get("/api/v1/runtimes")
	if err != nil || checkResponse(resp) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	if decodeJSON(resp, &out) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ids := make([]string, 0, len(out.Runtimes))
	for _, r := range out.Runtimes {
		ids = append(ids, r.ID)
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

// completeChatModels suggests model ids of the operator's active provider for
// `orva chat --model`.
func completeChatModels(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	client, ok := completionClient(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	settingsResp, err := client.Get("/api/v1/ai/settings")
	if err != nil || checkResponse(settingsResp) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var st struct {
		Settings struct {
			ActiveProviderID string `json:"active_provider_id"`
		} `json:"settings"`
	}
	if decodeJSON(settingsResp, &st) != nil || st.Settings.ActiveProviderID == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	resp, err := client.Get("/api/v1/ai/providers/" + url.PathEscape(st.Settings.ActiveProviderID) + "/models")
	if err != nil || checkResponse(resp) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if decodeJSON(resp, &out) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ids := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		ids = append(ids, m.ID)
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

// wireCompletions attaches dynamic completions to the command tree. Commands
// are resolved via root.Find so this stays decoupled from how each subcommand
// var is named/scoped.
func wireCompletions(root *cobra.Command) {
	// First positional = function name.
	for _, path := range [][]string{
		{"invoke"}, {"diff"}, {"rollback"}, {"logs"},
		{"functions", "get"}, {"functions", "delete"}, {"functions", "update"},
		{"deployments", "list"}, {"deployments", "get"}, {"deployments", "logs"},
	} {
		if c, _, err := root.Find(path); err == nil && c.Name() == path[len(path)-1] {
			c.ValidArgsFunction = completeFunctionNames
		}
	}

	// --fn flag = function name.
	for _, path := range [][]string{
		{"pool", "get"}, {"pool", "set"}, {"cron", "create"}, {"jobs", "list"},
	} {
		if c, _, err := root.Find(path); err == nil {
			_ = c.RegisterFlagCompletionFunc("fn", completeFunctionNames)
		}
	}

	// --function flag = function name (executions filters).
	for _, path := range [][]string{
		{"executions", "list"}, {"executions", "prune"},
	} {
		if c, _, err := root.Find(path); err == nil {
			_ = c.RegisterFlagCompletionFunc("function", completeFunctionNames)
		}
	}

	// Enum + resource flags.
	if c, _, err := root.Find([]string{"deploy"}); err == nil {
		_ = c.RegisterFlagCompletionFunc("runtime", completeRuntimes)
	}
	if c, _, err := root.Find([]string{"chat"}); err == nil {
		_ = c.RegisterFlagCompletionFunc("model", completeChatModels)
	}
	// Global --output enum.
	_ = root.RegisterFlagCompletionFunc("output", fixedCompletion("table", "json"))
}
