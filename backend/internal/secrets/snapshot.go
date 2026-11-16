package secrets

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/google/uuid"
)

const (
	MaxIAMSecretSnapshotCount = 1024
	MaxIAMSecretSnapshotBytes = 8 << 20
)

// SecretSnapshotLimits bounds the owned map prepared for a future IAM spawn
// snapshot. MaxTotalBytes counts key bytes and decrypted value bytes together.
type SecretSnapshotLimits struct {
	MaxSecrets    int
	MaxTotalBytes int
}

var errInvalidSecretSnapshot = errors.New("invalid secret snapshot")

// SnapshotForIAM strictly decrypts one owner's supplied rows into an owned map.
// Snapshot-only key checks reject empty, invalid UTF-8, and NUL-containing names;
// no additional environment-name restriction is imposed. This is inert
// preparation only: the caller must read rows and generation in one DB snapshot.
func (m *Manager) SnapshotForIAM(ownerID string, rows []database.SecretRow, limits SecretSnapshotLimits) (map[string]string, error) {
	if !canonicalSecretOwner(ownerID) ||
		limits.MaxSecrets < 1 || limits.MaxSecrets > MaxIAMSecretSnapshotCount ||
		limits.MaxTotalBytes < 1 || limits.MaxTotalBytes > MaxIAMSecretSnapshotBytes ||
		len(rows) > limits.MaxSecrets {
		return nil, errInvalidSecretSnapshot
	}

	out := make(map[string]string, len(rows))
	totalBytes := 0
	for _, row := range rows {
		if row.FunctionID != ownerID || row.Key == "" ||
			!utf8.ValidString(row.Key) || strings.IndexByte(row.Key, 0) >= 0 {
			return nil, errInvalidSecretSnapshot
		}
		if _, exists := out[row.Key]; exists {
			return nil, errInvalidSecretSnapshot
		}
		if len(row.Key) > limits.MaxTotalBytes-totalBytes {
			return nil, errInvalidSecretSnapshot
		}
		totalBytes += len(row.Key)

		// AES-GCM adds a 12-byte nonce and 16-byte tag before base64.
		maxEncoded := 4 * ((limits.MaxTotalBytes - totalBytes + 28 + 2) / 3)
		if len(row.ValueEncrypted) > maxEncoded {
			return nil, errInvalidSecretSnapshot
		}
		value, err := m.decrypt(row.ValueEncrypted)
		if err != nil || len(value) > limits.MaxTotalBytes-totalBytes {
			return nil, errInvalidSecretSnapshot
		}
		totalBytes += len(value)
		out[row.Key] = value
	}
	return out, nil
}

func canonicalSecretOwner(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id &&
		parsed.Version() >= 1 && parsed.Version() <= 8 &&
		parsed.Variant() == uuid.RFC4122
}
