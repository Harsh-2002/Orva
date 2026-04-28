package database

type SecretRow struct {
	FunctionID     string
	Key            string
	ValueEncrypted string
}

func (db *Database) UpsertSecret(functionID, key, encrypted string) error {
	_, err := db.write.Exec(`
		INSERT INTO function_secrets (function_id, key, value_encrypted)
		VALUES (?, ?, ?)
		ON CONFLICT(function_id, key) DO UPDATE SET
			value_encrypted = excluded.value_encrypted,
			updated_at = CURRENT_TIMESTAMP`,
		functionID, key, encrypted,
	)
	return err
}

func (db *Database) DeleteSecret(functionID, key string) error {
	_, err := db.write.Exec(
		`DELETE FROM function_secrets WHERE function_id = ? AND key = ?`,
		functionID, key,
	)
	return err
}

func (db *Database) DeleteSecretsForFunction(functionID string) error {
	_, err := db.write.Exec(
		`DELETE FROM function_secrets WHERE function_id = ?`, functionID,
	)
	return err
}

func (db *Database) ListSecretKeys(functionID string) ([]string, error) {
	rows, err := db.read.Query(
		`SELECT key FROM function_secrets WHERE function_id = ? ORDER BY key`,
		functionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (db *Database) ListSecrets(functionID string) ([]SecretRow, error) {
	rows, err := db.read.Query(
		`SELECT function_id, key, value_encrypted FROM function_secrets WHERE function_id = ?`,
		functionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretRow
	for rows.Next() {
		var r SecretRow
		if err := rows.Scan(&r.FunctionID, &r.Key, &r.ValueEncrypted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
