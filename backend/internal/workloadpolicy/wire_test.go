package workloadpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func wireExpected(revision, current, approved string) string {
	return fmt.Sprintf(`{"revision":%s,"current_artifact":%s,"approved_artifact":%s}`, revision, current, approved)
}

func TestPolicyWireOwnedExactExpectations(t *testing.T) {
	hash := strings.Repeat("a", 64)
	expected := wireExpected(`"9007199254740993"`, strconvJSON(hash), "null")
	body := []byte(`{"expected":` + expected + `,"policy":` + emptyPolicy + `}`)
	decoded, err := DecodeReplacement(body)
	if err != nil || decoded.Expected.Revision != 9007199254740993 ||
		decoded.Expected.CurrentArtifact != hash || decoded.Expected.ApprovedArtifact != database.IAMUnboundArtifact ||
		string(decoded.PolicyJSON) != emptyPolicy {
		t.Fatalf("exact expectations were lost: %v", err)
	}
	clear(body)
	if string(decoded.PolicyJSON) != emptyPolicy {
		t.Fatal("decoded policy aliases the input body")
	}
	review, err := DecodeReview([]byte(`{"expected":` + wireExpected(`"9223372036854775807"`, "null", strconvJSON(hash)) + `}`))
	if err != nil || review.Revision != 9223372036854775807 || review.CurrentArtifact != "" || review.ApprovedArtifact != hash {
		t.Fatalf("review expectations were coerced: %v", err)
	}
}

func strconvJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestPolicyWireRejectsForgedMetadataAndAmbiguity(t *testing.T) {
	expected := wireExpected(`"7"`, "null", "null")
	valid := `{"expected":` + expected + `,"policy":` + emptyPolicy + `}`
	cases := []string{
		"", "null", "[]", valid + "{}",
		strings.Replace(valid, `"expected":`, `"actor":{"kind":"api_key"},"expected":`, 1),
		strings.Replace(valid, `"expected":`, `"capabilities":{"canManage":true},"expected":`, 1),
		strings.Replace(valid, `"expected":`, `"approved_artifact":"forged","expected":`, 1),
		strings.Replace(valid, `"expected":`, `"quarantined":false,"expected":`, 1),
		strings.Replace(valid, `"policy":`, `"policy":null,"policy":`, 1),
		strings.Replace(valid, `"revision":"7"`, `"revision":"6","revision":"7"`, 1),
		strings.Replace(valid, `"revision":"7"`, `"revision":"7","\u0072evision":"7"`, 1),
		strings.Replace(valid, `"revision":"7"`, `"revision":"7","actor":"forged"`, 1),
		strings.Replace(valid, `"current_artifact":null,`, "", 1),
		strings.Replace(valid, `"approved_artifact":null`, `"approved_artifact":"iam:unbound"`, 1),
		strings.Replace(valid, `"current_artifact":null`, `"current_artifact":""`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"actor":"forged"`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"unknown":[[[[[[[[[[[[[[[[[[[]]]]]]]]]]]]]]]]]]]`, 1),
		string(append([]byte(valid), 0xff)),
	}
	for _, body := range cases {
		if value, err := DecodeReplacement([]byte(body)); value != nil || err != database.ErrIAMInvalidPolicy {
			t.Fatal("ambiguous/host-authored replacement fields were accepted")
		}
	}
	for _, revision := range []string{"7", "null", `"0"`, `"07"`, `"+7"`, `" 7"`, `"7.0"`, `"9223372036854775808"`, `"-1"`} {
		body := strings.Replace(valid, `"revision":"7"`, `"revision":`+revision, 1)
		if value, err := DecodeReplacement([]byte(body)); value != nil || err != database.ErrIAMInvalidPolicy {
			t.Fatalf("invalid revision %s accepted", revision)
		}
	}
	if _, err := DecodeReview([]byte(valid)); err != database.ErrIAMInvalidPolicy {
		t.Fatal("review accepted a replacement policy")
	}
}

func TestPolicyWireBoundsBeforeParsing(t *testing.T) {
	valid := []byte(`{"expected":` + wireExpected(`"1"`, "null", "null") + `,"policy":` + emptyPolicy + `}`)
	exact := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), MaxManagementBytes-len(valid))...)
	if _, err := DecodeReplacement(exact); err != nil {
		t.Fatal("valid exact envelope bound rejected")
	}
	if _, err := DecodeReplacement(append(exact, ' ')); err != database.ErrIAMInvalidPolicy {
		t.Fatal("oversized envelope accepted")
	}
	largePolicy := "{" + strings.Repeat(" ", 32769-len(emptyPolicy)) + emptyPolicy[1:]
	oversizedPolicy := []byte(`{"expected":` + wireExpected(`"1"`, "null", "null") + `,"policy":` + largePolicy + `}`)
	if _, err := DecodeReplacement(oversizedPolicy); err != database.ErrIAMInvalidPolicy {
		t.Fatal("oversized policy accepted inside a bounded envelope")
	}
}
