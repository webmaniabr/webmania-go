package webmania

import (
	"encoding/json"
	"github.com/webmaniabr/webmania-go/value"
	"os"
	"strings"
	"testing"
)

func TestSemanticCorpus(t *testing.T) {
	b, e := os.ReadFile("tests/semantic_cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Cases []struct {
			Name, Schema, Module string
			Valid                bool
			Instance             json.RawMessage
		}
	}
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			v, e := value.Decode(c.Instance, 64)
			if e != nil {
				t.Fatal(e)
			}
			e = schemaCheck(c.Module, map[string]any{"$ref": "#/components/schemas/" + c.Schema}, v)
			if (e == nil) != c.Valid {
				t.Fatalf("valid=%v, got %v", c.Valid, e)
			}
		})
	}
}

func TestNumericRepresentationBounds(t *testing.T) {
	for _, raw := range []string{`1e999999999`, `1e-999999999`, string(append([]byte("1"), []byte(strings.Repeat("0", 513))...))} {
		if _, e := value.Decode([]byte(raw), 64); e == nil {
			t.Error("excessive numeric representation accepted")
		}
	}
	v, e := value.Decode([]byte(`9007199254740993123456789.1234567890123456789`), 64)
	if e != nil || v.(json.Number).String() != `9007199254740993123456789.1234567890123456789` {
		t.Fatal("exact JSON number lost")
	}
}
