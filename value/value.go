// Package value preserves JSON presence, null, and exact numeric lexemes.
package value

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Field distinguishes omitted, explicitly null, and a supplied zero/false value.
type Field[T any] struct {
	v         T
	state     uint8
	raw       json.RawMessage
	canonical []byte
}

func Some[T any](v T) Field[T]    { return Field[T]{v: v, state: 2} }
func Null[T any]() Field[T]       { return Field[T]{state: 1} }
func (f Field[T]) IsZero() bool   { return f.state == 0 }
func (f Field[T]) IsSet() bool    { return f.state != 0 }
func (f Field[T]) IsNull() bool   { return f.state == 1 }
func (f Field[T]) Get() (T, bool) { return f.v, f.state == 2 }
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if f.state != 2 {
		return []byte("null"), nil
	}
	b, e := json.Marshal(f.v)
	if e != nil {
		return nil, e
	}
	if f.raw != nil && bytes.Equal(b, f.canonical) {
		return append([]byte(nil), f.raw...), nil
	}
	return b, nil
}
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		f.state = 1
		var z T
		f.v = z
		f.raw = nil
		f.canonical = nil
		return nil
	}
	var next T
	changed, e := unmarshalTyped(b, reflect.ValueOf(&next).Elem())
	if e != nil {
		return errors.New("invalid typed JSON field")
	}
	f.v = next
	f.raw = nil
	f.canonical = nil
	if changed {
		f.raw = append([]byte(nil), b...)
		f.canonical, _ = json.Marshal(next)
	}
	f.state = 2
	return nil
}

// Scalar represents a declared scalar union. Numeric input is json.Number, never float64.
type Scalar struct{ raw json.RawMessage }

func String(v string) Scalar {
	if !utf8.ValidString(v) {
		return Scalar{json.RawMessage{0xff}}
	}
	b, _ := json.Marshal(v)
	return Scalar{b}
}
func Number(v json.Number) Scalar { return Scalar{json.RawMessage(v.String())} }
func Boolean(v bool) Scalar {
	if v {
		return Scalar{[]byte("true")}
	}
	return Scalar{[]byte("false")}
}
func (s Scalar) MarshalJSON() ([]byte, error) {
	if s.raw == nil {
		return []byte("null"), nil
	}
	v, e := Decode(s.raw, 64)
	if e != nil {
		return nil, errors.New("invalid JSON scalar")
	}
	switch v.(type) {
	case map[string]any, []any:
		return nil, errors.New("expected JSON scalar")
	}
	return append([]byte(nil), s.raw...), nil
}
func (s *Scalar) UnmarshalJSON(b []byte) error {
	v, e := Decode(b, 64)
	if e != nil {
		return e
	}
	switch v.(type) {
	case map[string]any, []any:
		return errors.New("expected JSON scalar")
	}
	s.raw = append(s.raw[:0], b...)
	return nil
}
func (s Scalar) Decode() any { v, _ := Decode(s.raw, 64); return v }

// MarshalObject merges explicit extensions, refusing collisions even for omitted fields.
func MarshalObject(v any, extra map[string]json.RawMessage) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, errors.New("invalid model fields")
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	t := reflect.TypeOf(v)
	known := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		k := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if k != "-" && k != "" {
			known[k] = i
		}
	}
	for k, raw := range extra {
		if index, ok := known[k]; ok {
			field := t.Field(index)
			isZero, hasZero := reflect.ValueOf(v).Field(index).Interface().(interface{ IsZero() bool })
			if field.Tag.Get("wm") != "alternative" || !hasZero || !isZero.IsZero() {
				return nil, errors.New("extension collides with a declared field")
			}
		}
		if !json.Valid(raw) {
			return nil, errors.New("invalid extension JSON")
		}
		m[k] = raw
	}
	return json.Marshal(m)
}
func UnmarshalObject(b []byte, v any, extra *map[string]json.RawMessage) error {
	if _, e := Decode(b, 64); e != nil {
		return errors.New("invalid typed JSON object")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return errors.New("invalid JSON object")
	}
	target := reflect.ValueOf(v).Elem()
	t := target.Type()
	extras := map[string]json.RawMessage{}
	known := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		key := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if key != "" && key != "-" {
			known[key] = i
		}
	}
	for key, raw := range fields {
		index, ok := known[key]
		if !ok {
			extras[key] = raw
			continue
		}
		if _, e := unmarshalTyped(raw, target.Field(index)); e != nil {
			if t.Field(index).Tag.Get("wm") == "alternative" {
				target.Field(index).SetZero()
				extras[key] = raw
				continue
			}
			return errors.New("invalid typed JSON object")
		}
	}
	*extra = extras
	return nil
}

// Decode rejects duplicate object keys and excessive depth while retaining numbers exactly.
func Decode(b []byte, maxDepth int) (any, error) {
	if !utf8.Valid(b) || !validUnicodeEscapes(b) {
		return nil, errors.New("invalid UTF8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if depth > maxDepth {
			return nil, errors.New("JSON depth limit")
		}
		t, e := d.Token()
		if e != nil {
			return nil, errors.New("invalid JSON")
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				m := map[string]any{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return nil, e
					}
					s, ok := k.(string)
					if !ok {
						return nil, errors.New("invalid object key")
					}
					if _, ok = m[s]; ok {
						return nil, errors.New("duplicate JSON key")
					}
					v, e := read(depth + 1)
					if e != nil {
						return nil, e
					}
					m[s] = v
				}
				if _, e = d.Token(); e != nil {
					return nil, e
				}
				return m, nil
			case '[':
				a := []any{}
				for d.More() {
					v, e := read(depth + 1)
					if e != nil {
						return nil, e
					}
					a = append(a, v)
				}
				if _, e = d.Token(); e != nil {
					return nil, e
				}
				return a, nil
			default:
				return nil, errors.New("invalid JSON delimiter")
			}
		}
		if n, ok := t.(json.Number); ok && !BoundedNumber(n.String(), 10000) {
			return nil, errors.New("JSON numeric representation limit")
		}
		return t, nil
	}
	v, e := read(0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return v, nil
}

// Union preserves a typed alternative when the contract permits distinct JSON shapes.
// The complete runtime schema is validated before response decoding.
type Union[A, B any] struct {
	first  A
	second B
	which  uint8
}

func First[A, B any](v A) Union[A, B]   { return Union[A, B]{first: v, which: 1} }
func Second[A, B any](v B) Union[A, B]  { return Union[A, B]{second: v, which: 2} }
func (u Union[A, B]) First() (A, bool)  { return u.first, u.which == 1 }
func (u Union[A, B]) Second() (B, bool) { return u.second, u.which == 2 }
func (u Union[A, B]) MarshalJSON() ([]byte, error) {
	if u.which == 1 {
		return json.Marshal(u.first)
	}
	if u.which == 2 {
		return json.Marshal(u.second)
	}
	return []byte("null"), nil
}
func (u *Union[A, B]) UnmarshalJSON(b []byte) error {
	var a A
	if e := json.Unmarshal(b, &a); e == nil {
		u.first = a
		u.which = 1
		return nil
	}
	var c B
	if e := json.Unmarshal(b, &c); e == nil {
		u.second = c
		u.which = 2
		return nil
	}
	return errors.New("invalid typed union")
}

// BoundedNumber limits numeric lexemes before arbitrary-precision validation.
func BoundedNumber(s string, exponentLimit int) bool {
	if len(s) > 512 {
		return false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		n, e := strconv.Atoi(s[i+1:])
		if e != nil || n > exponentLimit || n < -exponentLimit {
			return false
		}
	}
	return true
}

// encoding/json substitutes isolated surrogate escapes; reject that lossy
// boundary while still allowing a literal escaped backslash followed by u.
func validUnicodeEscapes(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		if c == '"' {
			inString = false
			continue
		}
		if c != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		n, e := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
	}
	return true
}
