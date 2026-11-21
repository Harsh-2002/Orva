package builder

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path"
	"strings"
)

// sdkImportPatterns are the substrings we treat as "this handler imports
// the orva SDK." Conservative on purpose — we look for the canonical
// shapes the docs and editor templates produce. False positives here
// only mean we attach an unnecessary warning, which is cheap; false
// negatives mean we let an agent ship a broken function silently, which
// is exactly the foot-gun this scan exists to prevent.
var sdkImportPatterns = []string{
	"require('orva')",   // Node CommonJS, single-quoted (default editor template)
	"require(\"orva\")", // Node CommonJS, double-quoted
	"from 'orva'",       // Node ESM
	"from \"orva\"",     // Node ESM, double-quoted
	"from orva ",        // Python `from orva import ...`
	"from orva\timport", // Python with tab between identifier and `import`
	"from orva.",        // Python submodule, e.g. `from orva.kv import client`
	"import orva",       // Python `import orva`
}

// SourceUsesOrvaSDK reports whether the supplied handler source appears
// to import the in-sandbox `orva` module (kv / invoke / jobs). The match
// is a plain substring sweep — we don't try to parse the source. The
// caller pairs this with the function's SDK permission to decide whether
// to surface a deploy warning.
func SourceUsesOrvaSDK(source string) bool {
	if source == "" {
		return false
	}
	for _, pat := range sdkImportPatterns {
		if strings.Contains(source, pat) {
			return true
		}
	}
	return false
}

// ArchiveEntrypointUsesOrvaSDK makes a bounded advisory scan, leaving archive
// validation to the builder. It never extracts files.
func ArchiveEntrypointUsesOrvaSDK(archivePath, entrypoint string) bool {
	const (
		archiveScanLimit = 4 << 20
		sourceScanLimit  = 256 << 10
	)
	if entrypoint == "" {
		return false
	}
	entrypoint = path.Clean(entrypoint)
	f, err := os.Open(archivePath)
	if err != nil {
		return false
	}
	defer f.Close()
	zr, err := gzip.NewReader(io.LimitReader(f, archiveScanLimit))
	if err != nil {
		return false
	}
	defer zr.Close()
	tr := tar.NewReader(io.LimitReader(zr, archiveScanLimit))
	for {
		header, err := tr.Next()
		if err != nil {
			return false
		}
		if header.Typeflag == tar.TypeReg && path.Clean(header.Name) == entrypoint {
			prefix, err := io.ReadAll(io.LimitReader(tr, sourceScanLimit))
			return err == nil && SourceUsesOrvaSDK(string(prefix))
		}
	}
}

// SDKNoneWarning describes a detected SDK import without SDK permission.
const SDKNoneWarning = "Function imports the orva SDK (kv / invoke / jobs) " +
	"but sdk permission is false. Update the function with sdk=true to enable " +
	"platform SDK calls independently of network_mode. External HTTPS still requires egress."
