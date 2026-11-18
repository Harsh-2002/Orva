//go:build linux

package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/safepath"
)

const MaxPublicationRequestBytes = 256 << 10

var ErrPublicationInput = errors.New("invalid managed deployment input")

type PublicationInline struct {
	FunctionID string
	Code       string
	Filename   string
	Wait       bool
}

// DecodePublicationInline is shared by the optional REST and raw MCP/AI adapters.
func DecodePublicationInline(raw []byte, tool bool) (PublicationInline, error) {
	var in PublicationInline
	if len(raw) > MaxPublicationRequestBytes || !utf8.Valid(raw) || !publicationEscapes(raw) {
		return in, ErrPublicationInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return in, ErrPublicationInput
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return in, ErrPublicationInput
		}
		seen[key] = true
		value, err := d.Token()
		if err != nil {
			return in, ErrPublicationInput
		}
		switch key {
		case "code", "filename", "function_id", "dependencies":
			text, ok := value.(string)
			if !ok {
				return in, ErrPublicationInput
			}
			switch key {
			case "code":
				in.Code = text
			case "filename":
				in.Filename = text
			case "function_id":
				if !tool {
					return in, ErrPublicationInput
				}
				in.FunctionID = text
			case "dependencies":
				if text != "" {
					return in, ErrPublicationInput
				}
			}
		case "wait":
			b, ok := value.(bool)
			if !tool || !ok {
				return in, ErrPublicationInput
			}
			in.Wait = b
		default:
			return in, ErrPublicationInput
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return in, ErrPublicationInput
	}
	if _, err := d.Token(); err != io.EOF || !seen["code"] || in.Code == "" ||
		(tool && (!seen["function_id"] || in.FunctionID == "" || !seen["wait"])) {
		return in, ErrPublicationInput
	}
	return in, nil
}

// encoding/json silently repairs unpaired surrogate escapes.
func publicationEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			second, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || second < 0xdc00 || second > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func (s *PublicationQueue) Inline(ctx context.Context, owner string, in PublicationInline) (string, error) {
	if s == nil {
		return "", ErrPublicationUnavailable
	}
	fn, err := s.queue.reg.Get(owner)
	if err != nil {
		return "", err
	}
	filename := in.Filename
	if filename == "" {
		filename = fn.Entrypoint
	}
	if safepath.Validate(filename) != nil || len(in.Code) == 0 || len(in.Code) > MaxPublicationRequestBytes {
		return "", ErrPublicationInput
	}
	file, err := os.CreateTemp("", "orva-managed-deploy-*.tar.gz")
	if err != nil {
		return "", ErrPublicationUnavailable
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	err = tw.WriteHeader(&tar.Header{Name: filename, Mode: 0644, Size: int64(len(in.Code))})
	if err == nil {
		_, err = tw.Write([]byte(in.Code))
	}
	err = errors.Join(err, tw.Close(), gz.Close(), file.Close())
	if err != nil {
		_ = os.Remove(file.Name())
		return "", ErrPublicationUnavailable
	}
	return s.Submit(ctx, owner, file.Name())
}
