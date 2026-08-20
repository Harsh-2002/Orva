package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const traversalFnID = "019df200-7b00-7e00-9c00-aab1cd2e3f41"

// The concrete escalation: <dataDir>/functions/<id>/current is three levels
// below <dataDir>, where server.go writes the plaintext bootstrap admin key.
const adminKeyEscape = "../../../.admin-key"

// TestUpdateRejectsTraversalEntrypoint — the write-side gate. entrypoint was
// applied to the record with no validation whatsoever, so a key holding only
// read+write could point it anywhere on the host.
func TestUpdateRejectsTraversalEntrypoint(t *testing.T) {
	h, _ := newFnDiskHandler(t, traversalFnID)

	for _, bad := range []string{adminKeyEscape, "/etc/passwd", "..", "a/../../b"} {
		t.Run(bad, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"entrypoint": bad})
			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/functions/"+traversalFnID, strings.NewReader(string(body)))
			req.SetPathValue("id", traversalFnID)
			rec := httptest.NewRecorder()
			h.Update(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("entrypoint %q accepted with status %d; want 400", bad, rec.Code)
			}
		})
	}

	// And the stored value must be untouched by the rejected writes.
	fn, err := h.Registry.Get(traversalFnID)
	if err != nil {
		t.Fatal(err)
	}
	if fn.Entrypoint != "handler.js" {
		t.Fatalf("entrypoint changed despite rejection: %q", fn.Entrypoint)
	}
}

// TestGetSourceContainsTraversalEntrypoint — the read-side gate, which has to
// hold independently: rows written before validation existed still carry
// whatever they carry. This seeds the malicious value directly into the
// database, exactly as a pre-existing row would.
func TestGetSourceContainsTraversalEntrypoint(t *testing.T) {
	h, dataDir := newFnDiskHandler(t, traversalFnID)

	// Plant the file the attacker is after.
	const secret = "orva_key_SUPERSECRETADMINKEY\n"
	if err := os.WriteFile(filepath.Join(dataDir, ".admin-key"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	// A real code dir so the read gets as far as resolving the entrypoint.
	writeTree(t, filepath.Join(dataDir, "functions", traversalFnID, "current"))

	if _, err := h.DB.WriteDB().Exec(
		`UPDATE functions SET entrypoint = ? WHERE id = ?`, adminKeyEscape, traversalFnID,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.Registry.LoadAll(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/functions/"+traversalFnID+"/source", nil)
	req.SetPathValue("id", traversalFnID)
	rec := httptest.NewRecorder()
	h.GetSource(rec, req)

	if strings.Contains(rec.Body.String(), "SUPERSECRETADMINKEY") {
		t.Fatalf("GET /source returned the bootstrap admin key: %s", rec.Body.String())
	}
	if rec.Code == http.StatusOK {
		t.Errorf("traversal entrypoint returned 200; want a client error, got body %s",
			rec.Body.String())
	}
}

// TestGetSourceDistinguishesUndeployedFromUnreadable — an unreadable file
// used to be reported identically to "never deployed": 200 with an empty
// body. The editor rendered that as a blank buffer, and saving it overwrote
// the real handler.
func TestGetSourceDistinguishesUndeployedFromUnreadable(t *testing.T) {
	h, dataDir := newFnDiskHandler(t, traversalFnID)
	current := filepath.Join(dataDir, "functions", traversalFnID, "current")
	writeTree(t, current)

	handlerPath := filepath.Join(current, "handler.js")
	if err := os.WriteFile(handlerPath, []byte("module.exports=()=>{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make it unreadable. Skip when running as root, where mode is advisory.
	if os.Geteuid() == 0 {
		t.Skip("running as root: file mode does not deny reads")
	}
	if err := os.Chmod(handlerPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(handlerPath, 0o644) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/functions/"+traversalFnID+"/source", nil)
	req.SetPathValue("id", traversalFnID)
	rec := httptest.NewRecorder()
	h.GetSource(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("unreadable source reported as success (%d) with body %s — "+
			"the editor renders this as an empty buffer and saving destroys the handler",
			rec.Code, rec.Body.String())
	}
}
