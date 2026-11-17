package workloadpolicy

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// StrictObject checks raw JSON before any transport can discard duplicate keys.
func StrictObject(body []byte) (map[string]json.RawMessage, error) { return managementObject(body) }

func exactFields(m map[string]json.RawMessage, required []string, optional ...string) bool {
	for _, k := range required {
		if m[k] == nil {
			return false
		}
	}
	for k := range m {
		found := false
		for _, s := range append(required, optional...) {
			if k == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func decimal(raw json.RawMessage, zero bool) (uint64, error) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0, database.ErrIAMInvalidPolicy
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil || n > math.MaxInt64 || (!zero && n == 0) || strconv.FormatUint(n, 10) != text {
		return 0, database.ErrIAMInvalidPolicy
	}
	return n, nil
}

func authorOwners(raw json.RawMessage) ([]database.IAMOwnerExpectation, error) {
	var items []json.RawMessage
	if string(raw) == "null" || json.Unmarshal(raw, &items) != nil || items == nil || len(items) > 25 {
		return nil, database.ErrIAMInvalidPolicy
	}
	out := make([]database.IAMOwnerExpectation, 0, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &fields) != nil || !exactFields(fields, []string{"function_id", "expected"}) {
			return nil, database.ErrIAMInvalidPolicy
		}
		var id string
		if json.Unmarshal(fields["function_id"], &id) != nil {
			return nil, database.ErrIAMInvalidPolicy
		}
		e, err := decodeExpected(fields["expected"])
		if err != nil {
			return nil, err
		}
		out = append(out, database.IAMOwnerExpectation{OwnerID: id, Revision: e.Revision, CurrentArtifact: e.CurrentArtifact, ApprovedArtifact: e.ApprovedArtifact})
	}
	return out, nil
}

// DecodeManagedRequest is shared by HTTP and both raw MCP/AI paths. It accepts
// operation-specific authorable fields only; no actor, approval or identity claim.
func DecodeManagedRequest(op, id string, body []byte) (database.IAMManagedMutation, error) {
	r := database.IAMManagedMutation{Operation: op, PolicyID: id}
	m, err := managementObject(body)
	if err != nil {
		return r, err
	}
	fields := map[string][]string{
		"create": {"name", "policy"}, "update": {"definition_revision", "policy"},
		"attach": {"membership_revision", "version", "owners"},
		"detach": {"membership_revision", "owners", "confirm"},
		"revoke": {"definition_revision", "membership_revision", "owners", "confirm"},
		"delete": {"definition_revision", "membership_revision", "confirm"},
		"inline": {"policy", "expected"}, "review": {"expected"},
	}
	required, ok := fields[op]
	if !ok || !exactFields(m, required) {
		return r, database.ErrIAMInvalidPolicy
	}
	if raw := m["confirm"]; raw != nil && string(raw) != "true" {
		return r, database.ErrIAMInvalidPolicy
	}
	if raw := m["name"]; raw != nil {
		if json.Unmarshal(raw, &r.Name) != nil {
			return r, database.ErrIAMInvalidPolicy
		}
	}
	for k, target := range map[string]*uint64{"definition_revision": &r.DefinitionRevision, "membership_revision": &r.MembershipRevision, "version": &r.Version} {
		if raw := m[k]; raw != nil {
			n, e := decimal(raw, false)
			if e != nil {
				return r, e
			}
			*target = n
		}
	}
	if m["policy"] != nil {
		r.PolicyJSON = append([]byte(nil), m["policy"]...)
	}
	if m["owners"] != nil {
		r.Owners, err = authorOwners(m["owners"])
		if err != nil {
			return r, err
		}
	}
	if op == "inline" || op == "review" {
		e, err := decodeExpected(m["expected"])
		if err != nil {
			return r, err
		}
		r.PolicyID = ""
		r.Owners = []database.IAMOwnerExpectation{{OwnerID: id, Revision: e.Revision, CurrentArtifact: e.CurrentArtifact, ApprovedArtifact: e.ApprovedArtifact}}
	}
	if op == "create" && id != "" {
		return r, database.ErrIAMInvalidPolicy
	}
	return r, nil
}
