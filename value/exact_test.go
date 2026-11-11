package value

import (
	"encoding/json"
	"testing"
)

func TestExactIntegerLexemesAndStorageBounds(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want int64
	}{{"1.0", 1}, {"1e0", 1}, {"10e-1", 1}, {"9007199254740993.0", 9007199254740993}, {"9223372036854775807.000", 9223372036854775807}, {"-9223372036854775808.0", -9223372036854775808}} {
		t.Run(test.raw, func(t *testing.T) {
			var field Field[int64]
			if e := json.Unmarshal([]byte(test.raw), &field); e != nil {
				t.Fatal(e)
			}
			got, ok := field.Get()
			if !ok || got != test.want {
				t.Fatal("exact integer changed")
			}
			raw, e := json.Marshal(field)
			if e != nil || string(raw) != test.raw {
				t.Fatalf("numeric lexeme lost: %s %v", raw, e)
			}
		})
	}
	for _, bad := range []string{`"1"`, "1.00000000000000000000001", "9223372036854775808.0", "-9223372036854775809.0", "true", "{}", "1e999999999"} {
		var field Field[int64]
		if e := json.Unmarshal([]byte(bad), &field); e == nil {
			t.Errorf("invalid/out-of-range integer accepted: %s", bad)
		}
	}
}
func TestExactIntegerListPreservesRawUntilChanged(t *testing.T) {
	var f Field[[]int64]
	if e := json.Unmarshal([]byte(`[1.0,2e0]`), &f); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(f)
	if string(raw) != `[1.0,2e0]` {
		t.Fatal("integer list lexemes lost")
	}
	v, _ := f.Get()
	v[0] = 3
	raw, _ = json.Marshal(f)
	if string(raw) != `[3,2]` {
		t.Fatal("stale original bytes hid a typed mutation")
	}
	if e := json.Unmarshal([]byte(`[1.1]`), &f); e == nil {
		t.Fatal("fractional list member rounded")
	}
}
