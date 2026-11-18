//go:build linux

package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/google/uuid"
)

const MaxRollbackRequestBytes = 1024

var ErrRollbackInput = errors.New("managed rollback requires an exact deployment UUID (and function UUID); tools also require confirm=true")

type RollbackInput struct {
	FunctionID   string
	DeploymentID string
}

func validRollbackID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s && u.Variant() == uuid.RFC4122 && u.Version() >= 1 && u.Version() <= 8
}

// DecodeRollbackInput preserves raw field validation across REST, MCP and AI.
// pathOwner is the REST path UUID; an empty value selects tool input.
func DecodeRollbackInput(raw []byte, pathOwner string, tool bool) (RollbackInput, error) {
	in := RollbackInput{FunctionID: pathOwner}
	if len(raw) > MaxRollbackRequestBytes || !utf8.Valid(raw) || !publicationEscapes(raw) {
		return in, ErrRollbackInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return in, ErrRollbackInput
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return in, ErrRollbackInput
		}
		seen[key] = true
		value, err := d.Token()
		if err != nil {
			return in, ErrRollbackInput
		}
		switch key {
		case "deployment_id":
			in.DeploymentID, ok = value.(string)
		case "function_id":
			in.FunctionID, ok = value.(string)
			ok = ok && tool
		case "confirm":
			var yes bool
			yes, ok = value.(bool)
			ok = ok && yes && tool
		default:
			ok = false
		}
		if !ok {
			return in, ErrRollbackInput
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return in, ErrRollbackInput
	}
	if _, err := d.Token(); err != io.EOF || !validRollbackID(in.FunctionID) ||
		!validRollbackID(in.DeploymentID) || (tool && (!seen["confirm"] || !seen["function_id"])) {
		return in, ErrRollbackInput
	}
	return in, nil
}
