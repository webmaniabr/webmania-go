package webmania

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/webmaniabr/webmania-go/value"
)

// Error retains only bounded sanitized diagnostics. It never wraps a raw network cause.
type Error struct {
	Kind          string             `json:"kind"`
	Operation     string             `json:"operation,omitempty"`
	Status        int                `json:"status,omitempty"`
	Outcome       string             `json:"outcome,omitempty"`
	CorrelationID string             `json:"correlation_id,omitempty"`
	Diagnostics   any                `json:"diagnostics,omitempty"`
	Reference     *RecoveryReference `json:"reference,omitempty"`
	Location      *ProtectedLocation `json:"location,omitempty"`
	cancellation  error
}

func (e *Error) Error() string        { return "Webmania " + e.Kind + " failure" }
func (e *Error) Is(target error) bool { return e.cancellation != nil && target == e.cancellation }
func (e *Error) Format(f fmt.State, verb rune) {
	fmt.Fprintf(f, "Webmania error(kind=%s, status=%d, outcome=%s)", e.Kind, e.Status, e.Outcome)
}
func newError(k string) *Error { return &Error{Kind: k} }
func canceledError(err error) *Error {
	e := newError("transport")
	if err == context.Canceled || err == context.DeadlineExceeded {
		e.Kind = "cancelled"
		e.cancellation = err
	}
	return e
}

// RecoveryReference protects caller-provided lookup selectors. Reveal explicitly returns a copy.
type RecoveryReference struct{ raw []byte }

func (r *RecoveryReference) Reveal() map[string]any {
	if r == nil {
		return nil
	}
	v, _ := value.Decode(r.raw, 64)
	return v.(map[string]any)
}
func (r *RecoveryReference) String() string                { return "RecoveryReference([redacted])" }
func (r *RecoveryReference) GoString() string              { return r.String() }
func (r *RecoveryReference) MarshalJSON() ([]byte, error)  { return []byte(`"[redacted]"`), nil }
func (r *RecoveryReference) Format(f fmt.State, verb rune) { fmt.Fprint(f, r.String()) }

// ProtectedLocation contains untrusted metadata; Reveal does not authorize a download.
type ProtectedLocation struct{ raw string }

func (p *ProtectedLocation) Reveal() string {
	if p == nil {
		return ""
	}
	return p.raw
}
func (p *ProtectedLocation) String() string                { return "ProtectedLocation([redacted])" }
func (p *ProtectedLocation) GoString() string              { return p.String() }
func (p *ProtectedLocation) MarshalJSON() ([]byte, error)  { return []byte(`"[redacted]"`), nil }
func (p *ProtectedLocation) Format(f fmt.State, verb rune) { fmt.Fprint(f, p.String()) }

var uuidPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}`)
var sensitive = regexp.MustCompile(`(?i)password|senha|certificate|certificado|token|secret|consumer.?key|authorization|credential|credencia|url|xml|pdf|conteudo|content|base64`)
var urlPattern = regexp.MustCompile(`(?i)(?:https?|ftp)://[^\s<>"']+`)

type redactor struct {
	values   []string
	uuids    []string
	numbers  map[string]bool
	observed []string
}

func newRedactor(secrets []string, snapshot any, uuids []string) *redactor {
	r := &redactor{numbers: map[string]bool{}}
	seen := map[string]bool{}
	literalOnly := map[string]bool{}
	for _, s := range secrets {
		literalOnly[s] = true
	}
	add := func(s string) {
		if s == "" {
			return
		}

		for _, v := range []string{s, norm.NFC.String(s), norm.NFD.String(s), url.QueryEscape(s), url.PathEscape(s)} {
			if !seen[v] {
				seen[v] = true
				r.values = append(r.values, v)
			}
		}
	}
	for _, s := range secrets {
		add(s)
	}
	numeric := func(s string) {
		if !literalOnly[s] {
			if n, ok := diagnosticNumber(s); ok {
				r.numbers[n.RatString()] = true
			}
		}
	}
	walk(snapshot, func(k string, v any) {
		if k != "" && !publicDiagnosticKeys[k] {
			add(k)
			numeric(k)
		}
		switch x := v.(type) {
		case string:
			add(x)
			numeric(x)
			if isUUID(x) && isUUIDKey(k) {
				uuids = append(uuids, x)
			}
		case json.Number:
			add(x.String())
			numeric(x.String())
		}
	})
	for _, u := range uuids {
		if isUUID(u) {
			r.uuids = append(r.uuids, strings.ToLower(u))
		}
	}

	return r
}
func walk(v any, f func(string, any)) {
	var visit func(string, any)
	visit = func(k string, v any) {
		f(k, v)
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				visit(k, c)
			}
		case []any:
			for _, c := range x {
				visit("", c)
			}
		}
	}
	visit("", v)
}
func isUUID(s string) bool { return len(s) == 36 && uuidPattern.MatchString(s) }
func isUUIDKey(k string) bool {
	switch strings.ToLower(k) {
	case "uuid", "fd", "report", "company", "download", "empresa_uuid":
		return true
	}
	return false
}

// Redaction marks the union of matches in the original text before replacing.
// Sequential replacement can expose the suffix of an overlapping credential.
func (r *redactor) text(s string) string {
	if len(s) > 4096 {
		return "[truncated]"
	}
	mask := make([]bool, len(s))
	mark := func(a, b int) {
		for i := a; i < b; i++ {
			mask[i] = true
		}
	}
	for _, m := range urlPattern.FindAllStringIndex(s, -1) {
		mark(m[0], m[1])
	}
	for _, m := range diagnosticNumberPattern.FindAllStringIndex(s, -1) {
		v := s[m[0]:m[1]]
		if n, ok := diagnosticNumber(v); ok && r.numbers[n.RatString()] {
			mark(m[0], m[1])
		}
	}
	for _, m := range uuidPattern.FindAllStringIndex(s, -1) {
		for _, u := range r.uuids {
			if strings.EqualFold(s[m[0]:m[1]], u) {
				mark(m[0], m[1])
				break
			}
		}
	}
	match := func(v string, bounded bool) {
		if v == "" || len(v) > len(s) {
			return
		}
		for pos := 0; pos+len(v) <= len(s); {
			i := strings.Index(s[pos:], v)
			if i < 0 {
				break
			}
			i += pos
			j := i + len(v)
			if !bounded || tokenEdges(s, i, j) {
				mark(i, j)
			}
			pos = i + 1
		}
	}
	for _, v := range r.values {
		match(v, false)
	}
	for _, v := range r.observed {
		match(v, utf8.RuneCountInString(v) <= 3)
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if !mask[i] {
			out.WriteByte(s[i])
			i++
			continue
		}
		out.WriteString("[redacted]")
		for i < len(s) && mask[i] {
			i++
		}
	}
	result := out.String()
	if len(result) > 1024 {
		end := 1024
		for end > 0 && !utf8.ValidString(result[:end]) {
			end--
		}
		result = result[:end]
	}
	return result
}

var diagnosticNumberPattern = regexp.MustCompile(`[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?`)

func tokenEdges(s string, a, b int) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
	if a > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:a])
		if word(r) {
			return false
		}
	}
	if b < len(s) {
		r, _ := utf8.DecodeRuneInString(s[b:])
		if word(r) {
			return false
		}
	}
	return true
}
func (r *redactor) observe(values []string) {
	seen := map[string]bool{}
	for _, s := range values {
		for _, v := range []string{s, norm.NFC.String(s), norm.NFD.String(s), url.QueryEscape(s), url.PathEscape(s)} {
			if v != "" && v != "/" && len(v) <= 4096 && !seen[v] {
				seen[v] = true
				r.observed = append(r.observed, v)
			}
		}
	}
}
func (r *redactor) private(s string) bool { return r.text(s) != s || urlPattern.MatchString(s) }
func (r *redactor) clean(v any) any {
	budget := 512
	var clean func(any, int) any
	clean = func(v any, d int) any {
		budget--
		if budget < 0 || d > 12 {
			return "[truncated]"
		}
		switch x := v.(type) {
		case map[string]any:
			m := map[string]any{}
			i := 0
			for k, c := range x {
				if i >= 64 {
					m["[truncated]"] = true
					break
				}
				sk := r.text(k)
				if _, ok := m[sk]; ok {
					sk = fmt.Sprintf("[redacted-key-%d]", i)
				}
				if sensitive.MatchString(k) {
					m[sk] = "[redacted]"
				} else {
					m[sk] = clean(c, d+1)
				}
				i++
			}
			return m
		case []any:
			a := []any{}
			for i, c := range x {
				if i >= 64 {
					a = append(a, "[truncated]")
					break
				}
				a = append(a, clean(c, d+1))
			}
			return a
		case string:
			return r.text(x)
		case json.Number:
			if r.private(x.String()) {
				return "[redacted]"
			}
			return x
		case bool:
			return x
		case nil:
			return nil
		}
		return "[redacted]"
	}
	return clean(v, 0)
}
func recovery(body any, path map[string]any, secrets []string, uuids []string, capability bool, observed ...[]string) *RecoveryReference {
	if capability {
		return nil
	}
	forbidden := append([]string(nil), secrets...)
	walk(body, func(k string, v any) {
		if sensitive.MatchString(k) {
			walk(v, func(key string, c any) {
				if key != "" {
					forbidden = append(forbidden, key)
				}
				switch x := c.(type) {
				case string:
					forbidden = append(forbidden, x)
				case json.Number:
					forbidden = append(forbidden, x.String())
				}
			})
		}
	})
	r := newRedactor(forbidden, nil, uuids)
	for _, v := range observed {
		r.observe(v)
	}
	m := map[string]any{}
	for _, k := range []string{"ID", "id", "uuid", "chave"} {
		if v, ok := object(body)[k]; ok {
			m[k] = v
		}
	}
	for k, v := range path {
		m[k] = v
	}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x == "" || r.private(x) {
				delete(m, k)
			}
		case json.Number:
			if r.private(x.String()) {
				delete(m, k)
			}
		default:
			delete(m, k)
		}
	}
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return &RecoveryReference{b}
}

// observeLocation lexically isolates path before parsing, including malformed authorities/escapes.
func observeLocation(raw string) (secrets, uuids []string) {
	secrets = append(secrets, raw, strings.TrimSpace(raw))
	ref := strings.Trim(raw, " \t")
	fragments := strings.SplitN(ref, "#", 2)
	ref = fragments[0]
	if len(fragments) > 1 {
		secrets = append(secrets, fragments[1])
	}
	parts := strings.SplitN(ref, "?", 2)
	path := parts[0]
	if len(parts) > 1 {
		for _, p := range strings.Split(parts[1], "&") {
			secrets = append(secrets, p)
			if kv := strings.SplitN(p, "=", 2); len(kv) == 2 {
				secrets = append(secrets, kv[1])
				if s, e := url.QueryUnescape(kv[1]); e == nil {
					secrets = append(secrets, s)
				}
			}
		}
	}
	if i := strings.Index(path, "://"); i >= 0 {
		tail := path[i+3:]
		if j := strings.IndexByte(tail, '/'); j >= 0 {
			secrets = append(secrets, authoritySecrets(tail[:j])...)
			path = tail[j:]
		} else {
			secrets = append(secrets, authoritySecrets(tail)...)
			path = ""
		}
	} else if strings.HasPrefix(path, "//") {
		tail := path[2:]
		if j := strings.IndexByte(tail, '/'); j >= 0 {
			secrets = append(secrets, authoritySecrets(tail[:j])...)
			path = tail[j:]
		} else {
			secrets = append(secrets, authoritySecrets(tail)...)
			path = ""
		}
	}
	// Decode valid percent octets independently; invalid neighbors must not hide a path capability.
	decoded := regexp.MustCompile(`%[0-9a-fA-F]{2}`).ReplaceAllStringFunc(path, func(s string) string { v, _ := url.PathUnescape(s); return v })
	secrets = append(secrets, path, decoded)
	secrets = append(secrets, strings.Split(decoded, "/")...)
	for _, u := range uuidPattern.FindAllString(decoded, -1) {
		uuids = append(uuids, u)
	}
	for _, v := range append([]string{}, secrets...) {
		decoded := regexp.MustCompile(`%[0-9a-fA-F]{2}`).ReplaceAllStringFunc(v, func(s string) string { x, _ := url.PathUnescape(s); return x })
		if decoded != v {
			secrets = append(secrets, decoded)
		}
	}
	// Encoded delimiters and fragment assignments are a privacy observation
	// policy only. They never change URI routing, origin trust or navigation.
	decodedRef := strings.Trim(raw, " \t")
	for pass := 0; pass < 2; pass++ {
		next := regexp.MustCompile(`%[0-9a-fA-F]{2}`).ReplaceAllStringFunc(decodedRef, func(s string) string { x, _ := url.PathUnescape(s); return x })
		for _, part := range strings.FieldsFunc(next, func(r rune) bool { return r == '?' || r == '#' || r == '&' || r == ';' }) {
			if _, v, ok := strings.Cut(part, "="); ok {
				secrets = append(secrets, v)
			}
		}
		if next == decodedRef {
			break
		}
		decodedRef = next
	}
	if len(fragments) > 1 {
		for _, part := range strings.FieldsFunc(fragments[1], func(r rune) bool { return r == '&' || r == ';' }) {
			if _, v, ok := strings.Cut(part, "="); ok {
				secrets = append(secrets, v)
				if x, e := url.QueryUnescape(v); e == nil {
					secrets = append(secrets, x)
				}
			}
		}
	}
	return
}
func protectedLocation(raw string) (*ProtectedLocation, error) {
	s := strings.Trim(raw, " \t")
	if s == "" || len(s) > 65536 || strings.ContainsAny(s, "\\\r\n\t ") || strings.HasPrefix(s, "///") {
		return nil, newError("protocol")
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return nil, newError("protocol")
		}
	}
	u, e := url.Parse(s)
	if e != nil || u.User != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || (u.Scheme != "" && u.Host == "") || strings.HasSuffix(u.Host, ":") {
		return nil, newError("protocol")
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return nil, newError("protocol")
		}
	}
	if strings.Contains(s, ",http") || strings.Contains(s, ",//") {
		return nil, newError("protocol")
	}
	if u.Host != "" && (!validHost(u.Hostname()) || strings.ContainsAny(u.Hostname(), " ,%")) {
		return nil, newError("protocol")
	}
	decoded, e := url.PathUnescape(s)
	if e != nil || strings.ContainsAny(decoded, "\x00\r\n") {
		return nil, newError("protocol")
	}
	return &ProtectedLocation{raw: s}, nil
}

func authoritySecrets(a string) []string {
	v := []string{a}
	if i := strings.LastIndexByte(a, '@'); i >= 0 {
		user := a[:i]
		v = append(v, user)
		v = append(v, strings.Split(user, ":")...)
	}
	for _, s := range append([]string{}, v...) {
		if decoded, e := url.PathUnescape(s); e == nil {
			v = append(v, decoded)
		}
	}
	return v
}

func diagnosticNumber(s string) (*big.Rat, bool) {
	if len(s) > 256 || !value.BoundedNumber(s, 128) {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}
