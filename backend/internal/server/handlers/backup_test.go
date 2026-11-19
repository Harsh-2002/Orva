package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestManagedBackupAbortsPartialDownload(t *testing.T) {
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, partial := range []bool{false, true} {
		name := "before_stream"
		if partial {
			name = "mid_stream"
		}
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				DownloadManagedBackup(w, r, db, dir, func(_ context.Context, w io.Writer, _ string) error {
					if partial {
						_, _ = w.Write(bytes.Repeat([]byte{'x'}, 8192))
					}
					return errors.New("archive writer failed")
				})
			}))
			defer s.Close()
			response, err := s.Client().Get(s.URL)
			if partial && err != nil {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			_, err = io.ReadAll(response.Body)
			if partial && err == nil {
				t.Fatal("partial download was acknowledged")
			}
			if !partial && (err != nil || response.StatusCode != 500) {
				t.Fatalf("pre-stream failure status=%d, read error=%v", response.StatusCode, err)
			}
		})
	}
}
