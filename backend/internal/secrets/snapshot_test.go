package secrets

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const snapshotOwner = "018bcfe5-6800-7000-8000-000000000001"

func snapshotLimits() SecretSnapshotLimits {
	return SecretSnapshotLimits{MaxSecrets: 8, MaxTotalBytes: 1024}
}

func TestSnapshotForIAMReturnsOwnedSnapshot(t *testing.T) {
	manager := New(nil, t.TempDir())
	first, err := manager.encrypt("one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.encrypt("two")
	if err != nil {
		t.Fatal(err)
	}
	rows := []database.SecretRow{
		{FunctionID: snapshotOwner, Key: "FIRST", ValueEncrypted: first},
		{FunctionID: snapshotOwner, Key: "SECOND", ValueEncrypted: second},
	}
	original := append([]database.SecretRow(nil), rows...)

	got, err := manager.SnapshotForIAM(snapshotOwner, rows, snapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"FIRST": "one", "SECOND": "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}

	got["FIRST"] = "changed"
	rows[0].Key = "MUTATED"
	rows[0].ValueEncrypted = "MUTATED"
	if got["SECOND"] != "two" || !reflect.DeepEqual(rows[1], original[1]) {
		t.Fatal("snapshot or source rows unexpectedly aliased")
	}
	if !reflect.DeepEqual(original, []database.SecretRow{
		{FunctionID: snapshotOwner, Key: "FIRST", ValueEncrypted: first},
		{FunctionID: snapshotOwner, Key: "SECOND", ValueEncrypted: second},
	}) {
		t.Fatal("source rows were mutated")
	}
}

func TestSnapshotForIAMEmptyDoesNotLoadMasterKey(t *testing.T) {
	dir := t.TempDir()
	manager := New(nil, dir)
	got, err := manager.SnapshotForIAM(snapshotOwner, nil, snapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("empty snapshot = %#v, want allocated empty map", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".master.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty snapshot loaded or created master key: %v", err)
	}
}

func TestSnapshotForIAMRejectsOversizedCiphertextBeforeKeyLoading(t *testing.T) {
	dir := t.TempDir()
	manager := New(nil, dir)
	rows := []database.SecretRow{{FunctionID: snapshotOwner, Key: "K", ValueEncrypted: strings.Repeat("A", 4096)}}
	got, err := manager.SnapshotForIAM(snapshotOwner, rows, SecretSnapshotLimits{MaxSecrets: 1, MaxTotalBytes: 1})
	if err == nil || got != nil {
		t.Fatal("oversized ciphertext accepted or exposed partial output")
	}
	if _, err := os.Stat(filepath.Join(dir, ".master.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized ciphertext reached key loading")
	}
}

func TestSnapshotForIAMRejectsInvalidInputAtomically(t *testing.T) {
	writer := New(nil, t.TempDir())
	valid, err := writer.encrypt("secret-value")
	if err != nil {
		t.Fatal(err)
	}
	other, err := writer.encrypt("another-secret")
	if err != nil {
		t.Fatal(err)
	}

	badAEADBytes, err := base64.StdEncoding.DecodeString(valid)
	if err != nil {
		t.Fatal(err)
	}
	badAEADBytes[len(badAEADBytes)-1] ^= 0xff
	badAEAD := base64.StdEncoding.EncodeToString(badAEADBytes)
	shortCiphertext := base64.StdEncoding.EncodeToString([]byte{1})

	wrongKey := New(nil, t.TempDir())
	wrongKeyCiphertext, err := wrongKey.encrypt("other-key")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		owner string
		rows  []database.SecretRow
		limit SecretSnapshotLimits
		mgr   *Manager
	}{
		{"noncanonical owner", strings.ToUpper(snapshotOwner), nil, snapshotLimits(), writer},
		{"invalid owner", "not-a-uuid", nil, snapshotLimits(), writer},
		{"owner mismatch", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}, {FunctionID: "018bcfe5-6800-7000-8000-000000000002", Key: "B", ValueEncrypted: other}}, snapshotLimits(), writer},
		{"empty key", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, ValueEncrypted: valid}}, snapshotLimits(), writer},
		{"invalid utf8 key", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: string([]byte{0xff}), ValueEncrypted: valid}}, snapshotLimits(), writer},
		{"NUL key", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A\x00B", ValueEncrypted: valid}}, snapshotLimits(), writer},
		{"duplicate key", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}, {FunctionID: snapshotOwner, Key: "A", ValueEncrypted: other}}, snapshotLimits(), writer},
		{"bad base64", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: "!"}}, snapshotLimits(), writer},
		{"corrupt after valid row", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}, {FunctionID: snapshotOwner, Key: "B", ValueEncrypted: "!"}}, snapshotLimits(), writer},
		{"short ciphertext", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: shortCiphertext}}, snapshotLimits(), writer},
		{"bad authentication", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: badAEAD}}, snapshotLimits(), writer},
		{"wrong master key", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: wrongKeyCiphertext}}, snapshotLimits(), writer},
		{"count over caller limit", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}}, SecretSnapshotLimits{MaxSecrets: 0, MaxTotalBytes: 1024}, writer},
		{"count over bound", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}}, SecretSnapshotLimits{MaxSecrets: MaxIAMSecretSnapshotCount + 1, MaxTotalBytes: 1024}, writer},
		{"row count over limit", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}, {FunctionID: snapshotOwner, Key: "B", ValueEncrypted: other}}, SecretSnapshotLimits{MaxSecrets: 1, MaxTotalBytes: 1024}, writer},
		{"bytes over caller limit", snapshotOwner, []database.SecretRow{{FunctionID: snapshotOwner, Key: "A", ValueEncrypted: valid}}, SecretSnapshotLimits{MaxSecrets: 8, MaxTotalBytes: 2}, writer},
		{"bytes over bound", snapshotOwner, nil, SecretSnapshotLimits{MaxSecrets: 8, MaxTotalBytes: MaxIAMSecretSnapshotBytes + 1}, writer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rowsBefore := append([]database.SecretRow(nil), tt.rows...)
			got, err := tt.mgr.SnapshotForIAM(tt.owner, tt.rows, tt.limit)
			if err == nil || got != nil {
				t.Fatalf("snapshot = %#v, error = %v; want nil map and error", got, err)
			}
			if err != errInvalidSecretSnapshot {
				t.Fatalf("error = %v; want generic snapshot error", err)
			}
			if strings.Contains(err.Error(), tt.owner) ||
				strings.Contains(err.Error(), valid) || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("error disclosed snapshot data: %q", err)
			}
			for _, row := range tt.rows {
				if row.Key != "" && strings.Contains(err.Error(), row.Key) ||
					row.ValueEncrypted != "" && strings.Contains(err.Error(), row.ValueEncrypted) {
					t.Fatalf("error disclosed row data: %q", err)
				}
			}
			if !reflect.DeepEqual(rowsBefore, tt.rows) {
				t.Fatal("snapshot mutated input rows")
			}
		})
	}
}

func TestSnapshotForIAMAllowsIndependentOwners(t *testing.T) {
	manager := New(nil, t.TempDir())
	first, err := manager.encrypt("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.encrypt("second")
	if err != nil {
		t.Fatal(err)
	}
	otherOwner := "018bcfe5-6800-7000-8000-000000000002"

	got, err := manager.SnapshotForIAM(otherOwner, []database.SecretRow{{
		FunctionID: otherOwner, Key: "TOKEN", ValueEncrypted: second,
	}}, snapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]string{"TOKEN": "second"}) {
		t.Fatalf("snapshot = %#v", got)
	}

	ownerMap, err := manager.SnapshotForIAM(snapshotOwner, []database.SecretRow{{
		FunctionID: snapshotOwner, Key: "TOKEN", ValueEncrypted: first,
	}}, snapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	if ownerMap["TOKEN"] != "first" || got["TOKEN"] != "second" {
		t.Fatalf("owner snapshots crossed: first=%q second=%q", ownerMap["TOKEN"], got["TOKEN"])
	}
}
