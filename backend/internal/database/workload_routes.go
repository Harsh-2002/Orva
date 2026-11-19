package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func managedRoutePath(p string) bool {
	if !boundedExposureText(p, 4096) || p[0] != '/' || strings.ContainsAny(p, "\\%?#") {
		return false
	}
	for _, c := range p {
		if c < ' ' || c == 0x7f {
			return false
		}
	}
	for segment := range strings.SplitSeq(strings.Trim(p, "/"), "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return !strings.Contains(p, "//")
}

// ManagedCustomRoutePath keeps every platform namespace in the platform mux,
// including paths for which the current mux has no matching method.
func ManagedCustomRoutePath(p string) bool {
	if p == "/" || !managedRoutePath(p) {
		return false
	}
	root, _, _ := strings.Cut(p[1:], "/")
	switch root {
	case "api", "auth", "web", "_orva", "fn", "mcp", "webhook", "metrics", "register", "oauth", ".well-known":
		return false
	}
	return true
}

func validManagedRoutePattern(p string) bool {
	return ManagedCustomRoutePath(p) && !strings.Contains(strings.TrimSuffix(p, "/*"), "*")
}

// MatchManagedRoute resolves one snapshot of published mappings. Method
// filtering follows selection: it must never fall back to a broader binding.
func (db *Database) MatchManagedRoute(ctx context.Context, path string) (*Route, string, error) {
	if !ManagedCustomRoutePath(path) {
		return nil, "", nil
	}
	// Generate prefix lengths, not copies of each prefix. SQLite substr counts
	// Unicode code points; indexed probes run longest first after the exact key.
	offsets := []int{-1}
	n := 0
	for _, c := range path {
		n++
		if c == '/' {
			offsets = append(offsets, n)
		}
	}
	for i, j := 1, len(offsets)-1; i < j; i, j = i+1, j-1 {
		offsets[i], offsets[j] = offsets[j], offsets[i]
	}
	keys, _ := json.Marshal(offsets)
	var route Route
	err := db.read.QueryRowContext(ctx, `SELECT r.path,r.function_id,r.methods,r.created_at
 FROM json_each(?) AS k
 CROSS JOIN routes AS r ON r.path=CASE WHEN k.value=-1 THEN ? ELSE substr(?,1,k.value)||'*' END
 JOIN workload_exposure_versions AS v ON v.kind='route' AND v.key=r.path
 JOIN workload_route_publications AS p ON p.path=r.path AND p.version=v.version
     AND p.function_id=r.function_id AND p.methods=r.methods
 ORDER BY k.key LIMIT 1`, string(keys), path, path).
		Scan(&route.Path, &route.FunctionID, &route.Methods, &route.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", ErrExposureUnavailable
	}
	if route.Path == path {
		return &route, path, nil
	}
	return &route, strings.TrimSuffix(route.Path, "*"), nil
}

func quarantineManagedRoutesTx(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema
	 WHERE type='table' AND name='workload_route_publications')`).Scan(&exists)
	if err != nil || !exists {
		return err
	}
	// Preserve mapping history and the version high-water marks, not the public grant.
	_, err = tx.ExecContext(ctx, `DELETE FROM workload_route_publications`)
	return err
}
