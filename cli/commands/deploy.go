package commands

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
	"io/fs"
	"sort"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy a function",
	Long: `Package and deploy a function from the given directory path.

When --entrypoint is not provided and the function does not yet exist, the CLI
auto-detects the entrypoint:
  - If the source dir contains a tsconfig.json AND a handler.ts (or another
    .ts file when handler.ts is missing), the entrypoint defaults to that
    .ts file (e.g. handler.ts) so the validator passes before tsc runs.
  - Otherwise the server's runtime default is used (handler.js / handler.py).

Examples:
  orva deploy ./src --name greeter --runtime node
  orva deploy ./src --name greeter --runtime node --follow   # stream build logs`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploy,
}

func init() {
	deployCmd.Flags().String("name", "", "function name (required)")
	deployCmd.Flags().String("runtime", "", "runtime: node or python (required)")
	deployCmd.Flags().String("entrypoint", "", "entrypoint file (optional; auto-detects handler.ts when tsconfig.json + handler.ts present)")
	// --follow/-f matches logs/activity/deployments-logs; --watch is the
	// original name, kept as a hidden alias for back-compat.
	deployCmd.Flags().BoolP("follow", "f", false, "stream build logs and wait for the deploy to finish (non-zero exit on build failure)")
	deployCmd.Flags().Bool("watch", false, "deprecated alias for --follow")
	_ = deployCmd.Flags().MarkHidden("watch")
	deployCmd.MarkFlagRequired("name")
	deployCmd.MarkFlagRequired("runtime")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	srcPath := args[0]
	name, _ := cmd.Flags().GetString("name")
	runtime, _ := cmd.Flags().GetString("runtime")
	entrypoint, _ := cmd.Flags().GetString("entrypoint")
	watch, _ := cmd.Flags().GetBool("follow")
	if w, _ := cmd.Flags().GetBool("watch"); w {
		watch = true // honor the deprecated --watch alias
	}

	// Verify the source path exists.
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("path %s: %w", srcPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %s is not a directory", srcPath)
	}

	// Auto-detect TypeScript projects so the function row gets created with
	// `handler.ts` (or whatever the user's main TS file is) — otherwise the
	// validator looks for the runtime default `handler.js` BEFORE tsc has
	// emitted anything and fails the first deploy. Only kicks in when the
	// caller didn't pass --entrypoint explicitly.
	if entrypoint == "" {
		if detected := detectTSEntrypoint(srcPath); detected != "" {
			entrypoint = detected
			infof(cmd, "Detected TypeScript project, using entrypoint %q", entrypoint)
		}
	}

	// Resolve or create the function.
	fnID, err := resolveOrCreateFunction(cmd, client, name, runtime, entrypoint)
	if err != nil {
		return err
	}
	infof(cmd, "Deploying to function %s...", fnID)

	// Create tar.gz archive.
	archivePath, err := createArchive(srcPath)
	// Register the cleanup BEFORE the error check: createArchive creates its
	// temp file first and can fail after, so every failed deploy used to
	// leave a partial orva-deploy-*.tar.gz behind in /tmp.
	if archivePath != "" {
		defer os.Remove(archivePath)
	}
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}

	// Upload via multipart POST.
	resp, err := uploadDeploy(client, fnID, archivePath)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	var dep struct {
		ID           string `json:"id"`
		DeploymentID string `json:"deployment_id"`
	}
	_ = json.Unmarshal(raw, &dep)
	depID := dep.ID
	if depID == "" {
		depID = dep.DeploymentID
	}

	if watch && depID != "" {
		if err := watchBuild(cmd, client, depID); err != nil {
			// Emit the deployment record before returning the build error so
			// callers still get the machine-readable row.
			if outputJSON(cmd) {
				_ = emitRaw(raw)
			}
			return err
		}
	}

	okf(cmd, "Deploy submitted (deployment %s)", depID)
	return emitRaw(raw)
}

// watchBuild streams a deployment's build logs over SSE until a terminal event
// arrives. Log lines go to stderr (progress), so stdout stays clean for the
// final deployment record. Returns a non-nil error if the build fails.
func watchBuild(cmd *cobra.Command, client *cli.Client, depID string) error {
	resp, err := streamSSE(client, "/api/v1/deployments/"+depID+"/stream")
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer resp.Body.Close()

	infof(cmd, "Streaming build logs for deployment %s — Ctrl-C to stop.", depID)

	terminal := false
	cerr := consumeSSE(resp, func(event, data string) (bool, error) {
		switch event {
		case "log":
			var l struct {
				Line string `json:"line"`
			}
			if json.Unmarshal([]byte(data), &l) == nil {
				fmt.Fprintln(os.Stderr, l.Line)
			}
		case "succeeded":
			terminal = true
			okf(cmd, "Build succeeded.")
			return true, nil
		case "failed", "error":
			terminal = true
			var e struct {
				Message      string `json:"message"`
				ErrorMessage string `json:"error_message"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			msg := e.Message
			if msg == "" {
				msg = e.ErrorMessage
			}
			if msg == "" {
				msg = event
			}
			return true, fmt.Errorf("build failed: %s", msg)
		}
		return false, nil
	})
	if cerr != nil {
		return cerr
	}
	if !terminal {
		// Clean stream close with no succeeded/failed event (e.g. a reverse-proxy
		// idle timeout mid-build). Don't report a success we never actually saw.
		return fmt.Errorf("build stream ended before a result was reported; check `orva deployments get %s`", depID)
	}
	return nil
}

func resolveOrCreateFunction(cmd *cobra.Command, client *cli.Client, name, runtime, entrypoint string) (string, error) {
	// Try to find existing function by name.
	// High limit: the list endpoint defaults to 20, which would make deploy-by-name
	// miss an existing function on larger instances and wrongly create a duplicate.
	resp, err := client.Get("/api/v1/functions?limit=10000")
	if err == nil && resp.StatusCode == http.StatusOK {
		var result struct {
			Functions []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"functions"`
		}
		if decodeJSON(resp, &result) == nil {
			for _, fn := range result.Functions {
				if fn.Name != name {
					continue
				}
				// --entrypoint and --runtime were applied only on the CREATE
				// path below, so redeploying an existing function with a
				// changed value silently kept the old one and exited 0. Apply
				// them here too.
				if err := applyDeployOverrides(cmd, client, fn.ID, runtime, entrypoint); err != nil {
					return "", err
				}
				return fn.ID, nil
			}
		}
	}

	// Function doesn't exist, create it.
	infof(cmd, "Function %q not found, creating...", name)
	body := map[string]string{
		"name":    name,
		"runtime": runtime,
	}
	if entrypoint != "" {
		body["entrypoint"] = entrypoint
	}
	resp, err = client.Post("/api/v1/functions", body)
	if err != nil {
		return "", fmt.Errorf("create function: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return "", fmt.Errorf("create function: %w", err)
	}

	var fn struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(resp, &fn); err != nil {
		return "", fmt.Errorf("decode create response: %w", err)
	}
	infof(cmd, "Created function %s", fn.ID)
	return fn.ID, nil
}

// detectTSEntrypoint inspects the source dir for a TypeScript project shape
// and returns the .ts file the validator should look for at first-deploy time.
// Returns "" when the dir is not a TS project (no tsconfig.json) or when no
// candidate .ts source is present at the top level.
//
// Preference order:
//  1. handler.ts — the canonical Orva starter name.
//  2. The first *.ts file at the top level (excluding *.d.ts) when handler.ts
//     is absent. Skips dist/ etc. by only walking the top level.
//
// We deliberately don't try to read tsconfig.json's `files` / `include`
// entries — those can be globs that need a real TS resolver, and the
// auto-detect is best-effort: if it picks the wrong file the user can
// override via --entrypoint.
func detectTSEntrypoint(srcDir string) string {
	if _, err := os.Stat(filepath.Join(srcDir, "tsconfig.json")); err != nil {
		return ""
	}
	// Prefer handler.ts when present.
	if _, err := os.Stat(filepath.Join(srcDir, "handler.ts")); err == nil {
		return "handler.ts"
	}
	// Otherwise pick the first *.ts at the top level (skipping *.d.ts).
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".d.ts") {
			continue
		}
		if strings.HasSuffix(name, ".ts") {
			return name
		}
	}
	return ""
}

func createArchive(srcDir string) (string, error) {
	tmpFile, err := os.CreateTemp("", "orva-deploy-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	gzw := gzip.NewWriter(tmpFile)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	srcDir, err = filepath.Abs(srcDir)
	if err != nil {
		return "", err
	}

	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden dirs and common ignores.
		name := info.Name()
		if info.IsDir() && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__") {
			return filepath.SkipDir
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// filepath.Walk uses Lstat, so a symlink arrives as a symlink, and
		// FileInfoHeader emits a zero-size TypeSymlink header. The original
		// code then fell through to Open+Copy and wrote the target's CONTENT
		// into that zero-size entry: "archive/tar: write too long", which
		// aborted the whole deploy. A lib.js -> shared.js link is ordinary in
		// a JS project.
		//
		// DEREFERENCE rather than archiving the link, which is what `tar -h`
		// does. Packing links would mean the server has to recreate them, and
		// recreating a link from an untrusted archive is genuinely unsafe:
		// lexical containment cannot see through a chain, so an early link
		// entry can redirect a later write outside the extraction root. The
		// builder therefore refuses link entries outright, and dereferencing
		// here is what keeps ordinary projects deployable.
		if info.Mode()&fs.ModeSymlink != 0 {
			resolved, err := os.Stat(path) // follows the link
			if err != nil {
				return fmt.Errorf("symlink %s is broken: %w", relPath, err)
			}
			if resolved.IsDir() {
				// Walking through it could duplicate a whole tree, or loop
				// forever on a cycle. Say so rather than silently omitting it.
				return fmt.Errorf(
					"%s is a symlink to a directory, which deploy cannot pack; "+
						"replace it with the directory itself or exclude it", relPath)
			}
			hdr, err := tar.FileInfoHeader(resolved, "")
			if err != nil {
				return err
			}
			hdr.Name = relPath
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			// Sockets, devices and FIFOs have no meaningful archive form.
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})

	return tmpFile.Name(), err
}

func uploadDeploy(client *cli.Client, fnID, archivePath string) (*http.Response, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer writer.Close()

		part, err := writer.CreateFormFile("code", filepath.Base(archivePath))
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		f, err := os.Open(archivePath)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		defer f.Close()

		if _, err := io.Copy(part, f); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	url := client.BaseURL + "/api/v1/functions/" + fnID + "/deploy"
	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if client.APIKey != "" {
		req.Header.Set("X-Orva-API-Key", client.APIKey)
	}

	return client.HTTP.Do(req)
}

// applyDeployOverrides PATCHes runtime/entrypoint onto an existing function
// when the caller passed them explicitly. Only sends what was set, so a
// plain `orva deploy` never rewrites configuration the operator did not ask
// to change.
func applyDeployOverrides(cmd *cobra.Command, client *cli.Client, fnID, runtime, entrypoint string) error {
	body := map[string]string{}
	if cmd.Flags().Changed("runtime") && runtime != "" {
		body["runtime"] = runtime
	}
	if cmd.Flags().Changed("entrypoint") && entrypoint != "" {
		body["entrypoint"] = entrypoint
	}
	if len(body) == 0 {
		return nil
	}
	resp, err := client.Put("/api/v1/functions/"+fnID, body)
	if err != nil {
		return fmt.Errorf("apply deploy overrides: %w", err)
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return fmt.Errorf("apply deploy overrides: %w", err)
	}
	infof(cmd, "Updated %s on the existing function.", strings.Join(keysOf(body), " and "))
	return nil
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
