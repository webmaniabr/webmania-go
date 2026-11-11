package webmania

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"

	js "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/webmaniabr/webmania-go/value"
	"golang.org/x/text/message"
)

//go:embed contracts/*.json
var contractFS embed.FS

type operation struct {
	Module     string         `json:"module"`
	Path       string         `json:"path"`
	Verb       string         `json:"verb"`
	Service    string         `json:"service"`
	Query      map[string]any `json:"query"`
	Spec       map[string]any `json:"spec"`
	Parameters []parameter    `json:"parameters"`
}
type parameter struct {
	Name     string         `json:"name"`
	In       string         `json:"in"`
	Required bool           `json:"required"`
	Schema   map[string]any `json:"schema"`
}

var operations map[string]operation
var documents map[string]map[string]any
var publicDiagnosticKeys = map[string]bool{"body": true, "query": true, "path": true}
var schemaMu sync.Mutex
var schemaCache = map[string]*js.Schema{}

func init() {
	b, e := contractFS.ReadFile("contracts/operations.json")
	if e != nil {
		panic("embedded operation catalog missing")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&operations); e != nil {
		panic("embedded operation catalog invalid")
	}
	documents = map[string]map[string]any{}
	for _, o := range operations {
		if _, ok := documents[o.Module]; ok {
			continue
		}
		b, e := contractFS.ReadFile("contracts/" + o.Module + ".json")
		if e != nil {
			panic("embedded contract missing")
		}
		v, e := value.Decode(b, 128)
		if e != nil {
			panic("embedded contract invalid")
		}
		documents[o.Module] = v.(map[string]any)
		walk(v, func(k string, v any) {
			if k == "properties" {
				for name := range object(v) {
					publicDiagnosticKeys[name] = true
				}
			}
		})
	}
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func at(v any, keys ...string) any {
	for _, k := range keys {
		v = object(v)[k]
	}
	return v
}
func schemaCheck(module string, schema any, v any) error {
	if schema == nil {
		return nil
	}
	b, _ := json.Marshal(schema)
	key := fmt.Sprintf("%s:%x", module, sha256.Sum256(b))
	schemaMu.Lock()
	s := schemaCache[key]
	if s == nil {
		c := js.NewCompiler()
		c.DefaultDraft(js.Draft2020)
		c.AssertFormat()
		c.AssertVocabs()
		c.RegisterVocabulary(decimalVocabulary())
		c.UseLoader(noLoader{})
		wrapped := map[string]any{"components": documents[module]["components"], "allOf": []any{schema}}
		if e := c.AddResource("https://sdk.invalid/schema.json", wrapped); e != nil {
			schemaMu.Unlock()
			return newError("contract")
		}
		var e error
		s, e = c.Compile("https://sdk.invalid/schema.json")
		if e != nil {
			schemaMu.Unlock()
			return newError("contract")
		}
		schemaCache[key] = s
	}
	schemaMu.Unlock()
	if e := s.Validate(v); e != nil {
		return newError("validation")
	}
	return nil
}

type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external contract resources prohibited")
}

type decimalBounds struct{ minimum, maximum *big.Rat }

func (s *decimalBounds) Validate(c *js.ValidatorContext, v any) {
	var text string
	switch x := v.(type) {
	case string:
		text = x
	case json.Number:
		text = x.String()
	default:
		return
	}
	if !value.BoundedNumber(text, 10000) {
		c.AddError(decimalError{})
		return
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok || (s.minimum != nil && n.Cmp(s.minimum) < 0) || (s.maximum != nil && n.Cmp(s.maximum) > 0) {
		c.AddError(decimalError{})
	}
}

type decimalError struct{}

func (decimalError) KeywordPath() []string                   { return []string{"x-sdk-numeric-bound"} }
func (decimalError) LocalizedString(*message.Printer) string { return "exact decimal bound violated" }

var decimalMeta *js.Schema
var decimalOnce sync.Once

func decimalVocabulary() *js.Vocabulary {
	decimalOnce.Do(func() {
		c := js.NewCompiler()
		v, _ := js.UnmarshalJSON(strings.NewReader(`{"properties":{"x-sdk-numeric-minimum":{"type":"string"},"x-sdk-numeric-maximum":{"type":"string"}}}`))
		_ = c.AddResource("https://sdk.invalid/decimal", v)
		decimalMeta, _ = c.Compile("https://sdk.invalid/decimal")
	})
	return &js.Vocabulary{URL: "https://sdk.invalid/decimal", Schema: decimalMeta, Compile: func(_ *js.CompilerContext, o map[string]any) (js.SchemaExt, error) {
		s := &decimalBounds{}
		var exists bool
		for _, k := range []string{"x-sdk-numeric-minimum", "x-sdk-numeric-maximum"} {
			if v, ok := o[k]; ok {
				exists = true
				n, ok := new(big.Rat).SetString(fmt.Sprint(v))
				if !ok {
					return nil, fmt.Errorf("invalid decimal bound")
				}
				if k == "x-sdk-numeric-minimum" {
					s.minimum = n
				} else {
					s.maximum = n
				}
			}
		}
		if !exists {
			return nil, nil
		}
		return s, nil
	}}
}
