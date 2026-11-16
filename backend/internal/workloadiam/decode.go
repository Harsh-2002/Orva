package workloadiam

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Decode rejects ambiguous JSON before decoding; use Compile to bind an owner.
func Decode(data []byte) (Policy, error) {
	var p Policy
	if len(data) > MaxPolicyBytes || !utf8.Valid(data) {
		return p, errors.New("policy exceeds byte limit or contains invalid UTF-8")
	}
	if !validEscapes(data) {
		return p, errors.New("invalid Unicode escape")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := checkShape(d, reflect.TypeFor[Policy]()); err != nil {
		return p, fmt.Errorf("policy JSON: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return p, errors.New("policy must contain exactly one JSON value")
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Policy{}, fmt.Errorf("policy decode: %w", err)
	}
	if err := Validate(p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// checkShape enforces exact field spelling as encoding/json otherwise ignores case.
func checkShape(d *json.Decoder, typ reflect.Type) error {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return errors.New("null is not permitted")
	}
	switch typ.Kind() {
	case reflect.Struct:
		if tok != json.Delim('{') {
			return errors.New("expected object")
		}
		fields := make(map[string]reflect.StructField, typ.NumField())
		required := 0
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			fields[tag[0]] = f
			if len(tag) == 1 {
				required++
			}
		}
		seen := make(map[string]bool, len(fields))
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("expected field")
			}
			f, ok := fields[name]
			if !ok || seen[name] {
				return errors.New("unknown or duplicate field")
			}
			seen[name] = true
			if !strings.Contains(f.Tag.Get("json"), ",omitempty") {
				required--
			}
			if err := checkShape(d, f.Type); err != nil {
				return err
			}
		}
		if required != 0 {
			return errors.New("missing required field")
		}
		_, err = d.Token()
		return err
	case reflect.Slice:
		if tok != json.Delim('[') {
			return errors.New("expected array")
		}
		for n := 0; d.More(); n++ {
			if n >= MaxTargets {
				return errors.New("array exceeds limit")
			}
			if err := checkShape(d, typ.Elem()); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case reflect.String:
		if _, ok := tok.(string); !ok {
			return errors.New("expected string")
		}
	case reflect.Int:
		n, ok := tok.(json.Number)
		if !ok {
			return errors.New("expected integer")
		}
		if _, err := n.Int64(); err != nil {
			return errors.New("expected integer")
		}
	default:
		return errors.New("unsupported policy field")
	}
	return nil
}

// encoding/json repairs unpaired surrogates; authorization identities must not repair.
func validEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		n, ok := hex4(data, i+1)
		if !ok {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, ok := hex4(data, i+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func hex4(data []byte, start int) (int, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	n := 0
	for _, c := range data[start : start+4] {
		n *= 16
		switch {
		case c >= '0' && c <= '9':
			n += int(c - '0')
		case c >= 'a' && c <= 'f':
			n += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			n += int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return n, true
}
