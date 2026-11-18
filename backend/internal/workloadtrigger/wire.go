package workloadtrigger

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const MaxRequestBytes = 32 << 10

func object(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > MaxRequestBytes || !utf8.Valid(raw) || !validEscapes(raw) {
		return nil, database.ErrExposureInvalid
	}
	m, err := workloadpolicy.StrictObject(raw)
	if err != nil {
		return nil, database.ErrExposureInvalid
	}
	return m, nil
}
func fields(m map[string]json.RawMessage, required, optional string) bool {
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required) {
		if m[k] == nil {
			return false
		}
		allowed[k] = true
	}
	for _, k := range strings.Fields(optional) {
		allowed[k] = true
	}
	for k, v := range m {
		if !allowed[k] || string(v) == "null" {
			return false
		}
	}
	return true
}
func decimal(raw []byte, zero bool) (uint64, error) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, database.ErrExposureInvalid
	}
	n, err := strconv.ParseUint(s, 10, 63)
	if err != nil || strconv.FormatUint(n, 10) != s || (!zero && n == 0) || n >= math.MaxInt64 {
		return 0, database.ErrExposureInvalid
	}
	return n, nil
}
func decodeInspect(raw []byte) (kind, key, owner string, err error) {
	m, e := object(raw)
	if e != nil || !fields(m, "kind", "key function_id") {
		err = database.ErrExposureInvalid
		return
	}
	for k, p := range map[string]*string{"kind": &kind, "key": &key, "function_id": &owner} {
		if m[k] != nil && json.Unmarshal(m[k], p) != nil {
			err = database.ErrExposureInvalid
			return
		}
	}
	if (kind != "route" && kind != "inbound") || (owner != "" && !ids.IsUUID(owner)) || (kind == "inbound" && owner == "") {
		err = database.ErrExposureInvalid
	}
	return
}
func Decode(op string, raw []byte) (database.ExposureMutation, error) {
	c := database.ExposureMutation{Operation: op}
	m, err := object(raw)
	if err != nil {
		return c, err
	}
	required, optional := "version expected", ""
	switch op {
	case "route_set":
		required += " path function_id"
		optional = "methods"
	case "route_delete":
		required += " path confirm"
	case "inbound_create":
		required += " function_id name"
		optional = "signature_format signature_header active"
	case "inbound_update":
		required += " function_id id"
		optional = "name signature_format signature_header active"
	case "inbound_delete":
		required += " function_id id confirm"
	default:
		return c, database.ErrExposureInvalid
	}
	if !fields(m, required, optional) {
		return c, database.ErrExposureInvalid
	}
	if m["confirm"] != nil && string(m["confirm"]) != "true" {
		return c, database.ErrExposureInvalid
	}
	c.Version, err = decimal(m["version"], true)
	if err != nil {
		return c, err
	}
	owners, err := object(m["expected"])
	if err != nil || len(owners) < 1 || len(owners) > 2 {
		return c, database.ErrExposureInvalid
	}
	c.Expected = map[string]database.ExposureOwnerExpected{}
	for id, raw := range owners {
		f, e := object(raw)
		if e != nil || !ids.IsUUID(id) || !fields(f, "policy_revision authority_epoch", "") {
			return c, database.ErrExposureInvalid
		}
		r, e := decimal(f["policy_revision"], false)
		if e != nil {
			return c, e
		}
		a, e := decimal(f["authority_epoch"], false)
		if e != nil {
			return c, e
		}
		c.Expected[id] = database.ExposureOwnerExpected{PolicyRevision: r, AuthorityEpoch: a}
	}
	for k, p := range map[string]*string{"function_id": &c.FunctionID, "path": &c.Key, "id": &c.Key, "methods": &c.Methods} {
		if m[k] != nil && json.Unmarshal(m[k], p) != nil {
			return c, database.ErrExposureInvalid
		}
	}
	for k, p := range map[string]**string{"name": &c.Name, "signature_format": &c.SignatureFormat, "signature_header": &c.SignatureHeader} {
		if m[k] != nil && json.Unmarshal(m[k], p) != nil {
			return c, database.ErrExposureInvalid
		}
	}
	if m["active"] != nil && json.Unmarshal(m["active"], &c.Active) != nil {
		return c, database.ErrExposureInvalid
	}
	if op == "inbound_create" {
		c.Key = database.NewInboundWebhookID()
	}
	return c, nil
}

// Reject UTF-16 replacement rather than rewriting an authored selector or label.
func validEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		unit, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if unit >= 0xDC00 && unit <= 0xDFFF {
			return false
		}
		if unit < 0xD800 || unit > 0xDBFF {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xDC00 || low > 0xDFFF {
			return false
		}
		i += 6
	}
	return true
}
