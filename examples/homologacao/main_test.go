package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// exampleRun runs the example in this process with only the given WEBMANIA_* variables and
// returns its exit code, its JSON lines and its ledger entries.
func exampleRun(t *testing.T, env map[string]string) (int, []map[string]any) {
	t.Helper()
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "WEBMANIA_") {
			t.Setenv(name, "")
		}
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	captured := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); captured <- b }()
	stdout := os.Stdout
	os.Stdout, failed = w, 0
	code := run()
	os.Stdout = stdout
	w.Close()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(<-captured)), "\n") {
		var item map[string]any
		if json.Unmarshal([]byte(line), &item) != nil {
			t.Fatalf("bad line: %q", line)
		}
		lines = append(lines, item)
	}
	return code, lines
}

func syntheticPDF() string {
	out := "%PDF-1.4\n"
	var offsets []int
	for i, obj := range []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", "<< /Length 0 >>\nstream\n\nendstream"} {
		offsets = append(offsets, len(out))
		out += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := len(out)
	out += "xref\n0 5\n0000000000 65535 f \n"
	for _, n := range offsets {
		out += fmt.Sprintf("%010d 00000 n \n", n)
	}
	return out + fmt.Sprintf("trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
}

func secrets() map[string]string {
	return map[string]string{"WEBMANIA_CONSUMER_KEY": "example-key-value", "WEBMANIA_CONSUMER_SECRET": "example-secret-value", "WEBMANIA_ACCESS_TOKEN": "example-access-value", "WEBMANIA_ACCESS_TOKEN_SECRET": "example-access-secret-value", "WEBMANIA_ISSUER_TOKEN": "example-issuer-value", "WEBMANIA_LIVE": "1"}
}

// Without an allow list no origin is trusted: blocked before credentials or any request.
func TestAllowListIsRequired(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s.Close()
	for _, hosts := range []string{"", " , "} {
		env := secrets()
		env["WEBMANIA_API1_BASE_URL"], env["WEBMANIA_FILES_BASE_URL"], env["WEBMANIA_API2_BASE_URL"] = s.URL+"/api", s.URL, s.URL
		env["WEBMANIA_LIVE_ALLOWED_HOSTS"] = hosts
		code, lines := exampleRun(t, env)
		if code != blocked || len(lines) != 1 || lines[0]["result"] != "blocked" || lines[0]["reason"] != "allowed_hosts" {
			t.Fatalf("allow list %q not required: %d %v", hosts, code, lines)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("%d requests without an allow list", requests)
	}
}

// An accepted cancellation whose consult never shows "cancelado" fails the run.
func TestFinalConsultMustShowCancelled(t *testing.T) {
	pollInterval = 0
	raw, e := os.ReadFile(filepath.Join("..", "..", "tests", "operations.json"))
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Cases []struct {
			Operation string
			Request   map[string]any
			Response  any
		}
	}
	if e := json.Unmarshal(raw, &corpus); e != nil {
		t.Fatal(e)
	}
	requests, responses := map[string]map[string]any{}, map[string]map[string]any{}
	for _, c := range corpus.Cases {
		requests[c.Operation] = c.Request
		if m, ok := c.Response.(map[string]any); ok {
			responses[c.Operation] = m
		}
	}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	const nfeID, nfseID = "00000000-0000-4000-8000-0000000000d4", "00000000-0000-4000-8000-0000000000e5"
	pdf := syntheticPDF()
	var mu sync.Mutex
	var cancels []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reply := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
		file := func(b, mime string) { w.Header().Set("Content-Type", mime); w.Write([]byte(b)) }
		switch p := r.URL.Path; {
		case p == "/api/1/nfe/sefaz/":
			reply(map[string]any{"status": "online"})
		case p == "/api/1/nfe/certificado/":
			reply(map[string]any{"expiration": 30})
		case p == "/api/1/nfe/classe-imposto/":
			reply([]any{})
		case p == "/2/nfse/status":
			reply(responses["emitterGetNfseProviderStatus"])
		case p == "/api/1/nfe/emissao/":
			reply(map[string]any{"uuid": nfeID, "status": "processando"})
		case p == "/api/1/nfe/consulta/":
			reply(map[string]any{"uuid": nfeID, "status": "aprovado"})
		case p == "/api/1/nfe/cancelar/", p == "/2/nfse/cancelar":
			// Accepted, but the document never shows "cancelado".
			cancels = append(cancels, p)
			if p == "/2/nfse/cancelar" {
				reply(with(responses["emitterCancelNfse"], map[string]any{"uuid": nfseID, "status": "cancelado"}))
			} else {
				reply(map[string]any{"status": "cancelado"})
			}
		case strings.HasPrefix(p, "/danfe/"), p == "/nfse/"+nfseID:
			file(pdf, "application/pdf")
		case strings.HasPrefix(p, "/xmlnfe/"), p == "/xmlnfse/"+nfseID:
			file(`<?xml version="1.0"?><synthetic/>`, "text/xml")
		case p == "/2/nfse/emissao":
			reply(with(responses["emitterIssueNfse"], map[string]any{"uuid": nfseID, "status": "processando"}))
		case p == "/2/nfse/consulta/"+nfseID:
			reply(with(responses["emitterGetNfse"], map[string]any{"uuid": nfseID, "status": "aprovado", "pdf_nfse": "ok"}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	dir := t.TempDir()
	for name, op := range map[string]string{"nfe.json": "nfeIssueDocument", "nfse.json": "emitterIssueNfse"} {
		b, _ := json.Marshal(with(requests[op], map[string]any{"ambiente": 2}))
		if e := os.WriteFile(filepath.Join(dir, name), b, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	env := secrets()
	env["WEBMANIA_LIVE_SCOPE"] = "all"
	env["WEBMANIA_API1_BASE_URL"], env["WEBMANIA_FILES_BASE_URL"], env["WEBMANIA_API2_BASE_URL"] = s.URL+"/api", s.URL, s.URL
	env["WEBMANIA_LIVE_ALLOWED_HOSTS"] = "127.0.0.1"
	env["WEBMANIA_LIVE_NFE_PAYLOAD"], env["WEBMANIA_LIVE_NFSE_PAYLOAD"] = filepath.Join(dir, "nfe.json"), filepath.Join(dir, "nfse.json")
	env["WEBMANIA_LIVE_LEDGER"] = filepath.Join(dir, "ledger.jsonl")
	code, lines := exampleRun(t, env)
	if code != 1 {
		t.Fatalf("a document that never shows cancelado must fail the run: %d %v", code, lines)
	}
	for model, operation := range map[string]string{"nfe": "nfeGetDocument", "nfse": "emitterGetNfse"} {
		var verdict []map[string]any
		for _, l := range lines {
			if l["step"] == model+".consult_cancelled" && l["ok"] == false {
				verdict = append(verdict, l)
			}
			if l["step"] == "nfe.cancellation_xml" {
				t.Errorf("cancellation XML requested without cancelado")
			}
		}
		if len(verdict) != 1 || verdict[0]["operation"] != operation || verdict[0]["kind"] != "not_cancelled" {
			t.Errorf("%s: final consult not reported as not_cancelled: %v", model, verdict)
		}
	}
	mu.Lock()
	sort.Strings(cancels)
	if strings.Join(cancels, " ") != "/2/nfse/cancelar /api/1/nfe/cancelar/" {
		t.Errorf("each cancellation must be sent once: %v", cancels)
	}
	mu.Unlock()
	entries, _ := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	for _, want := range []string{`"identifier":"` + nfeID + `","model":"nfe","operation":"nfeCancelDocument","status":"aprovado"`, `"identifier":"` + nfseID + `","model":"nfse","operation":"emitterCancelNfse","status":"aprovado"`} {
		if !strings.Contains(string(entries), want) {
			t.Errorf("ledger misses %s: %s", want, entries)
		}
	}
}

// corpus returns the synthetic requests and object responses of tests/operations.json by operation.
func corpus(t *testing.T) (map[string]map[string]any, map[string]map[string]any) {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("..", "..", "tests", "operations.json"))
	if e != nil {
		t.Fatal(e)
	}
	var c struct {
		Cases []struct {
			Operation string
			Request   map[string]any
			Response  any
		}
	}
	if e := json.Unmarshal(raw, &c); e != nil {
		t.Fatal(e)
	}
	requests, responses := map[string]map[string]any{}, map[string]map[string]any{}
	for _, item := range c.Cases {
		requests[item.Operation] = item.Request
		if m, ok := item.Response.(map[string]any); ok {
			responses[item.Operation] = m
		}
	}
	return requests, responses
}

// The consults after the issue decide the cleanup: a document approved by a later consult is
// cancelled and confirmed; one that ends refused, or without a terminal status within the bound,
// fails the run as not_approved and is never cancelled, and its issue is never resent. Without a
// terminal status the ledger records "unknown" for reconciliation by consult.
func TestDocumentNotApprovedFailsTheRun(t *testing.T) {
	interval, attempts := pollInterval, pollAttempts
	defer func() { pollInterval, pollAttempts = interval, attempts }()
	pollInterval, pollAttempts = 0, 3
	requests, responses := corpus(t)
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	const docID = "00000000-0000-4000-8000-0000000000f6"
	pdf := syntheticPDF()
	// issue, consult and cancel path, then the issue, consult and cancel operations.
	flows := map[string][6]string{
		"nfe":  {"/api/1/nfe/emissao/", "/api/1/nfe/consulta/", "/api/1/nfe/cancelar/", "nfeIssueDocument", "nfeGetDocument", "nfeCancelDocument"},
		"nfse": {"/2/nfse/emissao", "/2/nfse/consulta/" + docID, "/2/nfse/cancelar", "emitterIssueNfse", "emitterGetNfse", "emitterCancelNfse"},
	}
	// What the consults before any cancellation show, in order; the last one repeats.
	scenarios := map[string][]string{"approved-later": {"processando", "aprovado"}, "refused": {"reprovado"}, "stuck": {"processando"}}
	for model, flow := range flows {
		for scenario, statuses := range scenarios {
			t.Run(model+"/"+scenario, func(t *testing.T) {
				var mu sync.Mutex
				issues, consults, cancels := 0, 0, 0
				document := func(status string) map[string]any {
					if model == "nfse" {
						return with(responses["emitterGetNfse"], map[string]any{"uuid": docID, "status": status, "pdf_nfse": "ok"})
					}
					return map[string]any{"uuid": docID, "status": status}
				}
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					reply := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
					file := func(b, mime string) { w.Header().Set("Content-Type", mime); w.Write([]byte(b)) }
					switch p := r.URL.Path; {
					case p == "/api/1/nfe/sefaz/":
						reply(map[string]any{"status": "online"})
					case p == "/api/1/nfe/certificado/":
						reply(map[string]any{"expiration": 30})
					case p == "/api/1/nfe/classe-imposto/":
						reply([]any{})
					case p == "/2/nfse/status":
						reply(responses["emitterGetNfseProviderStatus"])
					case p == flow[0]:
						issues++
						if model == "nfse" {
							reply(with(responses["emitterIssueNfse"], map[string]any{"uuid": docID, "status": "processando"}))
						} else {
							reply(map[string]any{"uuid": docID, "status": "processando"})
						}
					case p == flow[1] && r.Method == http.MethodGet:
						if cancels > 0 {
							reply(document("cancelado"))
							return
						}
						reply(document(statuses[min(consults, len(statuses)-1)]))
						consults++
					case p == "/api/1/nfe/consulta/":
						reply(map[string]any{"uuid": docID, "status": "aprovado"})
					case p == flow[2]:
						cancels++
						if model == "nfse" {
							reply(with(responses["emitterCancelNfse"], map[string]any{"uuid": docID, "status": "cancelado"}))
						} else {
							reply(map[string]any{"status": "cancelado"})
						}
					case strings.HasPrefix(p, "/danfe/"), p == "/nfse/"+docID:
						file(pdf, "application/pdf")
					case strings.HasPrefix(p, "/xmlnfe/"):
						file(`<?xml version="1.0"?><nfeProc><NFe/></nfeProc>`, "text/xml")
					case p == "/xmlnfse/"+docID:
						file(`<?xml version="1.0"?><CompNfse/>`, "text/xml")
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer s.Close()
				dir := t.TempDir()
				b, _ := json.Marshal(with(requests[flow[3]], map[string]any{"ambiente": 2}))
				payload := filepath.Join(dir, "payload.json")
				if e := os.WriteFile(payload, b, 0o600); e != nil {
					t.Fatal(e)
				}
				env := secrets()
				env["WEBMANIA_LIVE_SCOPE"] = model
				env["WEBMANIA_API1_BASE_URL"], env["WEBMANIA_FILES_BASE_URL"], env["WEBMANIA_API2_BASE_URL"] = s.URL+"/api", s.URL, s.URL
				env["WEBMANIA_LIVE_ALLOWED_HOSTS"] = "127.0.0.1"
				env["WEBMANIA_LIVE_NFE_PAYLOAD"], env["WEBMANIA_LIVE_NFSE_PAYLOAD"] = payload, payload
				env["WEBMANIA_LIVE_LEDGER"] = filepath.Join(dir, "ledger.jsonl")
				code, lines := exampleRun(t, env)
				mu.Lock()
				defer mu.Unlock()
				var entries []map[string]string
				raw, _ := os.ReadFile(env["WEBMANIA_LIVE_LEDGER"])
				for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
					var entry map[string]string
					if json.Unmarshal([]byte(line), &entry) == nil {
						entries = append(entries, entry)
					}
				}
				if issues != 1 || len(entries) == 0 {
					t.Fatalf("the document must be issued exactly once and ledgered: %d %v", issues, entries)
				}
				last := entries[len(entries)-1]
				var verdict map[string]any
				for _, l := range lines {
					if l["step"] == model+".consult" && l["ok"] == false {
						verdict = l
					}
				}
				if scenario == "approved-later" {
					// Approved only by the second consult: cancelled once and confirmed.
					for _, l := range lines {
						if l["ok"] != true {
							t.Errorf("failed step: %v", l)
						}
					}
					if code != 0 || cancels != 1 || consults != 2 || verdict != nil || last["status"] != "cancelado" || last["operation"] != flow[5] {
						t.Fatalf("a document approved by a later consult must be cancelled and confirmed: %d %d %d %v %v", code, cancels, consults, entries, lines)
					}
					return
				}
				want := map[string]string{"model": model, "identifier": docID, "status": map[string]string{"refused": "reprovado", "stuck": "unknown"}[scenario], "operation": flow[4]}
				if code != 1 || cancels != 0 || verdict == nil || verdict["kind"] != "not_approved" || verdict["operation"] != flow[4] {
					t.Fatalf("a document not approved must fail the run without a cancellation: %d %d %v", code, cancels, lines)
				}
				if fmt.Sprint(last) != fmt.Sprint(want) {
					t.Errorf("ledger: want %v, got %v", want, last)
				}
				if consults != map[string]int{"refused": 1, "stuck": pollAttempts}[scenario] {
					t.Errorf("consults: %d", consults)
				}
			})
		}
	}
}
