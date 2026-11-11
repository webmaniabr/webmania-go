package value

import (
	"encoding/json"
	"testing"
)

func TestStrictUnicodeBoundaries(t *testing.T) {
	for _, s := range []string{`"\ud800"`, `"\udfff"`, `"\ud800\u1234"`, string([]byte{34, 0xff, 34})} {
		if _, e := Decode([]byte(s), 64); e == nil {
			t.Errorf("accepted invalid Unicode %q", s)
		}
	}
	for _, s := range []string{`"\ud83d\ude00"`, `"\\ud800"`, `"ação fiscal"`} {
		if _, e := Decode([]byte(s), 64); e != nil {
			t.Errorf("refused valid Unicode %q: %v", s, e)
		}
	}
	if _, e := json.Marshal(String(string([]byte{0xff}))); e == nil {
		t.Fatal("Scalar.String replaced invalid UTF8")
	}
}
