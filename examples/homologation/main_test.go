package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const docID, docKey = "00000000-0000-4000-8000-0000000000f6", "00000000000000000000000000000000000000000000"

// corpusRequest returns the synthetic request of one operation from the shared corpus.
func corpusRequest(t *testing.T, operation string) map[string]any {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("..", "..", "tests", "operations.json"))
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Cases []struct {
			Operation string
			Request   map[string]any
		}
	}
	if e := json.Unmarshal(raw, &corpus); e != nil {
		t.Fatal(e)
	}
	for _, c := range corpus.Cases {
		if c.Operation == operation {
			return c.Request
		}
	}
	t.Fatalf("no corpus request for %s", operation)
	return nil
}

// withAmbiente writes the request with its top-level "ambiente" as the given JSON text.
func withAmbiente(t *testing.T, dir, name string, request map[string]any, literal string) string {
	t.Helper()
	rest := map[string]any{}
	for k, v := range request {
		if k != "ambiente" {
			rest[k] = v
		}
	}
	b, _ := json.Marshal(rest)
	text := `{"ambiente":` + literal + `,` + string(b[1:])
	if literal == "" {
		text = string(b[:len(b)-1]) + `,"extensao":{"ambiente":2}}`
	}
	path := filepath.Join(dir, name)
	if e := os.WriteFile(path, []byte(text), 0o600); e != nil {
		t.Fatal(e)
	}
	return path
}

// exampleRun runs the example in this process with only the given WEBMANIA_* variables and
// returns its exit code and its JSON lines.
func exampleRun(t *testing.T, env map[string]string, args ...string) (int, []map[string]any) {
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
	os.Stdout, failed, pollInterval = w, 0, 0
	code := run(args)
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

func credentials(base string) map[string]string {
	return map[string]string{"WEBMANIA_CONSUMER_KEY": "example-key-value", "WEBMANIA_CONSUMER_SECRET": "example-secret-value", "WEBMANIA_ACCESS_TOKEN": "example-access-value",
		"WEBMANIA_ACCESS_TOKEN_SECRET": "example-access-secret-value", "WEBMANIA_ISSUER_TOKEN": "example-issuer-value",
		"WEBMANIA_API1_BASE_URL": base + "/api", "WEBMANIA_API2_BASE_URL": base, "WEBMANIA_LIVE_ALLOWED_HOSTS": "127.0.0.1"}
}

func isBlocked(code int, lines []map[string]any, reason string) bool {
	return code == blocked && len(lines) == 1 && lines[0]["result"] == "blocked" && lines[0]["reason"] == reason
}

// Every guard runs before credentials are read or any request is made.
func TestGuardsRunBeforeAnyRequest(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s.Close()
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.jsonl")
	cases := map[string]string{"nfe": "nfeIssueDocument", "cte": "emitterIssueCte", "nfcom": "emitterIssueNfcom"}
	good := withAmbiente(t, dir, "good.json", corpusRequest(t, "emitterIssueCte"), "2")
	check := func(label, reason string, env map[string]string, args ...string) {
		t.Helper()
		if code, lines := exampleRun(t, env, args...); !isBlocked(code, lines, reason) {
			t.Errorf("%s: want blocked %s, got %d %v", label, reason, code, lines)
		}
	}
	env := credentials(s.URL)
	env["WEBMANIA_LIVE_LEDGER"] = ledger
	check("no --execute", "execute", env, "--model", "cte", "--payload", good)
	check("unknown model", "model", env, "--model", "nfx", "--payload", good, "--execute")
	for _, hosts := range []string{"", " , "} {
		env := credentials(s.URL)
		env["WEBMANIA_LIVE_ALLOWED_HOSTS"] = hosts
		check("allow list "+hosts, "allowed_hosts", env, "--model", "cte", "--payload", good, "--execute")
	}
	env = credentials(s.URL)
	env["WEBMANIA_LIVE_ALLOWED_HOSTS"] = "example.invalid"
	check("foreign origin", "origin_not_allowed", env, "--model", "cte", "--payload", good, "--execute")
	for model, operation := range cases {
		request := corpusRequest(t, operation)
		// 2.0 and 2e0 equal 2 but would be sent as written; text, production and nested only are refused too.
		for _, literal := range []string{"2.0", "2e0", "20e-1", `"2"`, "1", "null", ""} {
			path := withAmbiente(t, dir, model+".json", request, literal)
			env := credentials(s.URL)
			env["WEBMANIA_LIVE_LEDGER"] = filepath.Join(dir, "unused.jsonl")
			check(model+" ambiente "+literal, "payload", env, "--model", model, "--payload", path, "--execute")
		}
		path := withAmbiente(t, dir, model+".json", request, "2")
		for label, value := range map[string]string{"missing": "", "unwritable": dir} {
			env := credentials(s.URL)
			env["WEBMANIA_LIVE_LEDGER"] = value
			check(model+" ledger "+label, "ledger", env, "--model", model, "--payload", path, "--execute")
		}
		env := map[string]string{"WEBMANIA_API1_BASE_URL": s.URL + "/api", "WEBMANIA_API2_BASE_URL": s.URL, "WEBMANIA_LIVE_ALLOWED_HOSTS": "127.0.0.1", "WEBMANIA_LIVE_LEDGER": ledger}
		check(model+" credentials", "credentials", env, "--model", model, "--payload", path, "--execute")
	}
	if _, e := os.Stat(filepath.Join(dir, "unused.jsonl")); e == nil {
		t.Error("the ledger was opened for a refused payload")
	}
	if b, e := os.ReadFile(ledger); e != nil || len(b) != 0 {
		t.Errorf("the ledger must be created empty before credentials are read: %v %q", e, b)
	}
	cte := withAmbiente(t, dir, "cte-os.json", corpusRequest(t, "emitterIssueCte"), "2")
	env = credentials(s.URL)
	env["WEBMANIA_LIVE_LEDGER"] = ledger
	check("cte_os without modelo 67", "payload", env, "--model", "cte_os", "--payload", cte, "--execute")
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("%d requests before the guards passed", requests)
	}
}

type fixture struct {
	mu                                  sync.Mutex
	status                              string // what the consult shows
	afterCancel                         string // what the consult shows once the cancellation was accepted
	settled                             string // when set, what the consult shows from the second consult after the cancellation on
	dropIssue, dropCancel, withoutKey   bool
	modelo                              string // the API 2.0 NFCom answers carry their model
	onIssue                             func() // runs when the issue request arrives, before the answer
	issues, cancels, polled, postCancel int
	cancelBodies                        []map[string]any
}

// drop closes the connection of a request that was received, before any response.
func drop(w http.ResponseWriter) {
	c, _, _ := w.(http.Hijacker).Hijack()
	c.Close()
}

func (fx *fixture) serve(issuePath, consultPath, cancelPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		reply := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case issuePath:
			fx.issues++
			if fx.onIssue != nil {
				fx.onIssue()
			}
			if fx.dropIssue {
				drop(w)
				return
			}
			issued := map[string]any{"uuid": docID, "status": "processando"}
			if fx.modelo != "" {
				issued["modelo"] = fx.modelo
			}
			if !fx.withoutKey {
				issued["chave"] = docKey
			}
			reply(issued)
		case consultPath:
			fx.polled++
			if fx.cancels > 0 {
				if fx.postCancel++; fx.postCancel > 1 && fx.settled != "" {
					fx.status = fx.settled
				}
			}
			consulted := map[string]any{"uuid": docID, "status": fx.status}
			if fx.modelo != "" {
				consulted["modelo"] = fx.modelo
			}
			reply(consulted)
		case cancelPath:
			fx.cancels++
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			fx.cancelBodies = append(fx.cancelBodies, b)
			if fx.dropCancel {
				drop(w)
				return
			}
			fx.status = fx.afterCancel
			reply(map[string]any{"status": "processando_cancelamento"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func ledgerEntries(t *testing.T, path string) []map[string]string {
	t.Helper()
	b, _ := os.ReadFile(path)
	var out []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var entry map[string]string
		if line != "" && json.Unmarshal([]byte(line), &entry) == nil {
			out = append(out, entry)
		}
	}
	return out
}

func lineWith(lines []map[string]any, step string, ok bool) map[string]any {
	for _, l := range lines {
		if l["step"] == step && l["ok"] == ok {
			return l
		}
	}
	return nil
}

// Issue once, consult, cancel the approved document and confirm "cancelado"; never resend.
func TestCycleCancelsAndConfirms(t *testing.T) {
	routes := map[string][3]string{
		"nfe":   {"/api/1/nfe/emissao/", "/api/1/nfe/consulta/", "/api/1/nfe/cancelar/"},
		"cte":   {"/2/cte/emissao", "/2/cte/consulta/" + docID, "/2/cte/cancelar"},
		"nfcom": {"/2/nfcom/emissao", "/2/nfcom/consulta/" + docID, "/2/nfcom/cancelar"},
	}
	operations := map[string][3]string{
		"nfe":   {"nfeIssueDocument", "nfeGetDocument", "nfeCancelDocument"},
		"cte":   {"emitterIssueCte", "emitterGetCte", "emitterCancelCte"},
		"nfcom": {"emitterIssueNfcom", "emitterGetNfcom", "emitterCancelNfcom"},
	}
	for model, route := range routes {
		ops := operations[model]
		for _, scenario := range []string{"cancelled", "cancelling-then-cancelled", "not-cancelled", "unknown-cancel", "rejected", "stuck", "no-access-key"} {
			if scenario == "no-access-key" && model != "nfcom" {
				continue
			}
			t.Run(model+"/"+scenario, func(t *testing.T) {
				fx := &fixture{status: "aprovado", afterCancel: "cancelado"}
				if model == "nfcom" {
					fx.modelo = "nfcom"
				}
				switch scenario {
				case "cancelling-then-cancelled":
					fx.afterCancel, fx.settled = "processando_cancelamento", "cancelado"
				case "not-cancelled":
					fx.afterCancel = "processando_cancelamento"
				case "unknown-cancel":
					fx.dropCancel = true
				case "rejected":
					fx.status = "reprovado"
				case "stuck":
					// Still processing at every consult, with the bound lowered for the test.
					fx.status = "processando"
					defer func(n int) { pollAttempts = n }(pollAttempts)
					pollAttempts = 2
				case "no-access-key":
					fx.withoutKey = true
				}
				s := httptest.NewServer(fx.serve(route[0], route[1], route[2]))
				defer s.Close()
				dir := t.TempDir()
				payload := withAmbiente(t, dir, "payload.json", corpusRequest(t, ops[0]), "2")
				ledger := filepath.Join(dir, "ledger.jsonl")
				env := credentials(s.URL)
				env["WEBMANIA_LIVE_LEDGER"] = ledger
				code, lines := exampleRun(t, env, "--model", model, "--payload", payload, "--execute")
				out, _ := json.Marshal(lines)
				for _, private := range []string{docID, docKey, s.URL, "example-key-value", "example-issuer-value"} {
					if strings.Contains(string(out), private) {
						t.Fatalf("output leaked a private value: %s", out)
					}
				}
				fx.mu.Lock()
				defer fx.mu.Unlock()
				if fx.issues != 1 {
					t.Fatalf("the document must be issued exactly once: %d", fx.issues)
				}
				entries := ledgerEntries(t, ledger)
				if len(entries) == 0 || entries[0]["identifier"] != docID || entries[0]["operation"] != ops[0] {
					t.Fatalf("the issued document is not in the ledger: %v", entries)
				}
				last := entries[len(entries)-1]
				switch scenario {
				case "cancelled":
					if code != 0 || fx.cancels != 1 || last["status"] != "cancelado" || last["operation"] != ops[2] {
						t.Fatalf("cleanup not confirmed: %d %d %v %s", code, fx.cancels, entries, out)
					}
					if model == "nfcom" && fx.cancelBodies[0]["chave"] != docKey {
						t.Errorf("the NFCom cancellation must use the access key: %v", fx.cancelBodies[0])
					}
				case "cancelling-then-cancelled":
					// The consult after the cancellation waits through processando_cancelamento.
					if code != 0 || fx.cancels != 1 || fx.postCancel != 2 || last["status"] != "cancelado" || last["operation"] != ops[2] {
						t.Fatalf("a cancellation still processing must be consulted until cancelado: %d %d %d %v %s", code, fx.cancels, fx.postCancel, entries, out)
					}
				case "not-cancelled":
					verdict := lineWith(lines, model+".consult_cancelled", false)
					if code != 1 || fx.cancels != 1 || verdict == nil || verdict["kind"] != "not_cancelled" || verdict["operation"] != ops[1] {
						t.Fatalf("an accepted cancellation without cancelado must fail: %d %d %s", code, fx.cancels, out)
					}
					if last["status"] != "processando_cancelamento" {
						t.Errorf("ledger must keep the last observed status: %v", last)
					}
				case "unknown-cancel":
					want := map[string]string{"model": model, "identifier": docID, "status": "unknown", "operation": ops[2]}
					if code != 1 || fx.cancels != 1 || last["status"] != want["status"] || last["operation"] != want["operation"] || last["model"] != model {
						t.Fatalf("an unknown cancellation must be ledgered as unknown and never repeated: %d %d %v", code, fx.cancels, entries)
					}
					if lineWith(lines, model+".consult_cancelled", true) != nil || lineWith(lines, model+".consult_cancelled", false) != nil {
						t.Errorf("no consult may follow an unknown cancellation: %s", out)
					}
				case "rejected":
					verdict := lineWith(lines, model+".consult", false)
					if code != 1 || fx.cancels != 0 || verdict == nil || verdict["kind"] != "not_approved" || last["status"] != "reprovado" {
						t.Fatalf("a rejected document is reported and never cancelled: %d %d %v %s", code, fx.cancels, entries, out)
					}
				case "stuck":
					verdict := lineWith(lines, model+".consult", false)
					if code != 1 || fx.cancels != 0 || fx.polled != 2 || verdict == nil || verdict["kind"] != "not_approved" || last["status"] != "unknown" || last["operation"] != ops[1] {
						t.Fatalf("a document without a terminal status is ledgered unknown and never cancelled: %d %d %d %v %s", code, fx.cancels, fx.polled, entries, out)
					}
				case "no-access-key":
					verdict := lineWith(lines, model+".cancel", false)
					if code != 1 || fx.cancels != 0 || verdict == nil || verdict["kind"] != "no_access_key" || last["status"] != "aprovado" {
						t.Fatalf("an NFCom without access key cannot be cancelled and must stay in the ledger: %d %d %v %s", code, fx.cancels, entries, out)
					}
				}
			})
		}
	}
}

// An issue whose outcome is unknown is ledgered as unknown by the payload ID and never resent: nothing else is sent.
func TestUnknownIssueIsLedgeredAndNeverResent(t *testing.T) {
	routes := map[string][3]string{
		"nfe": {"/api/1/nfe/emissao/", "/api/1/nfe/consulta/", "/api/1/nfe/cancelar/"},
		"cte": {"/2/cte/emissao", "/2/cte/consulta/" + docID, "/2/cte/cancelar"},
	}
	for model, route := range routes {
		t.Run(model, func(t *testing.T) {
			fx := &fixture{status: "aprovado", afterCancel: "cancelado", dropIssue: true}
			s := httptest.NewServer(fx.serve(route[0], route[1], route[2]))
			defer s.Close()
			dir := t.TempDir()
			operation := map[string]string{"nfe": "nfeIssueDocument", "cte": "emitterIssueCte"}[model]
			request := corpusRequest(t, operation)
			payload := withAmbiente(t, dir, "payload.json", request, "2")
			ledger := filepath.Join(dir, "ledger.jsonl")
			env := credentials(s.URL)
			env["WEBMANIA_LIVE_LEDGER"] = ledger
			code, lines := exampleRun(t, env, "--model", model, "--payload", payload, "--execute")
			out, _ := json.Marshal(lines)
			fx.mu.Lock()
			defer fx.mu.Unlock()
			if code != 1 || fx.issues != 1 || fx.polled != 0 || fx.cancels != 0 {
				t.Fatalf("an unknown issue must stop the run without a resend, consult or cancellation: %d %d %d %d %s", code, fx.issues, fx.polled, fx.cancels, out)
			}
			id, _ := request["ID"].(string)
			if id == "" {
				id = "unidentified"
			} else if strings.Contains(string(out), id) {
				t.Fatalf("output leaked the payload ID: %s", out)
			}
			want := map[string]string{"model": model, "identifier": id, "status": "unknown", "operation": operation}
			if entries := ledgerEntries(t, ledger); len(entries) != 1 || !reflect.DeepEqual(entries[0], want) {
				t.Fatalf("the unknown issue must be ledgered as %v: %v", want, entries)
			}
			if issue := lineWith(lines, model+".issue", false); len(lines) != 1 || issue == nil || issue["outcome"] != "unknown" {
				t.Fatalf("the issue must be the only step, reported as unknown: %s", out)
			}
		})
	}
}

// A ledger write that fails mid-run is reported as a masked ledger step and fails the run; the approved document is
// still cancelled and confirmed.
func TestLedgerWriteFailingMidRunFailsTheRun(t *testing.T) {
	fx := &fixture{status: "aprovado", afterCancel: "cancelado"}
	s := httptest.NewServer(fx.serve("/api/1/nfe/emissao/", "/api/1/nfe/consulta/", "/api/1/nfe/cancelar/"))
	defer s.Close()
	dir := t.TempDir()
	payload := withAmbiente(t, dir, "payload.json", corpusRequest(t, "nfeIssueDocument"), "2")
	ledger := filepath.Join(dir, "ledger.jsonl")
	// The ledger is writable up front and becomes a directory while the issue request is in flight.
	fx.onIssue = func() {
		if e := errors.Join(os.Remove(ledger), os.Mkdir(ledger, 0o700)); e != nil {
			t.Error(e)
		}
	}
	env := credentials(s.URL)
	env["WEBMANIA_LIVE_LEDGER"] = ledger
	code, lines := exampleRun(t, env, "--model", "nfe", "--payload", payload, "--execute")
	out, _ := json.Marshal(lines)
	fx.mu.Lock()
	defer fx.mu.Unlock()
	failure := lineWith(lines, "ledger", false)
	if code != 1 || failure == nil || failure["kind"] != "ledger" || failure["operation"] != "nfeIssueDocument" || failure["ref"] != ref(docID) {
		t.Fatalf("a ledger that cannot be written must fail the run with a masked ledger step: %d %s", code, out)
	}
	if fx.issues != 1 || fx.cancels != 1 || lineWith(lines, "nfe.consult_cancelled", true) == nil {
		t.Fatalf("the approved document must still be cancelled once and confirmed: %d %d %s", fx.issues, fx.cancels, out)
	}
	if strings.Contains(string(out), docID) {
		t.Fatalf("output leaked the identifier: %s", out)
	}
}
