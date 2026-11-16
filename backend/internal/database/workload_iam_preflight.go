package database

import (
	"context"
	"fmt"
)

type IAMLegacySDKEntry struct {
	FunctionID     string
	Name           string
	SDK            bool
	ReviewRequired bool
}

// IAMLegacySDKPreflight reports legacy flags, even before IAM schema exists.
// It proposes no grants and neither creates nor updates policy/function rows.
func (db *Database) IAMLegacySDKPreflight(ctx context.Context) ([]IAMLegacySDKEntry, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT id,name,sdk FROM functions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("IAM legacy preflight: %w", err)
	}
	defer rows.Close()
	result := []IAMLegacySDKEntry{}
	for rows.Next() {
		var e IAMLegacySDKEntry
		if err := rows.Scan(&e.FunctionID, &e.Name, &e.SDK); err != nil {
			return nil, fmt.Errorf("IAM legacy preflight: %w", err)
		}
		e.ReviewRequired = e.SDK
		result = append(result, e)
	}
	return result, rows.Err()
}
