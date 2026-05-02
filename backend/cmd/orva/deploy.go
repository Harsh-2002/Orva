package main

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

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/spf13/cobra"
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
  - Otherwise the server's runtime default is used (handler.js / handler.py).`,
	Args: cobra.ExactArgs(1),
	Run:  runDeploy,
}

func init() {
	deployCmd.Flags().String("name", "", "function name (required)")
	deployCmd.Flags().String("runtime", "", "runtime (node24, node22, python314, python313) (required)")
	deployCmd.Flags().String("entrypoint", "", "entrypoint file (optional; auto-detects handler.ts when tsconfig.json + handler.ts present)")
	deployCmd.MarkFlagRequired("name")
	deployCmd.MarkFlagRequired("runtime")
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	srcPath := args[0]
	name, _ := cmd.Flags().GetString("name")
	runtime, _ := cmd.Flags().GetString("runtime")
	entrypoint, _ := cmd.Flags().GetString("entrypoint")

	// Verify the source path exists.
	info, err := os.Stat(srcPath)
	if err != nil {
		exitError("path %s: %v", srcPath, err)
	}
	if !info.IsDir() {
		exitError("path %s is not a directory", srcPath)
	}

	// Auto-detect TypeScript projects so the function row gets created with
	// `handler.ts` (or whatever the user's main TS file is) — otherwise the
	// validator looks for the runtime default `handler.js` BEFORE tsc has
	// emitted anything and fails the first deploy. Only kicks in when the
	// caller didn't pass --entrypoint explicitly.
	if entrypoint == "" {
		if detected := detectTSEntrypoint(srcPath); detected != "" {
			entrypoint = detected
			fmt.Printf("Detected TypeScript project, using entrypoint %q\n", entrypoint)
		}
	}

	// Resolve or create the function.
	fnID := resolveOrCreateFunction(client, name, runtime, entrypoint)
	fmt.Printf("Deploying to function %s...\n", fnID)

	// Create tar.gz archive.
	archivePath, err := createArchive(srcPath)
	if err != nil {
		exitError("create archive: %v", err)
	}
	defer os.Remove(archivePath)

	// Upload via multipart POST.
	resp, err := uploadDeploy(client, fnID, archivePath)
	if err != nil {
		exitError("upload failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func resolveOrCreateFunction(client *cli.Client, name, runtime, entrypoint string) string {
	// Try to find existing function by name.
	resp, err := client.Get("/api/v1/functions")
	if err == nil && resp.StatusCode == http.StatusOK {
		var result struct {
			Functions []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"functions"`
		}
		if decodeJSON(resp, &result) == nil {
			for _, fn := range result.Functions {
				if fn.Name == name {
					return fn.ID
				}
			}
		}
	}

	// Function doesn't exist, create it.
	fmt.Printf("Function %q not found, creating...\n", name)
	body := map[string]string{
		"name":    name,
		"runtime": runtime,
	}
	if entrypoint != "" {
		body["entrypoint"] = entrypoint
	}
	resp, err = client.Post("/api/v1/functions", body)
	if err != nil {
		exitError("create function: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("create function: %v", err)
	}

	var fn struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(resp, &fn); err != nil {
		exitError("decode create response: %v", err)
	}
	fmt.Printf("Created function %s\n", fn.ID)
	return fn.ID
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
