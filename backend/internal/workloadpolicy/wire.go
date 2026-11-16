package workloadpolicy

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

const MaxManagementBytes = workloadiam.MaxPolicyBytes + 4096

type Replacement struct {
	Expected   Expected
	PolicyJSON []byte
}

// DecodeReplacement accepts authorable policy and CAS expectations only.
// It is an internal wire seam; no HTTP route/tool is registered by this package.
func DecodeReplacement(body []byte) (*Replacement, error) {
	root, err := managementObject(body)
	if err != nil || len(root) != 2 || root["policy"] == nil || root["expected"] == nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	expected, err := decodeExpected(root["expected"])
	if err != nil {
		return nil, err
	}
	policy, err := workloadiam.Decode(root["policy"])
	if err != nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	return &Replacement{Expected: expected, PolicyJSON: canonical}, nil
}

func DecodeReview(body []byte) (Expected, error) {
	root, err := managementObject(body)
	if err != nil || len(root) != 1 || root["expected"] == nil {
		return Expected{}, database.ErrIAMInvalidPolicy
	}
	return decodeExpected(root["expected"])
}

func decodeExpected(body []byte) (Expected, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) != 3 ||
		fields["revision"] == nil || fields["current_artifact"] == nil || fields["approved_artifact"] == nil {
		return Expected{}, database.ErrIAMInvalidPolicy
	}
	var revision string
	if json.Unmarshal(fields["revision"], &revision) != nil {
		return Expected{}, database.ErrIAMInvalidPolicy
	}
	value, err := strconv.ParseUint(revision, 10, 64)
	if err != nil || value == 0 || value > math.MaxInt64 || strconv.FormatUint(value, 10) != revision {
		return Expected{}, database.ErrIAMInvalidPolicy
	}
	artifact := func(raw json.RawMessage) (string, error) {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return "", nil
		}
		var text string
		if json.Unmarshal(raw, &text) != nil || !digest(text) {
			return "", database.ErrIAMInvalidPolicy
		}
		return text, nil
	}
	current, err := artifact(fields["current_artifact"])
	if err != nil {
		return Expected{}, err
	}
	approved, err := artifact(fields["approved_artifact"])
	if err != nil {
		return Expected{}, err
	}
	if approved == "" {
		approved = database.IAMUnboundArtifact
	}
	return Expected{Revision: value, CurrentArtifact: current, ApprovedArtifact: approved}, nil
}

func managementObject(body []byte) (map[string]json.RawMessage, error) {
	invalid := database.ErrIAMInvalidPolicy
	if len(body) == 0 || len(body) > MaxManagementBytes || !utf8.Valid(body) {
		return nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	remaining := 8192
	var walk func(int) error
	walk = func(depth int) error {
		remaining--
		if remaining < 0 || depth > 16 {
			return invalid
		}
		t, err := decoder.Token()
		if err != nil {
			return invalid
		}
		delim, container := t.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return invalid
				}
				seen[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return invalid
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return invalid
			}
		default:
			return invalid
		}
		return nil
	}
	if walk(0) != nil {
		return nil, invalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, invalid
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, invalid
	}
	return root, nil
}
