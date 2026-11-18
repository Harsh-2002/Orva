package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestRequiredPublicationHasNoLegacyFallback(t *testing.T) {
	h := &FunctionHandler{RequireDeployIAM: true}
	req := httptest.NewRequest("POST", "/functions/id/deploy-inline", strings.NewReader(`{"code":"x"}`))
	req.SetPathValue("fn_id", "id")
	w := httptest.NewRecorder()
	h.DeployInline(w, req)
	if w.Code != 503 {
		t.Fatalf("required nil mode fell back: %d", w.Code)
	}
}

func TestPublicationMultipartRejectsDuplicateUnknownAndOversize(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "multipart.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(db)
	id := ids.New()
	if err := reg.Set(&database.Function{ID: id, Name: "multipart", Runtime: "node", Entrypoint: "handler.js", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	// No malformed request may reach this deliberately uninitialized service.
	h := &FunctionHandler{Registry: reg, DeployIAM: &builder.PublicationQueue{}, RequireDeployIAM: true}
	for _, variant := range []string{"unknown-field", "duplicate-code", "unknown-file", "oversize"} {
		t.Run(variant, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("code", "source.tar.gz")
			if err != nil {
				t.Fatal(err)
			}
			if variant == "oversize" {
				if _, err := part.Write(make([]byte, (50<<20)+1)); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := part.Write([]byte("unused-archive")); err != nil {
					t.Fatal(err)
				}
			}
			switch variant {
			case "unknown-field":
				if err := writer.WriteField("operator", "untrusted-claim"); err != nil {
					t.Fatal(err)
				}
			case "duplicate-code":
				if _, err := writer.CreateFormFile("code", "duplicate.tar.gz"); err != nil {
					t.Fatal(err)
				}
			case "unknown-file":
				if _, err := writer.CreateFormFile("provenance", "claim.json"); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/api/v1/functions/"+id+"/deploy", bytes.NewReader(body.Bytes()))
			req.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			h.Deploy(w, req)
			if w.Code != 400 {
				t.Fatalf("malformed multipart reached submission: %d", w.Code)
			}
		})
	}
}
