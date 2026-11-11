package webmania

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAlpha3PrivacyUnionAndSourcePolicies(t *testing.T) {
	for _, tc := range []struct {
		secrets    []string
		text, want string
	}{
		{[]string{"abcde", "defgh"}, "abcdefgh", "[redacted]"},
		{[]string{"abc", "bcdef", "efghi"}, "abcdefghij", "[redacted]j"},
		{[]string{"1234"}, "912345", "9[redacted]5"},
		{[]string{"x"}, "tax", "ta[redacted]"},
		{[]string{"0bad", "BADF00D0-0000-4000-8000-000000000001"}, "BADF00D0-0000-4000-8000-000000000001bad", "[redacted]bad"},
	} {
		r := newRedactor(tc.secrets, nil, nil)
		if got := r.text(tc.text); got != tc.want {
			t.Errorf("union got %q want %q", got, tc.want)
		}
	}
	r := newRedactor(nil, map[string]any{"status": true, "informacoes_fisco": "PrivateFiscal", "privateExtraKey": "PrivateValue"}, nil)
	r.observe([]string{"ab"})
	if got := r.text("token ab; table stable"); got != "token [redacted]; table stable" {
		t.Fatal(got)
	}
	got := r.clean(map[string]any{"status": true, "informacoes_fisco": "Use a valid CFOP", "privateExtraKey": "PrivateFiscal PrivateValue"})
	b, _ := json.Marshal(got)
	s := string(b)
	if !strings.Contains(s, `"status":true`) || !strings.Contains(s, `"informacoes_fisco"`) || strings.Contains(s, "PrivateFiscal") || strings.Contains(s, "PrivateValue") || strings.Contains(s, "privateExtraKey") {
		t.Fatal(s)
	}
	for _, loc := range []string{"https://invalid.test/report%3Ftoken%3DopaqueEncoded", "https://invalid.test/report#token=opaqueEncoded", "https://invalid.test/report#token%3DopaqueEncoded"} {
		observed, uuids := observeLocation(loc)
		r := newRedactor(nil, nil, uuids)
		r.observe(observed)
		if r.text("opaqueEncoded") == "opaqueEncoded" {
			t.Fatal("observation gap")
		}
	}
}

func TestAlpha3StrictInputText(t *testing.T) {
	for _, v := range []any{string([]byte{0xff}), map[string]any{string([]byte{0xff}): "x"}, map[string]any{"x": json.RawMessage{34, 0xff, 34}}} {
		if validInputText(v) {
			t.Fatal("invalid input accepted")
		}
	}
	if !validInputText(map[string]any{"unicode": "ação fiscal", "bytes": []byte{0xff}}) {
		t.Fatal("supported text/opaque bytes refused")
	}
}
func TestAlpha3AuthorityConstruction(t *testing.T) {
	for _, u := range []string{"https://host.test:", "https://host.test:0", "https://host.test:65536", "https://[::1]:", "https://[::1]:0", "https://[::1]:65536", "https://::1", "https://[127.0.0.1]", "https://host.test:1:2"} {
		if _, e := newTransport(Config{BaseURL: u}, ""); e == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, u := range []string{"https://host.test:1", "https://host.test:65535", "http://[::1]:8080", "https://host.test"} {
		if _, e := newTransport(Config{BaseURL: u}, ""); e != nil {
			t.Errorf("refused %s", u)
		}
	}
}
