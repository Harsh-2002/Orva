package commands

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
)

const (
	managedOwner = "0190000a-0000-7000-8000-000000000001"
	managedOther = "0190000b-0000-7000-8000-000000000002"
	managedHook  = "0190000c-0000-7000-8000-0000000000aa"
)

func TestCanonicalManagedUUID(t *testing.T) {
	accepted := []string{managedOwner, managedOther, managedHook}
	for _, value := range accepted {
		if !canonicalManagedUUID(value) {
			t.Errorf("canonicalManagedUUID(%q) = false, want true", value)
		}
	}
	refused := map[string]string{
		"a name":         "payments",
		"uppercase":      "0190000A-0000-7000-8000-000000000001",
		"no hyphens":     "0190000a000070008000000000000001",
		"nil uuid":       "00000000-0000-0000-0000-000000000000",
		"version nibble": "0190000a-0000-0000-8000-000000000001",
		"variant nibble": "0190000a-0000-7000-c000-000000000001",
		"trailing space": managedOwner + " ",
		"":               "",
	}
	for label, value := range refused {
		if canonicalManagedUUID(value) {
			t.Errorf("%s: canonicalManagedUUID(%q) = true, want false", label, value)
		}
	}
}

func TestIsDecimalString(t *testing.T) {
	for _, value := range []string{"0", "1", "9007199254740993", "9223372036854775807"} {
		if !isDecimalString(value) {
			t.Errorf("isDecimalString(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "00", "01", "-1", "1.0", " 1", "1 ", "abc", "99999999999999999999"} {
		if isDecimalString(value) {
			t.Errorf("isDecimalString(%q) = true, want false", value)
		}
	}
}

// The wire hazard this guards: decoding a CAS counter into map[string]any turns
// it into float64, and 9007199254740993 would come back as ...992.
func TestManagedInspectionPreservesDecimalStrings(t *testing.T) {
	raw := []byte(`{"version":"9007199254740993","expected":{"` + managedOwner + `":{"policy_revision":"9007199254740995","authority_epoch":"7"}},"route_published":true}`)
	var inspection managedInspection
	if err := json.Unmarshal(raw, &inspection); err != nil {
		t.Fatalf("decode inspection: %v", err)
	}
	if inspection.Version != "9007199254740993" {
		t.Fatalf("version = %q, want the exact decimal string", inspection.Version)
	}
	body := managedCASBody(&inspection)
	if body["version"] != "9007199254740993" {
		t.Fatalf("body version = %v", body["version"])
	}
	expected, ok := body["expected"].(map[string]map[string]string)
	if !ok {
		t.Fatalf("expected tokens are not string maps: %T", body["expected"])
	}
	if expected[managedOwner]["policy_revision"] != "9007199254740995" {
		t.Fatalf("policy_revision = %q", expected[managedOwner]["policy_revision"])
	}
	if expected[managedOwner]["authority_epoch"] != "7" {
		t.Fatalf("authority_epoch = %q", expected[managedOwner]["authority_epoch"])
	}
}

func TestManagedVersionOverride(t *testing.T) {
	newCmd := func(args ...string) *cobra.Command {
		cmd := &cobra.Command{Use: "delete", RunE: func(*cobra.Command, []string) error { return nil }}
		cmd.Flags().String("expected-version", "", "")
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("parse flags %v: %v", args, err)
		}
		return cmd
	}
	inspection := &managedInspection{Version: "4"}

	version, err := managedVersion(newCmd(), inspection)
	if err != nil || version != "4" {
		t.Fatalf("default = %q, %v; want the inspected 4", version, err)
	}

	version, err = managedVersion(newCmd("--expected-version", "9"), inspection)
	if err != nil || version != "9" {
		t.Fatalf("override = %q, %v; want 9", version, err)
	}

	if _, err := managedVersion(newCmd("--expected-version", "09"), inspection); err == nil {
		t.Fatal("a malformed --expected-version was accepted")
	}
}

func TestManagedOwnerRefusesNames(t *testing.T) {
	if _, err := managedOwnerUUID("payments"); err == nil {
		t.Fatal("a function name was accepted as a managed owner")
	}
	if got, err := managedOwnerUUID(managedOwner); err != nil || got != managedOwner {
		t.Fatalf("managed owner = %q, %v", got, err)
	}
}
