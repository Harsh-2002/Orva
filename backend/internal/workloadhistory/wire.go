package workloadhistory

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const MaxRequestBytes = (1 << 20) + 8192

func ValidRequestJSON(raw []byte) bool {
	if len(raw) > MaxRequestBytes {
		return false
	}
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && len(raw) <= MaxRequestBytes && raw[0] == '{' && utf8.Valid(raw) && strictJSON(raw)
}

// Execute is the original-raw boundary shared by HTTP, external MCP and AI.
// Identity is only the host-authenticated reference in ctx, never a DTO field.
func (s *Service) Execute(ctx context.Context, op string, raw []byte) (any, error) {
	if !ValidRequestJSON(raw) || !requestShape(op, raw) {
		return nil, database.ErrHistoryInput
	}
	decode := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	switch op {
	case "list":
		var in struct {
			TargetID string                   `json:"target_id"`
			After    database.HistorySelector `json:"after"`
			Limit    int                      `json:"limit"`
		}
		if decode(&in) != nil {
			return nil, database.ErrHistoryInput
		}
		return s.List(ctx, in.TargetID, in.After, in.Limit)
	case "get":
		var in database.HistorySelector
		if decode(&in) != nil {
			return nil, database.ErrHistoryInput
		}
		return s.Get(ctx, in)
	case "capacity":
		var in struct{}
		if decode(&in) != nil {
			return nil, database.ErrHistoryInput
		}
		ref, ok := auth.OperatorFromContext(ctx)
		if !ok || s == nil || s.DB == nil {
			return nil, database.ErrOperatorDenied
		}
		return s.DB.WorkHistoryCapacity(ctx, ref)
	case "purge":
		var in struct {
			Selected []database.HistorySelector `json:"selected"`
			Confirm  bool                       `json:"confirm"`
		}
		if decode(&in) != nil {
			return nil, database.ErrHistoryInput
		}
		err := s.Purge(ctx, in.Selected, in.Confirm)
		return map[string]bool{"purged": err == nil}, err
	case "reissue":
		var in database.HistoryReissue
		if decode(&in) != nil {
			return nil, database.ErrHistoryInput
		}
		id, err := s.Reissue(ctx, in)
		return map[string]string{"new_id": id, "origin": "operator"}, err
	default:
		return nil, database.ErrHistoryInput
	}
}

func requestShape(op string, raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	allowed, required := "", ""
	switch op {
	case "capacity":
	case "list":
		allowed, required = "target_id after limit", "limit"
	case "get":
		allowed, required = "kind id", "kind id"
	case "purge":
		allowed, required = "selected confirm", "selected confirm"
	case "reissue":
		allowed = "source target_id payload scheduled_at max_attempts name expression timezone enabled reviewed confirm"
		required = "source target_id payload reviewed confirm"
	default:
		return false
	}
	for key := range fields {
		if !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return false
		}
	}
	for _, key := range strings.Fields(required) {
		if len(fields[key]) == 0 || (key != "payload" && bytes.Equal(fields[key], []byte("null"))) {
			return false
		}
	}
	selector := func(raw json.RawMessage) bool { return requestShape("get", raw) }
	if op == "list" && fields["after"] != nil && !selector(fields["after"]) {
		return false
	}
	if op == "reissue" {
		if !selector(fields["source"]) {
			return false
		}
		var source database.HistorySelector
		if json.Unmarshal(fields["source"], &source) != nil {
			return false
		}
		config := "scheduled_at max_attempts"
		if source.Kind == "cron" {
			config = "name expression timezone enabled"
		}
		for _, key := range strings.Fields(config) {
			if len(fields[key]) == 0 || bytes.Equal(fields[key], []byte("null")) {
				return false
			}
		}
	}
	if op == "purge" {
		var selected []json.RawMessage
		if json.Unmarshal(fields["selected"], &selected) != nil {
			return false
		}
		for _, raw := range selected {
			if !selector(raw) {
				return false
			}
		}
	}
	return true
}

func RequestSchema(op string) map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	uuid := func() map[string]any { return map[string]any{"type": "string", "format": "uuid"} }
	obj := func(props map[string]any, required ...string) map[string]any {
		out := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	selector := func(kinds ...string) map[string]any {
		return obj(map[string]any{"kind": map[string]any{"type": "string", "enum": kinds}, "id": uuid()}, "kind", "id")
	}
	all := []string{"job", "cron", "delete", "reissue", "purge"}
	truth := map[string]any{"type": "boolean", "const": true}
	switch op {
	case "capacity":
		return obj(map[string]any{})
	case "get":
		return selector(all...)
	case "list":
		return obj(map[string]any{"target_id": uuid(), "after": selector(all...), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, "limit")
	case "purge":
		return obj(map[string]any{"selected": map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": selector(all...)}, "confirm": truth}, "selected", "confirm")
	case "reissue":
		schema := obj(map[string]any{"source": selector("job", "cron"), "target_id": uuid(), "payload": map[string]any{},
			"scheduled_at": map[string]any{"type": "string", "format": "date-time"}, "max_attempts": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
			"name": str(), "expression": str(), "timezone": str(), "enabled": map[string]any{"type": "boolean"}, "reviewed": truth, "confirm": truth},
			"source", "target_id", "payload", "reviewed", "confirm")
		schema["oneOf"] = []any{
			map[string]any{"properties": map[string]any{"source": selector("job"), "max_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, "required": []string{"scheduled_at", "max_attempts"}},
			map[string]any{"properties": map[string]any{"source": selector("cron")}, "required": []string{"name", "expression", "timezone", "enabled"}},
		}
		return schema
	default:
		return obj(map[string]any{})
	}
}
