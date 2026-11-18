package attribution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validSHA(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range v {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// JSON must have one unambiguous document. encoding/json alone accepts duplicate
// members; reject them before decoding so no auditor and engine disagree.
const maxJSONDepth = 128

func strictJSON(raw []byte, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("JSON document missing")
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("JSON contains invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	// Go struct decoding accepts case-insensitive aliases. The wire contract
	// does not: enforce exact JSON tags while preserving free-map key semantics.
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return fmt.Errorf("JSON syntax invalid")
	}
	if err := exactFields(value, reflect.TypeOf(target)); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("JSON schema: %w", err)
	}
	return nil
}

func uniqueValue(d *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("JSON nesting limit exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return fmt.Errorf("JSON syntax: %w", err)
	}
	open, isContainer := token.(json.Delim)
	if !isContainer {
		return nil
	}
	if open != '{' && open != '[' {
		return fmt.Errorf("unexpected JSON delimiter")
	}
	seen := map[string]bool{}
	for d.More() {
		if open == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON member")
			}
			seen[name] = true
		}
		if err := uniqueValue(d, depth+1); err != nil {
			return err
		}
	}
	close, err := d.Token()
	if err != nil || (open == '{' && close != json.Delim('}')) || (open == '[' && close != json.Delim(']')) {
		return fmt.Errorf("invalid JSON container ending")
	}
	return nil
}

func exactFields(value any, t reflect.Type) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() == reflect.Interface || t == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fields[name] = f.Type
		}
		for name, child := range object {
			fieldType, ok := fields[name]
			if !ok {
				return fmt.Errorf("JSON schema field name is not exact or is unsupported")
			}
			if err := exactFields(child, fieldType); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if items, ok := value.([]any); ok {
			for _, child := range items {
				if err := exactFields(child, t.Elem()); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if object, ok := value.(map[string]any); ok {
			for _, child := range object {
				if err := exactFields(child, t.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sameJSON(a, b []byte) bool {
	var va, vb any
	if strictJSON(a, &va) != nil || strictJSON(b, &vb) != nil {
		return false
	}
	ca, ea := json.Marshal(va)
	cb, eb := json.Marshal(vb)
	return ea == nil && eb == nil && bytes.Equal(ca, cb)
}
