package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var functionsCmd = &cobra.Command{
	Use:   "functions",
	Short: "Manage functions",
	Long:  "Create, list, get, and delete serverless functions.",
}

var functionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List functions",
	Long: `List functions on the server with their runtime, status, and version.

Results are paginated. The footer reports "showing N of TOTAL"; when more
exist than the current page, raise --limit or page with --offset so a script
or agent never silently misses functions.

  orva functions list
  orva functions list --limit 500
  orva functions list --limit 50 --offset 50
  orva functions list -o json | jq '.functions[].name'`,
	Args: cobra.NoArgs,
	RunE: runFunctionsList,
}

var functionsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new function",
	Long: `Create a new function. Name and runtime are required; resource limits and
network mode are optional and fall back to server defaults when omitted.

  orva functions create --name greeter --runtime node
  orva functions create --name fetcher --runtime python \
    --memory-mb 256 --timeout-ms 60000 --network-mode egress`,
	Args: cobra.NoArgs,
	RunE: runFunctionsCreate,
}

var functionsGetCmd = &cobra.Command{
	Use:   "get [name-or-id]",
	Short: "Get function details",
	Long: `Print the full function record as JSON (detail view).

  orva functions get greeter
  orva functions get greeter | jq .network_mode`,
	Args: cobra.ExactArgs(1),
	RunE: runFunctionsGet,
}

var functionsDeleteCmd = &cobra.Command{
	Use:   "delete [name-or-id]",
	Short: "Delete a function",
	Long: `Permanently delete a function and all of its deployments. Prompts for
confirmation unless --yes is set.

  orva functions delete greeter
  orva functions delete greeter --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runFunctionsDelete,
}

var functionsUpdateCmd = &cobra.Command{
	Use:   "update <name-or-id>",
	Short: "Update a function's configuration",
	Long: `Update a function's settings in place (a partial merge — only the flags you
pass are changed). This is how an agent or CI toggles egress, adjusts limits,
or pauses a function without a full redeploy.

Runtime cannot be changed here (it is fixed at create; deploy new code to
change behavior). --env replaces the entire env-var map.

  orva functions update greeter --network-mode egress
  orva functions update greeter --memory-mb 256 --timeout-ms 60000
  orva functions update greeter --status inactive
  orva functions update greeter --env API_URL=https://x --env DEBUG=1`,
	Args: cobra.ExactArgs(1),
	RunE: runFunctionsUpdate,
}

var functionsPurgeCacheCmd = &cobra.Command{
	Use:   "purge-cache <name-or-id>",
	Short: "Delete a function's cached npm/pip downloads",
	Long: `Delete the function's build cache — the npm and pip downloads Orva keeps so a
redeploy does not refetch every dependency. The cache is per-function and is
rebuilt by the next deploy, which will be slower once.

Use this to recover from a build that pulled a bad or malicious package: the
cache is the only place such bytes persist between deploys.

  orva functions purge-cache greeter
  orva functions purge-cache greeter --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runFunctionsPurgeCache,
}

func init() {
	functionsListCmd.Flags().Int("limit", 100, "maximum number of functions to return")
	functionsListCmd.Flags().Int("offset", 0, "number of functions to skip (pagination)")

	functionsCreateCmd.Flags().String("name", "", "function name (required)")
	functionsCreateCmd.Flags().String("runtime", "", "runtime: node or python (required)")
	functionsCreateCmd.Flags().Int("memory-mb", 0, "memory limit in MB (0 = server default)")
	functionsCreateCmd.Flags().Int("timeout-ms", 0, "invocation timeout in ms (0 = server default)")
	functionsCreateCmd.Flags().String("network-mode", "", "network mode: none | egress (empty = server default)")
	functionsCreateCmd.MarkFlagRequired("name")
	functionsCreateCmd.MarkFlagRequired("runtime")

	functionsUpdateCmd.Flags().String("name", "", "rename the function")
	functionsUpdateCmd.Flags().String("description", "", "function description")
	functionsUpdateCmd.Flags().String("entrypoint", "", "entrypoint file")
	functionsUpdateCmd.Flags().Int64("timeout-ms", 0, "invocation timeout in ms")
	functionsUpdateCmd.Flags().Int64("memory-mb", 0, "memory limit in MB")
	functionsUpdateCmd.Flags().Float64("cpus", 0, "CPU limit (cores)")
	functionsUpdateCmd.Flags().StringArray("env", nil, "env var KEY=VALUE (repeatable; replaces the whole env map)")
	functionsUpdateCmd.Flags().String("network-mode", "", "network mode: none | egress")
	functionsUpdateCmd.Flags().Int("max-concurrency", 0, "max concurrent invocations (0 = unlimited)")
	functionsUpdateCmd.Flags().String("concurrency-policy", "", "when at capacity: queue | reject")
	functionsUpdateCmd.Flags().String("auth-mode", "", "invocation auth: none | platform_key | signed")
	functionsUpdateCmd.Flags().Int("rate-limit-per-min", 0, "per-minute invocation rate limit (0 = none)")
	functionsUpdateCmd.Flags().String("status", "", "status: active | inactive")

	functionsCmd.AddCommand(functionsListCmd)
	functionsCmd.AddCommand(functionsCreateCmd)
	functionsCmd.AddCommand(functionsGetCmd)
	functionsCmd.AddCommand(functionsDeleteCmd)
	functionsCmd.AddCommand(functionsUpdateCmd)
	functionsCmd.AddCommand(functionsPurgeCacheCmd)
}

func runFunctionsList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	path := "/api/v1/functions"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := client.Get(path)
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var result struct {
		Functions []struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			Runtime   string    `json:"runtime"`
			Status    string    `json:"status"`
			Version   int       `json:"version"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"functions"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	t := newTable("ID", "NAME", "RUNTIME", "STATUS", "VERSION", "CREATED")
	for _, fn := range result.Functions {
		t.row(fn.ID, fn.Name, fn.Runtime, fn.Status, fn.Version, fn.CreatedAt.Format(time.DateTime))
	}
	t.flush()

	shown := len(result.Functions)
	if offset+shown < result.Total {
		infof(cmd, "\nShowing %d of %d (raise --limit or page with --offset %d)", shown, result.Total, offset+shown)
	} else {
		infof(cmd, "\nShowing %d of %d", shown, result.Total)
	}
	return nil
}

func runFunctionsCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	name, _ := cmd.Flags().GetString("name")
	runtime, _ := cmd.Flags().GetString("runtime")
	memoryMB, _ := cmd.Flags().GetInt("memory-mb")
	timeoutMS, _ := cmd.Flags().GetInt("timeout-ms")
	networkMode, _ := cmd.Flags().GetString("network-mode")

	body := map[string]any{
		"name":    name,
		"runtime": runtime,
	}
	if memoryMB > 0 {
		body["memory_mb"] = memoryMB
	}
	if timeoutMS > 0 {
		body["timeout_ms"] = timeoutMS
	}
	if networkMode != "" {
		body["network_mode"] = networkMode
	}

	resp, err := client.Post("/api/v1/functions", body)
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var fn struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Runtime string `json:"runtime"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(raw, &fn); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	okf(cmd, "Created function %s (%s, %s) — %s", fn.Name, fn.ID, fn.Runtime, fn.Status)
	return nil
}

func runFunctionsGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/functions/" + fnID)
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	// get is a detail view — always emit the single object as JSON.
	return emitRaw(raw)
}

func runFunctionsDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	if err := confirm(cmd, fmt.Sprintf("Delete function %q and all its deployments?", args[0])); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/functions/" + fnID)
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) && len(raw) > 0 {
		return emitRaw(raw)
	}
	okf(cmd, "Deleted function %s", fnID)
	return nil
}

func runFunctionsPurgeCache(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	if err := confirm(cmd, fmt.Sprintf("Purge the build cache for %q? The next deploy refetches its dependencies.", args[0])); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/build-cache")
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) && len(raw) > 0 {
		return emitRaw(raw)
	}
	okf(cmd, "Purged build cache for %s", args[0])
	return nil
}

func runFunctionsUpdate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	// Partial update: only send fields whose flag was explicitly set, so an
	// omitted flag leaves the server's value untouched (the REST struct is
	// all-pointers/merge).
	body := map[string]any{}
	f := cmd.Flags()
	setStr := func(flag, json string) {
		if f.Changed(flag) {
			v, _ := f.GetString(flag)
			body[json] = v
		}
	}
	setStr("name", "name")
	setStr("description", "description")
	setStr("entrypoint", "entrypoint")
	setStr("network-mode", "network_mode")
	setStr("concurrency-policy", "concurrency_policy")
	setStr("auth-mode", "auth_mode")
	setStr("status", "status")
	if f.Changed("timeout-ms") {
		v, _ := f.GetInt64("timeout-ms")
		body["timeout_ms"] = v
	}
	if f.Changed("memory-mb") {
		v, _ := f.GetInt64("memory-mb")
		body["memory_mb"] = v
	}
	if f.Changed("cpus") {
		v, _ := f.GetFloat64("cpus")
		body["cpus"] = v
	}
	if f.Changed("max-concurrency") {
		v, _ := f.GetInt("max-concurrency")
		body["max_concurrency"] = v
	}
	if f.Changed("rate-limit-per-min") {
		v, _ := f.GetInt("rate-limit-per-min")
		body["rate_limit_per_min"] = v
	}
	if f.Changed("env") {
		pairs, _ := f.GetStringArray("env")
		env := map[string]string{}
		for _, p := range pairs {
			k, v, ok := strings.Cut(p, "=")
			if !ok {
				return fmt.Errorf("update: --env %q must be KEY=VALUE", p)
			}
			env[k] = v
		}
		body["env_vars"] = env
	}

	if len(body) == 0 {
		return fmt.Errorf("update: nothing to change — pass at least one field flag (see --help)")
	}

	resp, err := client.Put("/api/v1/functions/"+fnID, body)
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if outputJSON(cmd) {
		return emitRaw(raw)
	}
	okf(cmd, "Updated function %s", args[0])
	return nil
}
