package database

import "time"

type Route struct {
	Path       string    `json:"path"`
	FunctionID string    `json:"function_id"`
	Methods    string    `json:"methods"` // "*" or comma-separated "GET,POST"
	CreatedAt  time.Time `json:"created_at"`
}

func (db *Database) UpsertRoute(path, functionID, methods string) error {
	if methods == "" {
		methods = "*"
	}
	_, err := db.write.Exec(`
		INSERT INTO routes (path, function_id, methods)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			function_id = excluded.function_id,
			methods = excluded.methods`,
		path, functionID, methods,
	)
	return err
}

func (db *Database) DeleteRoute(path string) error {
	_, err := db.write.Exec(`DELETE FROM routes WHERE path = ?`, path)
	return err
}

func (db *Database) GetRoute(path string) (*Route, error) {
	var r Route
	err := db.read.QueryRow(
		`SELECT path, function_id, methods, created_at FROM routes WHERE path = ?`,
		path,
	).Scan(&r.Path, &r.FunctionID, &r.Methods, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// MatchRoute returns the route for a path, trying exact match first, then
// the longest registered prefix route (a route whose `path` ends in `/*`).
// Returns (nil, nil) when nothing matches, to differentiate from DB errors.
func (db *Database) MatchRoute(reqPath string) (*Route, string, error) {
	// Exact match.
	if r, err := db.GetRoute(reqPath); err == nil {
		return r, r.Path, nil
	}
	// Longest prefix match (path LIKE "%/*").
	rows, err := db.read.Query(
		`SELECT path, function_id, methods, created_at
		 FROM routes
		 WHERE path LIKE '%/*'
		 ORDER BY length(path) DESC`,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var r Route
		if err := rows.Scan(&r.Path, &r.FunctionID, &r.Methods, &r.CreatedAt); err != nil {
			return nil, "", err
		}
		// "/shortener/*" → prefix "/shortener/"
		prefix := r.Path[:len(r.Path)-1] // drop the trailing *
		if len(reqPath) >= len(prefix) && reqPath[:len(prefix)] == prefix {
			return &r, prefix, nil
		}
	}
	return nil, "", nil
}

func (db *Database) ListRoutes() ([]*Route, error) {
	rows, err := db.read.Query(
		`SELECT path, function_id, methods, created_at FROM routes ORDER BY path`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Route
	for rows.Next() {
		var r Route
		if err := rows.Scan(&r.Path, &r.FunctionID, &r.Methods, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}
