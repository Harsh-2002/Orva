package sdkhost

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const maxIAMCursorBytes = 12 << 10

type iamCursor struct {
	Version int    `json:"v"`
	Owner   string `json:"owner"`
	Prefix  string `json:"prefix"`
	Last    string `json:"last"`
}

func encodeIAMCursor(owner, prefix, last string) string {
	b, _ := json.Marshal(iamCursor{Version: 1, Owner: owner, Prefix: prefix, Last: last})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeIAMCursor(owner, prefix, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	invalid := errors.New("invalid IAM cursor")
	if len(encoded) > maxIAMCursorBytes {
		return "", invalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || !utf8.Valid(b) || base64.RawURLEncoding.EncodeToString(b) != encoded {
		return "", invalid
	}
	var c iamCursor
	if decodeArgs(b, maxIAMCursorBytes, &c) != nil || c.Version != 1 || c.Owner != owner || c.Prefix != prefix ||
		!utf8.ValidString(c.Last) || !utf8.ValidString(c.Prefix) || database.ValidateKVKey(c.Last) != nil || !strings.HasPrefix(c.Last, prefix) {
		return "", invalid
	}
	return c.Last, nil
}
