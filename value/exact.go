package value

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
)

// UnmarshalExact binds exact JSON names and decodes integer values without float conversion.
// It preserves integer storage limits and never rounds fractional values.
func UnmarshalExact(b []byte, dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("expected JSON destination pointer")
	}
	if _, e := Decode(b, 64); e != nil {
		return errors.New("invalid exact JSON")
	}
	_, e := unmarshalTyped(b, v.Elem())
	return e
}

func unmarshalTyped(b []byte, v reflect.Value) (bool, error) {
	if !v.CanSet() {
		return false, errors.New("invalid JSON destination")
	}
	if v.CanAddr() {
		if u, ok := v.Addr().Interface().(json.Unmarshaler); ok {
			return false, u.UnmarshalJSON(b)
		}
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		decoded, e := Decode(b, 1)
		if e != nil {
			return false, errors.New("invalid exact integer")
		}
		num, ok := decoded.(json.Number)
		if !ok {
			return false, errors.New("expected exact JSON integer")
		}
		n, ok := new(big.Rat).SetString(num.String())
		if !ok || !n.IsInt() {
			return false, errors.New("expected exact JSON integer")
		}
		if v.Kind() >= reflect.Int && v.Kind() <= reflect.Int64 {
			if !n.Num().IsInt64() || v.OverflowInt(n.Num().Int64()) {
				return false, errors.New("JSON integer outside storage range")
			}
			v.SetInt(n.Num().Int64())
		} else {
			if !n.Num().IsUint64() || v.OverflowUint(n.Num().Uint64()) {
				return false, errors.New("JSON integer outside storage range")
			}
			v.SetUint(n.Num().Uint64())
		}
		canonical, _ := json.Marshal(v.Interface())
		return !bytes.Equal(bytes.TrimSpace(b), canonical), nil
	case reflect.Pointer:
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			v.SetZero()
			return false, nil
		}
		next := reflect.New(v.Type().Elem())
		changed, e := unmarshalTyped(b, next.Elem())
		if e == nil {
			v.Set(next)
		}
		return changed, e
	case reflect.Slice, reflect.Array:
		// []byte uses the standard base64 representation; RawMessage is handled above.
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return false, json.Unmarshal(b, v.Addr().Interface())
		}
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) && v.Kind() == reflect.Slice {
			v.SetZero()
			return false, nil
		}
		var parts []json.RawMessage
		if e := json.Unmarshal(b, &parts); e != nil {
			return false, errors.New("invalid JSON list")
		}
		if v.Kind() == reflect.Array && len(parts) != v.Len() {
			return false, errors.New("invalid JSON array length")
		}
		next := reflect.New(v.Type()).Elem()
		if v.Kind() == reflect.Slice {
			next = reflect.MakeSlice(v.Type(), len(parts), len(parts))
		}
		changed := false
		for i, p := range parts {
			c, e := unmarshalTyped(p, next.Index(i))
			if e != nil {
				return false, e
			}
			changed = changed || c
		}
		v.Set(next)
		return changed, nil
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(b, &fields); e != nil {
			return false, errors.New("invalid JSON object")
		}
		changed := false
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if key == "-" {
				continue
			}
			if key == "" {
				key = f.Name
			}
			if raw, ok := fields[key]; ok {
				c, e := unmarshalTyped(raw, v.Field(i))
				if e != nil {
					return false, e
				}
				changed = changed || c
			}
		}
		return changed, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return false, errors.New("JSON object requires string keys")
		}
		var parts map[string]json.RawMessage
		if e := json.Unmarshal(b, &parts); e != nil {
			return false, errors.New("invalid JSON object")
		}
		if parts == nil {
			v.SetZero()
			return false, nil
		}
		next := reflect.MakeMapWithSize(v.Type(), len(parts))
		changed := false
		for key, raw := range parts {
			item := reflect.New(v.Type().Elem()).Elem()
			c, e := unmarshalTyped(raw, item)
			if e != nil {
				return false, e
			}
			changed = changed || c
			k := reflect.New(v.Type().Key()).Elem()
			k.SetString(key)
			next.SetMapIndex(k, item)
		}
		v.Set(next)
		return changed, nil
	case reflect.Interface:
		decoded, e := Decode(b, 64)
		if e != nil {
			return false, e
		}
		if decoded == nil {
			v.SetZero()
		} else {
			v.Set(reflect.ValueOf(decoded))
		}
		return false, nil
	default:
		return false, json.Unmarshal(b, v.Addr().Interface())
	}
}
