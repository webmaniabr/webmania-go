// Package webmania provides typed clients for Webmania Emissor Fiscal and Monitor Fiscal.
package webmania

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/webmaniabr/webmania-go/value"
)

const Version = "0.1.0-alpha.5"

// Service credential types cannot be substituted for one another.
type CompanyCredentials struct{ ConsumerKey, ConsumerSecret, AccessToken, AccessTokenSecret string }
type ResellerCredentials struct{ ConsumerKey, ConsumerSecret, AccessToken, AccessTokenSecret string }
type IssuerCredentials struct{ Token string }
type MonitorCredentials struct{ Token string }

func (c CompanyCredentials) String() string                { return "CompanyCredentials([redacted])" }
func (c ResellerCredentials) String() string               { return "ResellerCredentials([redacted])" }
func (c IssuerCredentials) String() string                 { return "IssuerCredentials([redacted])" }
func (c MonitorCredentials) String() string                { return "MonitorCredentials([redacted])" }
func (c CompanyCredentials) Format(f fmt.State, v rune)    { fmt.Fprint(f, c.String()) }
func (c ResellerCredentials) Format(f fmt.State, v rune)   { fmt.Fprint(f, c.String()) }
func (c IssuerCredentials) Format(f fmt.State, v rune)     { fmt.Fprint(f, c.String()) }
func (c MonitorCredentials) Format(f fmt.State, v rune)    { fmt.Fprint(f, c.String()) }
func (c CompanyCredentials) MarshalJSON() ([]byte, error)  { return []byte(`"[redacted]"`), nil }
func (c ResellerCredentials) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (c IssuerCredentials) MarshalJSON() ([]byte, error)   { return []byte(`"[redacted]"`), nil }
func (c MonitorCredentials) MarshalJSON() ([]byte, error)  { return []byte(`"[redacted]"`), nil }

type FileAuth struct{ mode, token string }

func CompanyFileAuth() FileAuth                 { return FileAuth{mode: "company"} }
func IssuerFileAuth() FileAuth                  { return FileAuth{mode: "issuer"} }
func DocumentTokenAuth(token string) FileAuth   { return FileAuth{mode: "document_token", token: token} }
func PublicReportAuth() FileAuth                { return FileAuth{mode: "capability"} }
func AnonymousFileAuth() FileAuth               { return FileAuth{mode: "anonymous"} }
func (f FileAuth) String() string               { return "FileAuth([redacted])" }
func (f FileAuth) Format(s fmt.State, v rune)   { fmt.Fprint(s, f.String()) }
func (f FileAuth) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

type Result[T any] struct {
	Value  T
	Status int
}
type FileResponse struct {
	Status    int
	MediaType string
	Data      []byte
	Location  *ProtectedLocation
	Protected bool
}

func (f FileResponse) String() string               { return "FileResponse([private contents])" }
func (f FileResponse) Format(s fmt.State, v rune)   { fmt.Fprint(s, f.String()) }
func (f FileResponse) MarshalJSON() ([]byte, error) { return []byte(`"[private file contents]"`), nil }

type baseClient struct {
	scope   string
	t       *transport
	headers http.Header
	secrets []string
}
type NfeClient struct{ base *baseClient }
type EmitterClient struct{ base *baseClient }
type MonitorClient struct{ base *baseClient }
type ResellerClient struct{ base *baseClient }
type NfeFilesClient struct{ base *baseClient }
type EmitterFilesClient struct{ base *baseClient }

// ReportFilesClient downloads current report exports (fiscalDownloadReportV2) from one API2 origin.
type ReportFilesClient struct{ base *baseClient }

func validCredential(s string) bool {
	if strings.TrimSpace(s) == "" || len(s) > 65536 {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func legacyHeaders(a, b, c, d string) (http.Header, []string, error) {
	v := []string{a, b, c, d}
	h := http.Header{}
	names := []string{"X-Consumer-Key", "X-Consumer-Secret", "X-Access-Token", "X-Access-Token-Secret"}
	for i, s := range v {
		if !validCredential(s) {
			return nil, nil, newError("configuration")
		}
		h.Set(names[i], s)
	}
	return h, v, nil
}
func makeBase(scope string, h http.Header, secrets []string, cfg Config, def string) (*baseClient, error) {
	t, e := newTransport(cfg, def)
	if e != nil {
		return nil, e
	}
	return &baseClient{scope, t, h, secrets}, nil
}
func NewNfeClient(c CompanyCredentials, cfg Config) (*NfeClient, error) {
	h, s, e := legacyHeaders(c.ConsumerKey, c.ConsumerSecret, c.AccessToken, c.AccessTokenSecret)
	if e != nil {
		return nil, e
	}
	b, e := makeBase("Nfe", h, s, cfg, "https://webmania.com.br/api")
	return &NfeClient{b}, e
}
func NewResellerClient(c ResellerCredentials, cfg Config) (*ResellerClient, error) {
	h, s, e := legacyHeaders(c.ConsumerKey, c.ConsumerSecret, c.AccessToken, c.AccessTokenSecret)
	if e != nil {
		return nil, e
	}
	b, e := makeBase("Reseller", h, s, cfg, "https://webmania.com.br")
	return &ResellerClient{b}, e
}
func NewEmitterClient(c IssuerCredentials, cfg Config) (*EmitterClient, error) {
	if !validCredential(c.Token) {
		return nil, newError("configuration")
	}
	b, e := makeBase("Emitter", http.Header{"Authorization": []string{"Bearer " + c.Token}}, []string{c.Token}, cfg, "https://api.webmania.com.br")
	return &EmitterClient{b}, e
}
func NewMonitorClient(c MonitorCredentials, cfg Config) (*MonitorClient, error) {
	if !validCredential(c.Token) {
		return nil, newError("configuration")
	}
	b, e := makeBase("Monitor", http.Header{"Authorization": []string{"Bearer " + c.Token}}, []string{c.Token}, cfg, "https://api.webmania.com.br")
	return &MonitorClient{b}, e
}
func NewNfeFilesClient(c *CompanyCredentials, cfg Config) (*NfeFilesClient, error) {
	h := http.Header{}
	var s []string
	var e error
	if c != nil {
		h, s, e = legacyHeaders(c.ConsumerKey, c.ConsumerSecret, c.AccessToken, c.AccessTokenSecret)
		if e != nil {
			return nil, e
		}
	}
	b, e := makeBase("NfeFiles", h, s, cfg, "https://webmania.com.br")
	return &NfeFilesClient{b}, e
}
func NewEmitterFilesClient(c *IssuerCredentials, cfg Config) (*EmitterFilesClient, error) {
	h := http.Header{}
	var s []string
	if c != nil {
		if !validCredential(c.Token) {
			return nil, newError("configuration")
		}
		h.Set("Authorization", "Bearer "+c.Token)
		s = []string{c.Token}
	}
	b, e := makeBase("EmitterFiles", h, s, cfg, "https://api.webmania.com.br")
	return &EmitterFilesClient{b}, e
}

// NewReportFilesClient downloads current report exports. Company credentials are optional and sent
// only with CompanyFileAuth; AnonymousFileAuth uses the report URL capability alone.
func NewReportFilesClient(c *CompanyCredentials, cfg Config) (*ReportFilesClient, error) {
	h := http.Header{}
	var s []string
	var e error
	if c != nil {
		h, s, e = legacyHeaders(c.ConsumerKey, c.ConsumerSecret, c.AccessToken, c.AccessTokenSecret)
		if e != nil {
			return nil, e
		}
	}
	b, e := makeBase("ReportFiles", h, s, cfg, "https://api.webmania.com.br")
	return &ReportFilesClient{b}, e
}

// NewIssuerReportFilesClient downloads current report exports with the company-scoped API2 Bearer
// of the company that exported the report, sent only with IssuerFileAuth; AnonymousFileAuth uses
// the report URL capability alone.
func NewIssuerReportFilesClient(c IssuerCredentials, cfg Config) (*ReportFilesClient, error) {
	if !validCredential(c.Token) {
		return nil, newError("configuration")
	}
	b, e := makeBase("ReportFiles", http.Header{"Authorization": []string{"Bearer " + c.Token}}, []string{c.Token}, cfg, "https://api.webmania.com.br")
	return &ReportFilesClient{b}, e
}

type executionResult struct {
	raw           []byte
	status        int
	file          FileResponse
	decodeFailure func() error
}

func snapshot(v any, limit int64) ([]byte, any, error) {
	if !validInputText(v) {
		return nil, nil, newError("validation")
	}
	b, e := json.Marshal(v)
	if e != nil {
		return nil, nil, newError("validation")
	}
	if int64(len(b)) > limit {
		return nil, nil, newError("request_limit")
	}
	s, e := value.Decode(b, 64)
	if e != nil {
		return nil, nil, newError("validation")
	}
	return b, s, nil
}
func (c *baseClient) execute(ctx context.Context, oid string, paths map[string]any, query, body any, auth FileAuth) (executionResult, error) {
	return c.executeKnown(ctx, oid, paths, query, body, auth, nil, nil)
}
func (c *baseClient) executeKnown(ctx context.Context, oid string, paths map[string]any, query, body any, auth FileAuth, known, knownUUID []string) (executionResult, error) {
	empty := executionResult{}
	o, ok := operations[oid]
	if !ok || o.Service != c.scope {
		return empty, newError("configuration")
	}
	if o.Spec["x-sdk-generation-enabled"] == false {
		return empty, newError("capability")
	}
	if e := ctx.Err(); e != nil {
		return empty, canceledError(e)
	}
	_, qs, e := snapshot(query, c.t.requestLimit)
	if e != nil {
		return empty, e
	}
	if qs == nil {
		qs = map[string]any{}
	}
	q := object(qs)
	if q == nil {
		return empty, newError("validation")
	}
	if e = schemaCheck(o.Module, o.Query, q); e != nil {
		return empty, e
	}
	if oid == "nfeGetDocument" {
		if e = schemaCheck(o.Module, map[string]any{"$ref": "#/components/schemas/NfeLookupRequest"}, q); e != nil {
			return empty, e
		}
	}
	if oid == "monitorGetDownloadStatistics" {
		_, y := q["ano"]
		_, m := q["mes"]
		if y != m {
			return empty, newError("validation")
		}
	}
	route := o.Path
	_, ps, e := snapshot(paths, c.t.requestLimit)
	if e != nil {
		return empty, e
	}
	path := object(ps)
	for _, p := range o.Parameters {
		if p.In != "path" {
			continue
		}
		v, ok := path[p.Name]
		if !ok {
			return empty, newError("validation")
		}
		if e = schemaCheck(o.Module, p.Schema, v); e != nil {
			return empty, e
		}
		s, ok := scalarText(v)
		if !ok || s == "." || s == ".." || strings.ContainsAny(s, "/\\%?#\x00\r\n") {
			return empty, newError("validation")
		}
		route = strings.ReplaceAll(route, "{"+p.Name+"}", url.PathEscape(s))
	}
	bs := at(o.Spec, "requestBody", "content", "application/json", "schema")
	var raw []byte
	var frozen any
	if bs != nil {
		raw, frozen, e = snapshot(body, c.t.requestLimit)
		if e != nil {
			return empty, e
		}
		if e = schemaCheck(o.Module, bs, frozen); e != nil {
			return empty, e
		}
	} else if body != nil {
		return empty, newError("validation")
	}
	h := c.headers.Clone()
	secrets := append([]string{}, c.secrets...)
	observed := append([]string{}, known...)
	capAccess := false
	if strings.HasSuffix(c.scope, "Files") {
		h = http.Header{}
		security, _ := o.Spec["security"].([]any)
		allows := func(k string) bool {
			for _, p := range security {
				if _, ok := object(p)[k]; ok {
					return true
				}
			}
			return false
		}
		modeOK := false
		switch auth.mode {
		case "company":
			modeOK = (c.scope == "NfeFiles" || c.scope == "ReportFiles") && allows("ConsumerKey") && c.headers.Get("X-Consumer-Key") != ""
			if modeOK {
				h = c.headers.Clone()
			}
		case "issuer":
			modeOK = (c.scope == "EmitterFiles" && allows("FiscalFileCompanyBearer") || c.scope == "ReportFiles" && allows("ReportCompanyBearer")) && c.headers.Get("Authorization") != ""
			if modeOK {
				h = c.headers.Clone()
			}
		case "document_token":
			modeOK = (allows("DocumentToken") || allows("FiscalDocumentLinkToken")) && validCredential(auth.token)
			if modeOK {
				q["token"] = auth.token
				secrets = append(secrets, auth.token)
				capAccess = true
			}
		case "capability":
			modeOK = len(security) == 0
			capAccess = true
		case "anonymous":
			for _, p := range security {
				if len(object(p)) == 0 {
					modeOK = true
				}
			}
			capAccess = true
		}
		if !modeOK {
			return empty, newError("configuration")
		}
	} else if auth.mode != "" {
		return empty, newError("configuration")
	}
	h.Set("User-Agent", "Webmania-Go/"+Version)
	h.Set("Accept", "application/json")
	h.Set("Accept-Encoding", "identity")
	if raw != nil {
		h.Set("Content-Type", "application/json")
	}
	responses := object(o.Spec["responses"])
	allowed := map[string]bool{}
	binary := false
	for status, r := range responses {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		for mime, cs := range object(at(r, "content")) {
			if at(cs, "schema", "format") == "binary" {
				allowed[mime] = true
				binary = true
			}
		}
	}
	if binary {
		mimes := []string{"application/json"}
		for mime := range allowed {
			mimes = append(mimes, mime)
		}
		sort.Strings(mimes)
		h.Set("Accept", strings.Join(mimes, ", "))
	}
	values := url.Values{}
	for k, v := range q {
		s, ok := scalarText(v)
		if !ok {
			return empty, newError("validation")
		}
		values.Set(k, s)
	}
	target := strings.TrimRight(c.t.base.Path, "/") + route
	if len(values) > 0 {
		target += "?" + values.Encode()
	}
	diagSnapshot := map[string]any{"body": frozen, "query": q, "path": path}
	uuids := append([]string{}, knownUUID...)
	for _, v := range path {
		if s, ok := v.(string); ok && isUUID(s) {
			uuids = append(uuids, s)
		}
	}
	effect := at(o.Spec, "x-operation-effects", "business_mutation")
	mutation := effect != false
	if effect == nil {
		mutation = o.Verb != "GET" && o.Verb != "HEAD"
	}
	if effect == "only_when_relatorio_is_present" {
		_, mutation = object(frozen)["relatorio"]
	}
	if effect == "provider_status_refresh_when_atualizar_status_is_1" {
		mutation = fmt.Sprint(q["atualizar_status"]) == "1"
	}
	// The pinned report x-retry-policy consumes bounded export quota even on GET.
	// Keep this local effect overlay independent of absent generated effect metadata.
	if oid == "fiscalDownloadReport" {
		mutation = true
	}
	wr, networkErr := c.t.send(ctx, o.Verb, target, raw, h)
	// Observe every received Location before diagnostics or recovery, including invalid metadata.
	for _, loc := range wr.locations {
		s, u := observeLocation(loc)
		observed = append(observed, s...)
		knownUUID = append(knownUUID, u...)
		uuids = append(uuids, u...)
	}
	red := newRedactor(secrets, diagSnapshot, uuids)
	red.observe(observed)
	ref := recovery(frozen, path, secrets, knownUUID, capAccess, observed)
	if wr.incompleteHeaders {
		ref = nil
	}
	failure := func(kind string, v any) *Error {
		er := newError(kind)
		er.Operation = oid
		er.Status = wr.status
		// Unseen/rejected metadata can contain a capability equal to an earlier
		// correlation value. Export no untrusted correlation on an observation gap.
		if wr.headers != nil && !wr.incompleteHeaders {
			for _, k := range []string{"X-Correlation-Id", "X-Request-Id", "Request-Id"} {
				if s := wr.headers.Get(k); s != "" {
					er.CorrelationID = red.text(s)
					break
				}
			}
		}
		if v != nil {
			er.Diagnostics = red.clean(v)
		}
		if mutation && wr.sent && (networkErr != nil || wr.status >= 500 || kind == "protocol" || kind == "response_schema" || kind == "business" || kind == "protected_file" || kind == "redirect") {
			er.Outcome = "unknown"
			er.Reference = ref
		}
		return er
	}
	if networkErr != nil {
		er := failure(networkErr.(*Error).Kind, nil)
		er.cancellation = networkErr.(*Error).cancellation
		return empty, er
	}
	if wr.status >= 300 && wr.status < 400 {
		if len(wr.headers.Values("Location")) != 1 {
			return empty, failure("protocol", nil)
		}
		loc, e := protectedLocation(wr.headers.Get("Location"))
		if e != nil {
			return empty, failure("protocol", nil)
		}
		if binary && wr.status == 302 && at(responses["302"], "headers", "Location", "required") == true {
			return executionResult{file: FileResponse{Status: wr.status, Location: loc}}, nil
		}
		er := failure("redirect", nil)
		er.Location = loc
		return empty, er
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(wr.headers.Get("Content-Type"), ";")[0]))
	var response any
	var decodeErr error
	if mime == "application/json" || strings.HasSuffix(mime, "+json") {
		response, decodeErr = value.Decode(wr.raw, 64)
	}
	if wr.status < 200 || wr.status >= 300 {
		return empty, failure("http", response)
	}
	if m, ok := response.(map[string]any); ok {
		if _, exists := m["error"]; exists {
			return empty, failure("business", response)
		}
	}
	actualResponse, declared := responseForStatus(responses, wr.status)
	if !declared {
		return empty, failure("protocol", response)
	}
	if binary {
		if response != nil || decodeErr != nil {
			return empty, failure("business", response)
		}
		actualAllowed := map[string]bool{}
		for media, content := range object(at(actualResponse, "content")) {
			if at(content, "schema", "format") == "binary" {
				actualAllowed[media] = true
			}
		}
		inspect := inspectFile
		if oid == "fiscalDownloadReport" || oid == "fiscalDownloadReportV2" {
			inspect = func(status int, mime string, data []byte, allowed map[string]bool) (FileResponse, error) {
				return inspectReport(oid, status, mime, data, allowed)
			}
		}
		file, e := inspect(wr.status, mime, wr.raw, actualAllowed)
		if e != nil {
			return empty, failure(e.(*Error).Kind, nil)
		}
		return executionResult{file: file, status: wr.status}, nil
	}
	if response == nil || decodeErr != nil {
		return empty, failure("protocol", nil)
	}
	rs := at(actualResponse, "content", "application/json", "schema")
	if rs == nil {
		return empty, failure("protocol", nil)
	}
	if e = schemaCheck(o.Module, rs, response); e != nil {
		return empty, failure("response_schema", response)
	}
	return executionResult{raw: wr.raw, status: wr.status, decodeFailure: func() error { return failure("response_schema", response) }}, nil
}

func (r executionResult) decodeError() error {
	if r.decodeFailure != nil {
		return r.decodeFailure()
	}
	return newError("response_schema")
}

// responseForStatus follows OpenAPI precedence: exact code, class wildcard, default.
func responseForStatus(responses map[string]any, status int) (any, bool) {
	for _, key := range []string{fmt.Sprint(status), fmt.Sprintf("%dXX", status/100), "default"} {
		if response, ok := responses[key]; ok {
			return response, true
		}
	}
	return nil, false
}
