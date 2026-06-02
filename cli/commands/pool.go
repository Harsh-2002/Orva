package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	"github.com/spf13/cobra"
)

// poolCmd exposes the per-function warm-pool autoscaler config to the
// terminal. It hits the same /api/v1/pool/config endpoints the dashboard's
// function settings use. The pool keeps min_warm sandboxes hot, scales up
// toward max_warm under load (one new worker per target_concurrency
// in-flight requests), and recycles workers idle longer than idle_ttl.
var poolCmd = &cobra.Command{
	Use:   "pool",
	Short: "View or tune warm-pool autoscaler config",
	Long: `View or tune the warm-sandbox autoscaler for a function.

The autoscaler keeps min_warm sandboxes hot at all times, scales up toward
max_warm under load (adding a worker per target_concurrency in-flight
requests), recycles workers idle longer than idle_ttl seconds, and — when
scale_to_zero is on — drops to zero warm workers when fully idle (trading a
cold start on the next request for zero idle cost).

Pool config is always per-function: pass --fn to target one.`,
	Example: `  # Show a function's pool config
  orva pool get --fn greeter

  # Keep two warm, cap at twenty, scale to zero when idle
  orva pool set --fn greeter --min-warm 2 --max-warm 20 --scale-to-zero

  # Only change the idle TTL; other fields keep their current values
  orva pool set --fn greeter --idle-ttl 300`,
}

var poolGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show a function's warm-pool config",
	Long: `Show the autoscaler config for a function: min_warm, max_warm, idle_ttl,
target_concurrency, and scale_to_zero.

If the function has no explicit override, the server reports it as
unconfigured (running on built-in defaults).`,
	Example: `  orva pool get --fn greeter
  orva pool get --fn greeter -o json`,
	Args: cobra.NoArgs,
	RunE: runPoolGet,
}

var poolSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Tune a function's warm-pool config",
	Long: `Tune the autoscaler for a function.

Only the flags you actually pass are sent; every omitted field keeps its
current value (the server treats this as a partial update). Changes apply to
new sandbox spawns; existing warm workers keep their behavior until recycled.`,
	Example: `  orva pool set --fn greeter --min-warm 1 --max-warm 10
  orva pool set --fn greeter --target-concurrency 5
  orva pool set --fn greeter --scale-to-zero
  orva pool set --fn greeter --scale-to-zero=false`,
	Args: cobra.NoArgs,
	RunE: runPoolSet,
}

func init() {
	poolGetCmd.Flags().String("fn", "", "function name or id (required)")
	_ = poolGetCmd.MarkFlagRequired("fn")

	poolSetCmd.Flags().String("fn", "", "function name or id (required)")
	poolSetCmd.Flags().Int("min-warm", 0, "minimum warm sandboxes kept hot")
	poolSetCmd.Flags().Int("max-warm", 0, "maximum warm sandboxes under load")
	poolSetCmd.Flags().Int("idle-ttl", 0, "seconds a worker may sit idle before recycling")
	poolSetCmd.Flags().Int("target-concurrency", 0, "in-flight requests per worker before scaling up")
	poolSetCmd.Flags().Bool("scale-to-zero", false, "drop to zero warm workers when fully idle")
	_ = poolSetCmd.MarkFlagRequired("fn")

	poolCmd.AddCommand(poolGetCmd, poolSetCmd)
}

func runPoolGet(cmd *cobra.Command, _ []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnArg, _ := cmd.Flags().GetString("fn")
	fnID, err := resolveFnID(client, fnArg)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/pool/config?function_id=" + url.QueryEscape(fnID))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	// An unconfigured function returns {function_id, configured:false}.
	var probe struct {
		Configured *bool `json:"configured"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Configured != nil && !*probe.Configured {
		infof(cmd, "function %q has no pool override — running on built-in defaults", fnArg)
		return nil
	}

	var cfg poolConfigView
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	printPoolConfig(cfg)
	return nil
}

func runPoolSet(cmd *cobra.Command, _ []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnArg, _ := cmd.Flags().GetString("fn")
	fnID, err := resolveFnID(client, fnArg)
	if err != nil {
		return err
	}

	// Send only the tunables the user actually changed — the server applies
	// them on top of the existing row (partial update).
	body := map[string]any{"function_id": fnID}
	if cmd.Flags().Changed("min-warm") {
		v, _ := cmd.Flags().GetInt("min-warm")
		body["min_warm"] = v
	}
	if cmd.Flags().Changed("max-warm") {
		v, _ := cmd.Flags().GetInt("max-warm")
		body["max_warm"] = v
	}
	if cmd.Flags().Changed("idle-ttl") {
		v, _ := cmd.Flags().GetInt("idle-ttl")
		body["idle_ttl_seconds"] = v
	}
	if cmd.Flags().Changed("target-concurrency") {
		v, _ := cmd.Flags().GetInt("target-concurrency")
		body["target_concurrency"] = v
	}
	if cmd.Flags().Changed("scale-to-zero") {
		v, _ := cmd.Flags().GetBool("scale-to-zero")
		body["scale_to_zero"] = v
	}
	if len(body) == 1 {
		return fmt.Errorf("nothing to set — pass at least one of --min-warm, --max-warm, --idle-ttl, --target-concurrency, --scale-to-zero")
	}

	resp, err := client.Put("/api/v1/pool/config", body)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var cfg poolConfigView
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	okf(cmd, "pool config updated for %q", fnArg)
	printPoolConfig(cfg)
	return nil
}

// poolConfigView mirrors database.PoolConfig's JSON shape.
type poolConfigView struct {
	FunctionID        string `json:"function_id"`
	MinWarm           int    `json:"min_warm"`
	MaxWarm           int    `json:"max_warm"`
	IdleTTLSeconds    int    `json:"idle_ttl_seconds"`
	TargetConcurrency int    `json:"target_concurrency"`
	ScaleToZero       bool   `json:"scale_to_zero"`
}

func printPoolConfig(cfg poolConfigView) {
	t := newTable("FIELD", "VALUE")
	t.row("min_warm", cfg.MinWarm)
	t.row("max_warm", cfg.MaxWarm)
	t.row("idle_ttl_seconds", cfg.IdleTTLSeconds)
	t.row("target_concurrency", cfg.TargetConcurrency)
	t.row("scale_to_zero", cfg.ScaleToZero)
	t.flush()
}
