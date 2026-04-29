package database

import (
	"database/sql"
	"fmt"
	"time"
)

// BlocklistRule is a single row in egress_blocklist. Default + suggested
// rules are seeded by Migrate(); custom rules come from the operator.
type BlocklistRule struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`       // 'default' | 'suggested' | 'custom'
	RuleType  string    `json:"rule_type"`  // 'cidr' | 'hostname' | 'wildcard'
	Value     string    `json:"value"`
	Label     string    `json:"label,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Allowed values used by validation in handlers + manager.
const (
	BlocklistKindDefault   = "default"
	BlocklistKindSuggested = "suggested"
	BlocklistKindCustom    = "custom"

	BlocklistTypeCIDR     = "cidr"
	BlocklistTypeHostname = "hostname"
	BlocklistTypeWildcard = "wildcard"
)

func ValidBlocklistRuleType(s string) bool {
	switch s {
	case BlocklistTypeCIDR, BlocklistTypeHostname, BlocklistTypeWildcard:
		return true
	}
	return false
}

// ListBlocklistRules returns every row, in (kind, id) order so the UI
// can render Default → Suggested → Custom without further sorting.
func (db *Database) ListBlocklistRules() ([]*BlocklistRule, error) {
	rows, err := db.read.Query(`
		SELECT id, kind, rule_type, value, COALESCE(label, ''), enabled,
		       created_at, updated_at
		FROM egress_blocklist
		ORDER BY
		    CASE kind WHEN 'default' THEN 0 WHEN 'suggested' THEN 1 ELSE 2 END,
		    id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*BlocklistRule
	for rows.Next() {
		r := &BlocklistRule{}
		var enabled int
		if err := rows.Scan(&r.ID, &r.Kind, &r.RuleType, &r.Value, &r.Label,
			&enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListEnabledBlocklistRules is the firewall manager's hot read.
func (db *Database) ListEnabledBlocklistRules() ([]*BlocklistRule, error) {
	rows, err := db.read.Query(`
		SELECT id, kind, rule_type, value, COALESCE(label, ''), enabled,
		       created_at, updated_at
		FROM egress_blocklist
		WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*BlocklistRule
	for rows.Next() {
		r := &BlocklistRule{}
		var enabled int
		if err := rows.Scan(&r.ID, &r.Kind, &r.RuleType, &r.Value, &r.Label,
			&enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertCustomBlocklistRule adds an operator-entered rule. The kind is
// always 'custom'; default/suggested rows are seeded by Migrate().
func (db *Database) InsertCustomBlocklistRule(ruleType, value, label string, enabled bool) (*BlocklistRule, error) {
	if !ValidBlocklistRuleType(ruleType) {
		return nil, fmt.Errorf("invalid rule_type: %s", ruleType)
	}
	res, err := db.write.Exec(`
		INSERT INTO egress_blocklist (kind, rule_type, value, label, enabled)
		VALUES ('custom', ?, ?, NULLIF(?, ''), ?)`,
		ruleType, value, label, boolToInt(enabled))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.GetBlocklistRule(id)
}

func (db *Database) GetBlocklistRule(id int64) (*BlocklistRule, error) {
	r := &BlocklistRule{}
	var enabled int
	err := db.read.QueryRow(`
		SELECT id, kind, rule_type, value, COALESCE(label, ''), enabled,
		       created_at, updated_at
		FROM egress_blocklist WHERE id = ?`, id).
		Scan(&r.ID, &r.Kind, &r.RuleType, &r.Value, &r.Label,
			&enabled, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return r, nil
}

// SetBlocklistRuleEnabled toggles the enabled flag. Allowed for any kind.
func (db *Database) SetBlocklistRuleEnabled(id int64, enabled bool) error {
	res, err := db.write.Exec(`
		UPDATE egress_blocklist
		SET enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateBlocklistRuleValue lets the operator edit the value of a custom
// rule. Refusing to edit default/suggested rules keeps the seed canonical.
func (db *Database) UpdateBlocklistRuleValue(id int64, ruleType, value, label string) error {
	if !ValidBlocklistRuleType(ruleType) {
		return fmt.Errorf("invalid rule_type: %s", ruleType)
	}
	res, err := db.write.Exec(`
		UPDATE egress_blocklist
		SET rule_type = ?, value = ?, label = NULLIF(?, ''),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND kind = 'custom'`,
		ruleType, value, label, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("rule not found or not editable")
	}
	return nil
}

// DeleteCustomBlocklistRule removes a custom rule. Default/suggested
// rules can't be deleted — only toggled off.
func (db *Database) DeleteCustomBlocklistRule(id int64) error {
	res, err := db.write.Exec(
		`DELETE FROM egress_blocklist WHERE id = ? AND kind = 'custom'`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("rule not found or not deletable (only custom rules can be deleted)")
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
