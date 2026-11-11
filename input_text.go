package webmania

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// Inspect supported Go string boundaries before encoding/json can replace
// invalid UTF8. This also sees strings inside value.Field's private storage.
func validInputText(v any) bool {
	budget := 1 << 20
	rawType := reflect.TypeOf(json.RawMessage{})
	var visit func(reflect.Value, int) bool
	visit = func(v reflect.Value, d int) bool {
		budget--
		if budget < 0 || d > 128 {
			return false
		}
		if !v.IsValid() {
			return true
		}
		switch v.Kind() {
		case reflect.String:
			return utf8.ValidString(v.String())
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				return visit(v.Elem(), d+1)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if !visit(v.Field(i), d+1) {
					return false
				}
			}
		case reflect.Map:
			if v.IsNil() {
				return true
			}
			it := v.MapRange()
			for it.Next() {
				if !visit(it.Key(), d+1) || !visit(it.Value(), d+1) {
					return false
				}
			}
		case reflect.Slice, reflect.Array:
			if v.Type() == rawType {
				return utf8.Valid(v.Bytes())
			}
			// Ordinary byte slices are base64 in JSON, not text boundaries.
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return true
			}
			for i := 0; i < v.Len(); i++ {
				if !visit(v.Index(i), d+1) {
					return false
				}
			}
		}
		return true
	}
	return visit(reflect.ValueOf(v), 0)
}
