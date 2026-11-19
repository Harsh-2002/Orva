package commands

import (
	"bytes"
	"testing"
)

func TestWriteRawJSONPreservesServerNumbers(t *testing.T) {
	var out bytes.Buffer
	input := []byte(`{"version":9007199254740993,"limits":[9223372036854775807,-9223372036854775808,1.20e+3]}` + "\n ")
	if err := writeRawJSON(&out, input); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"version\": 9007199254740993,\n  \"limits\": [\n    9223372036854775807,\n    -9223372036854775808,\n    1.20e+3\n  ]\n}\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestWriteRawJSONPreservesInvalidInput(t *testing.T) {
	var out bytes.Buffer
	input := []byte(`{"version":1} {"version":2}`)
	if err := writeRawJSON(&out, input); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), input) {
		t.Fatalf("invalid input changed: got %q, want %q", out.Bytes(), input)
	}
}
