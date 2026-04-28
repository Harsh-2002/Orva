// Package secrets encrypts per-function secret values at rest using AES-GCM
// with a master key persisted at ${DataDir}/.master.key. The master key is
// generated on first use (32 random bytes, 0600 permissions) and never
// leaves the host. There is no KMS dependency — this is deliberately simple,
// matching Orva's single-box deployment model.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Harsh-2002/Orva/internal/database"
)

// Manager encrypts/decrypts function secrets and persists them in SQLite.
type Manager struct {
	db      *database.Database
	keyPath string

	mu   sync.RWMutex
	aead cipher.AEAD
}

// New constructs a Manager. The master key is lazily loaded on first
// encrypt/decrypt so a cold start where no secrets exist does no disk I/O.
func New(db *database.Database, dataDir string) *Manager {
	return &Manager{
		db:      db,
		keyPath: filepath.Join(dataDir, ".master.key"),
	}
}

func (m *Manager) loadOrCreateKey() ([]byte, error) {
	if b, err := os.ReadFile(m.keyPath); err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("master key at %s has wrong size (%d, want 32)", m.keyPath, len(b))
		}
		return b, nil
	}

	// Generate a fresh 256-bit key.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir for master key: %w", err)
	}
	if err := os.WriteFile(m.keyPath, key, 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

func (m *Manager) cipher() (cipher.AEAD, error) {
	m.mu.RLock()
	if m.aead != nil {
		defer m.mu.RUnlock()
		return m.aead, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aead != nil {
		return m.aead, nil
	}

	key, err := m.loadOrCreateKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	m.aead = aead
	return aead, nil
}

// encrypt returns base64(nonce || ciphertext).
func (m *Manager) encrypt(plaintext string) (string, error) {
	aead, err := m.cipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (m *Manager) decrypt(b64 string) (string, error) {
	aead, err := m.cipher()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("secret base64: %w", err)
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secret blob too short")
	}
	pt, err := aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("secret decrypt: %w", err)
	}
	return string(pt), nil
}

// Upsert stores (or overwrites) a secret for a function.
func (m *Manager) Upsert(functionID, key, value string) error {
	if key == "" {
		return errors.New("secret key required")
	}
	blob, err := m.encrypt(value)
	if err != nil {
		return err
	}
	return m.db.UpsertSecret(functionID, key, blob)
}

// Delete removes a single secret key for a function.
func (m *Manager) Delete(functionID, key string) error {
	return m.db.DeleteSecret(functionID, key)
}

// List returns the secret names for a function (values are NOT returned).
func (m *Manager) List(functionID string) ([]string, error) {
	return m.db.ListSecretKeys(functionID)
}

// GetForFunction returns the decrypted secrets map for invocation-time
// injection. Keys are plaintext, values are decrypted plaintext.
func (m *Manager) GetForFunction(functionID string) (map[string]string, error) {
	rows, err := m.db.ListSecrets(functionID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		pt, err := m.decrypt(r.ValueEncrypted)
		if err != nil {
			// Skip corrupt entries rather than failing the whole invocation.
			continue
		}
		out[r.Key] = pt
	}
	return out, nil
}

// DeleteAllForFunction removes every secret tied to a function. Used when
// a function itself is deleted.
func (m *Manager) DeleteAllForFunction(functionID string) error {
	return m.db.DeleteSecretsForFunction(functionID)
}
