package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
)

// writeBadArchive emits a minimal gzip tar containing a single file
// with an attacker-controlled path. Only used by RestoreFrom's
// traversal-rejection test.
func writeBadArchive(w *bytes.Buffer, path string, body []byte) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: path,
		Mode: 0o644,
		Size: int64(len(body)),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
