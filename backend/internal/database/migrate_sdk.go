package database

import "fmt"

func (db *Database) migrateSDKPermission() error {
	tx, err := db.write.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('functions') WHERE name = 'sdk'`).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	// Column creation and legacy grants must commit together; never replay a revoked grant.
	for _, stmt := range []string{
		`ALTER TABLE functions ADD COLUMN sdk INTEGER NOT NULL DEFAULT 0`,
		`UPDATE functions SET sdk = 1 WHERE network_mode = 'egress'`,
		`UPDATE deployments SET snapshot = json_set(snapshot, '$.sdk',
		 json(CASE WHEN json_extract(snapshot, '$.network_mode') = 'egress' THEN 'true' ELSE 'false' END))
		 WHERE snapshot != '' AND json_valid(snapshot) AND json_type(snapshot, '$.sdk') IS NULL`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate sdk permission: %w", err)
		}
	}
	return tx.Commit()
}
