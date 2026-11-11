package webmania_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The neutral homologation example against a loopback fixture: masked lines, ledger, no replay.
func TestHomologationExample(t *testing.T) {
	if _, e := exec.LookPath("go"); e != nil {
		t.Skip("go tool not available")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "homologacao")
	if out, e := exec.Command("go", "build", "-o", binary, "./examples/homologacao").CombinedOutput(); e != nil {
		t.Fatalf("example build failed: %v %s", e, out)
	}
	raw, e := os.ReadFile("tests/operations.json")
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
	cases := map[string]map[string]any{}
	responses := map[string]any{}
	for _, c := range corpus.Cases {
		cases[c.Operation], responses[c.Operation] = c.Request, c.Response
	}
	secrets := map[string]string{"WEBMANIA_CONSUMER_KEY": "example-key-value", "WEBMANIA_CONSUMER_SECRET": "example-secret-value", "WEBMANIA_ACCESS_TOKEN": "example-access-value", "WEBMANIA_ACCESS_TOKEN_SECRET": "example-access-secret-value", "WEBMANIA_ISSUER_TOKEN": "example-issuer-value"}
	clean := func(extra map[string]string) []string {
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "WEBMANIA_") {
				env = append(env, kv)
			}
		}
		for k, v := range secrets {
			env = append(env, k+"="+v)
		}
		for k, v := range extra {
			env = append(env, k+"="+v)
		}
		return env
	}
	cmd := exec.Command(binary)
	cmd.Env = clean(nil)
	out, _ := cmd.Output()
	if cmd.ProcessState.ExitCode() != 77 || strings.TrimSpace(string(out)) != `{"result":"blocked"}` {
		t.Fatalf("example not blocked without opt-in: %d %s", cmd.ProcessState.ExitCode(), out)
	}
	const nfeID, nfseID = "00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000b2"
	var mu sync.Mutex
	state := map[string]string{"nfe": "aprovado", "nfse": "aprovado"}
	issued := 0
	merge := func(base any, extra map[string]any) map[string]any {
		out := map[string]any{}
		if m, ok := base.(map[string]any); ok {
			for k, v := range m {
				out[k] = v
			}
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		file := func(b []byte, mime string) { w.Header().Set("Content-Type", mime); w.Write(b) }
		switch p := r.URL.Path; {
		case p == "/api/1/nfe/sefaz/":
			jsonResponse(w, map[string]any{"status": "online"})
		case p == "/api/1/nfe/certificado/":
			jsonResponse(w, map[string]any{"expiration": 30})
		case p == "/api/1/nfe/classe-imposto/":
			jsonResponse(w, []any{})
		case p == "/2/nfse/status":
			jsonResponse(w, responses["emitterGetNfseProviderStatus"])
		case p == "/api/1/nfe/emissao/":
			issued++
			jsonResponse(w, map[string]any{"uuid": nfeID, "status": "processando"})
		case p == "/api/1/nfe/consulta/":
			jsonResponse(w, map[string]any{"uuid": nfeID, "status": state["nfe"]})
		case p == "/api/1/nfe/cancelar/":
			state["nfe"] = "cancelado"
			jsonResponse(w, map[string]any{"status": "cancelado"})
		case strings.HasPrefix(p, "/danfe/"):
			file([]byte(pdf), "application/pdf")
		case strings.HasPrefix(p, "/xmlnfe/"):
			file([]byte(`<?xml version="1.0"?><nfeProc><NFe/></nfeProc>`), "text/xml")
		case p == "/2/nfse/emissao":
			issued++
			jsonResponse(w, merge(responses["emitterIssueNfse"], map[string]any{"uuid": nfseID, "status": "processando"}))
		case p == "/2/nfse/consulta/"+nfseID:
			jsonResponse(w, merge(responses["emitterGetNfse"], map[string]any{"uuid": nfseID, "status": state["nfse"], "pdf_nfse": "ok"}))
		case p == "/2/nfse/cancelar":
			state["nfse"] = "cancelado"
			jsonResponse(w, merge(responses["emitterCancelNfse"], map[string]any{"uuid": nfseID, "status": "cancelado"}))
		case p == "/xmlnfse/"+nfseID:
			file([]byte(`<?xml version="1.0"?><CompNfse/>`), "text/xml")
		case p == "/nfse/"+nfseID:
			file([]byte(pdf), "application/pdf")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	nfe, _ := json.Marshal(merge(cases["nfeIssueDocument"], map[string]any{"ambiente": 2}))
	nfse, _ := json.Marshal(merge(cases["emitterIssueNfse"], map[string]any{"ambiente": 2}))
	os.WriteFile(filepath.Join(dir, "nfe.json"), nfe, 0o600)
	os.WriteFile(filepath.Join(dir, "nfse.json"), nfse, 0o600)
	ledger := filepath.Join(dir, "ledger.jsonl")
	cmd = exec.Command(binary)
	cmd.Env = clean(map[string]string{"WEBMANIA_LIVE": "1", "WEBMANIA_LIVE_SCOPE": "all", "WEBMANIA_API1_BASE_URL": s.URL + "/api", "WEBMANIA_FILES_BASE_URL": s.URL, "WEBMANIA_API2_BASE_URL": s.URL, "WEBMANIA_LIVE_ALLOWED_HOSTS": "127.0.0.1", "WEBMANIA_LIVE_LEDGER": ledger, "WEBMANIA_LIVE_NFE_PAYLOAD": filepath.Join(dir, "nfe.json"), "WEBMANIA_LIVE_NFSE_PAYLOAD": filepath.Join(dir, "nfse.json")})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, _ = cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("example failed (%d): %s %s", code, out, stderr.String())
	}
	steps := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var item map[string]any
		if json.Unmarshal([]byte(line), &item) != nil || item["ok"] != true {
			t.Fatalf("bad or failed step: %s", line)
		}
		steps[item["step"].(string)] = true
	}
	for _, step := range []string{"readonly.sefaz", "readonly.nfse_provider", "nfe.issue", "nfe.cancel", "nfe.cancellation_xml", "nfse.issue", "nfse.cancel", "nfse.pdf"} {
		if !steps[step] {
			t.Errorf("missing step %s", step)
		}
	}
	for _, private := range append([]string{nfeID, nfseID, s.URL}, func() []string {
		var v []string
		for _, x := range secrets {
			v = append(v, x)
		}
		return v
	}()...) {
		if strings.Contains(string(out)+stderr.String(), private) {
			t.Errorf("example output leaked a private value")
		}
	}
	entries, _ := os.ReadFile(ledger)
	if !bytes.Contains(entries, []byte(nfeID)) || !bytes.Contains(entries, []byte(nfseID)) || bytes.Count(entries, []byte(`"status":"cancelado"`)) != 2 {
		t.Errorf("ledger incomplete: %s", entries)
	}
	if issued != 2 {
		t.Errorf("each document must be issued exactly once, got %d", issued)
	}
}

// The example ledgers an NF-e whose issue or cancellation outcome is unknown without resending it, and a
// ledger it cannot write fails the run with a masked line instead of being skipped silently.
func TestHomologationExampleLedgerFailures(t *testing.T) {
	if _, e := exec.LookPath("go"); e != nil {
		t.Skip("go tool not available")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "homologacao")
	if out, e := exec.Command("go", "build", "-o", binary, "./examples/homologacao").CombinedOutput(); e != nil {
		t.Fatalf("example build failed: %v %s", e, out)
	}
	raw, e := os.ReadFile("tests/operations.json")
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
	var document map[string]any
	var provider any
	for _, c := range corpus.Cases {
		switch c.Operation {
		case "nfeIssueDocument":
			document = map[string]any{}
			for k, v := range c.Request {
				document[k] = v
			}
			document["ambiente"] = 2
		case "emitterGetNfseProviderStatus":
			provider = c.Response
		}
	}
	payload, _ := json.Marshal(document)
	payloadPath := filepath.Join(dir, "nfe.json")
	os.WriteFile(payloadPath, payload, 0o600)
	const nfeID = "00000000-0000-4000-8000-0000000000c3"
	// mode: "drop-issue" or "drop-cancel" closes that connection after the request was received;
	// "lose-ledger" turns the ledger into a directory while the issue request is in flight.
	run := func(t *testing.T, mode string, ledger string) (int, string, int) {
		var mu sync.Mutex
		status, cancels := "aprovado", 0
		drop := func(w http.ResponseWriter) {
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
		}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			switch p := r.URL.Path; {
			case p == "/api/1/nfe/sefaz/":
				jsonResponse(w, map[string]any{"status": "online"})
			case p == "/api/1/nfe/certificado/":
				jsonResponse(w, map[string]any{"expiration": 30})
			case p == "/api/1/nfe/classe-imposto/":
				jsonResponse(w, []any{})
			case p == "/2/nfse/status":
				jsonResponse(w, provider)
			case p == "/api/1/nfe/emissao/":
				if mode == "lose-ledger" {
					if e := errors.Join(os.Remove(ledger), os.Mkdir(ledger, 0o700)); e != nil {
						t.Error(e)
					}
				}
				if mode == "drop-issue" {
					drop(w)
					return
				}
				jsonResponse(w, map[string]any{"uuid": nfeID, "status": "processando"})
			case p == "/api/1/nfe/consulta/":
				jsonResponse(w, map[string]any{"uuid": nfeID, "status": status})
			case p == "/api/1/nfe/cancelar/":
				cancels++
				if mode == "drop-cancel" {
					drop(w)
					return
				}
				status = "cancelado"
				jsonResponse(w, map[string]any{"status": "cancelado"})
			case strings.HasPrefix(p, "/danfe/"):
				w.Header().Set("Content-Type", "application/pdf")
				w.Write([]byte(pdf))
			case strings.HasPrefix(p, "/xmlnfe/"):
				w.Header().Set("Content-Type", "text/xml")
				w.Write([]byte(`<?xml version="1.0"?><nfeProc><NFe/></nfeProc>`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer s.Close()
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "WEBMANIA_") {
				env = append(env, kv)
			}
		}
		env = append(env, "WEBMANIA_CONSUMER_KEY=example-key-value", "WEBMANIA_CONSUMER_SECRET=example-secret-value", "WEBMANIA_ACCESS_TOKEN=example-access-value", "WEBMANIA_ACCESS_TOKEN_SECRET=example-access-secret-value", "WEBMANIA_ISSUER_TOKEN=example-issuer-value",
			"WEBMANIA_LIVE=1", "WEBMANIA_LIVE_SCOPE=nfe", "WEBMANIA_API1_BASE_URL="+s.URL+"/api", "WEBMANIA_FILES_BASE_URL="+s.URL, "WEBMANIA_API2_BASE_URL="+s.URL, "WEBMANIA_LIVE_ALLOWED_HOSTS=127.0.0.1", "WEBMANIA_LIVE_LEDGER="+ledger, "WEBMANIA_LIVE_NFE_PAYLOAD="+payloadPath)
		cmd := exec.Command(binary)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, _ := cmd.Output()
		text := string(out) + stderr.String()
		if strings.Contains(text, nfeID) || strings.Contains(text, s.URL) || strings.Contains(text, "example-key-value") {
			t.Error("example output leaked a private value")
		}
		mu.Lock()
		defer mu.Unlock()
		return cmd.ProcessState.ExitCode(), string(out), cancels
	}
	t.Run("unknown-cancel", func(t *testing.T) {
		ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
		code, out, cancels := run(t, "drop-cancel", ledger)
		if code != 1 || cancels != 1 {
			t.Fatalf("unknown cancellation must stop the run without replay: %d %d %s", code, cancels, out)
		}
		entries, _ := os.ReadFile(ledger)
		if !bytes.Contains(entries, []byte(`"identifier":"`+nfeID+`","model":"nfe","operation":"nfeCancelDocument","status":"unknown"`)) {
			t.Errorf("unknown cancellation missing from the ledger: %s", entries)
		}
	})
	t.Run("unknown-issue", func(t *testing.T) {
		ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
		code, out, cancels := run(t, "drop-issue", ledger)
		if code != 1 || cancels != 0 || strings.Count(strings.TrimSpace(out), "\n") != 4 {
			t.Fatalf("an unknown issue must stop the run without a resend or any later step: %d %d %s", code, cancels, out)
		}
		entries, _ := os.ReadFile(ledger)
		if string(entries) != `{"identifier":"controlled-001","model":"nfe","operation":"nfeIssueDocument","status":"unknown"}`+"\n" {
			t.Errorf("the unknown issue must be the only ledger entry, under the payload ID: %s", entries)
		}
	})
	// The ledger is writable up front but not when the issued document must be recorded.
	t.Run("ledger-lost-mid-run", func(t *testing.T) {
		ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
		code, out, cancels := run(t, "lose-ledger", ledger)
		if code != 1 || cancels != 1 || !strings.Contains(out, `"kind":"ledger","ok":false,"operation":"nfeIssueDocument"`) || !strings.Contains(out, `"step":"ledger"`) {
			t.Fatalf("a ledger write failing mid-run must fail the run with a masked line, and the document is still cancelled: %d %d %s", code, cancels, out)
		}
	})
	// A mutation needs a writable ledger: without one the run stops before any request.
	for name, ledger := range map[string]string{"unwritable-ledger": t.TempDir(), "missing-ledger": ""} {
		t.Run(name, func(t *testing.T) {
			code, out, cancels := run(t, "", ledger)
			if code != 77 || strings.TrimSpace(out) != `{"reason":"ledger","result":"blocked"}` || cancels != 0 {
				t.Fatalf("a mutation without a writable ledger must be blocked: %d %d %s", code, cancels, out)
			}
		})
	}
}
