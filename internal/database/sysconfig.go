package database

import (
	"database/sql"
	"strconv"
)

// GetSystemConfigInt reads an integer-valued system_config row. Returns
// fallback when the row is missing or the value isn't a valid int. Used
// by background loops that need to read tuning knobs once per tick.
func (db *Database) GetSystemConfigInt(key string, fallback int) int {
	var v string
	err := db.read.QueryRow(`SELECT value FROM system_config WHERE key = ?`, key).Scan(&v)
	if err != nil {
		if err != sql.ErrNoRows {
			// Quiet failure — caller fallbacks are fine for tuning knobs.
			_ = err
		}
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
