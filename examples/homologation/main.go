// Emissão em homologação (ambiente 2) de um documento de qualquer modelo, com limpeza.
// Requer um payload privado preparado para a conta autorizada; nunca gera partes ou dados fiscais.
//
// Só envia com --execute. Antes de ler credenciais ou fazer qualquer requisição exige
// WEBMANIA_LIVE_ALLOWED_HOSTS (deve conter os hosts de WEBMANIA_API1_BASE_URL e
// WEBMANIA_API2_BASE_URL), um payload cujo "ambiente" de nível superior, como será enviado, seja o
// inteiro JSON 2, e um WEBMANIA_LIVE_LEDGER gravável. O documento emitido é consultado e, quando
// aprovado, cancelado; só uma consulta com status "cancelado" conclui a limpeza. Uma mutação nunca é
// reenviada: um resultado desconhecido, ou um documento sem status final dentro do limite de
// consultas, fica no ledger com status "unknown" e encerra com 1.
// Saída: uma linha JSON mascarada por passo. Códigos: 0 sucesso, 1 falha ou desconhecido, 77 bloqueado.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	sdk "github.com/webmaniabr/webmania-go"
	f "github.com/webmaniabr/webmania-go/model/api1fiscal"
	cte "github.com/webmaniabr/webmania-go/model/api2cteissuance"
	dce "github.com/webmaniabr/webmania-go/model/api2dceissuance"
	mdfe "github.com/webmaniabr/webmania-go/model/api2mdfeissuance"
	nfcom "github.com/webmaniabr/webmania-go/model/api2nfcomissuance"
	nfse "github.com/webmaniabr/webmania-go/model/api2nfseissuance"
)

const (
	blocked      = 77
	cancelReason = "Cancelamento de teste em homologacao."
)

var (
	pollAttempts = 24
	pollInterval = 5 * time.Second
	failed       = 0
)

type unknownOutcome struct{}

func (unknownOutcome) Error() string { return "outcome unknown" }

type clients struct {
	nfe     *sdk.NfeClient
	emitter *sdk.EmitterClient
}

// document is what a consult or a cancellation needs to address the issued document.
type document struct{ uuid, chave string }

// flow is one model's issue, consult and cancel operations.
type flow struct {
	model, issueOp, consultOp, cancelOp string
	api2, byKey                         bool
	issue                               func(context.Context, clients) (any, error)
	consult                             func(context.Context, clients, document) (any, error)
	cancel                              func(context.Context, clients, document) (any, error)
	terminal                            func(string) bool
}

func ref(v string) any {
	if v == "" {
		return nil
	}
	s := sha256.Sum256([]byte(v))
	return hex.EncodeToString(s[:])[:12]
}

func emit(step, operation string, ok bool, extra map[string]any) {
	line := map[string]any{"step": step, "operation": operation, "ok": ok, "http_status": nil, "outcome": nil, "kind": nil, "ref": nil}
	for k, v := range extra {
		line[k] = v
	}
	if !ok {
		failed++
	}
	b, _ := json.Marshal(line)
	os.Stdout.Write(append(b, '\n'))
}

func block(reason string) int {
	b, _ := json.Marshal(map[string]string{"result": "blocked", "reason": reason})
	os.Stdout.Write(append(b, '\n'))
	return blocked
}

// ledger appends one private line per observed document state; a failed write fails the run.
func ledger(model, identifier, status, operation string) {
	path := os.Getenv("WEBMANIA_LIVE_LEDGER")
	fh, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if e == nil {
		b, _ := json.Marshal(map[string]string{"model": model, "identifier": identifier, "status": status, "operation": operation})
		_, e = fh.Write(append(b, '\n'))
		e = errors.Join(e, fh.Close())
	}
	if e != nil {
		emit("ledger", operation, false, map[string]any{"kind": "ledger", "ref": ref(identifier)})
	}
}

// fields reads a typed result generically, only to find identifiers and statuses.
func fields(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	if json.Unmarshal(b, &m) == nil {
		if inner, wrapped := m["Value"]; wrapped && len(m) == 2 {
			if _, status := m["Status"]; status {
				return fields(inner)
			}
		}
		return m
	}
	return map[string]any{}
}

func text(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func typed[T any](v any) T {
	var out T
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// call runs one step; a sent mutation whose outcome is unknown stops the whole run.
func call(step, operation, reference string, mutation bool, fn func() (any, error)) (any, bool, error) {
	v, e := fn()
	if e != nil {
		var er *sdk.Error
		extra := map[string]any{"ref": ref(reference)}
		if errors.As(e, &er) {
			extra["kind"] = er.Kind
			if er.Status != 0 {
				extra["http_status"] = er.Status
			}
			if er.Outcome != "" {
				extra["outcome"] = er.Outcome
			}
		}
		emit(step, operation, false, extra)
		if mutation && er != nil && er.Outcome == "unknown" {
			return v, false, unknownOutcome{}
		}
		return v, false, nil
	}
	emit(step, operation, true, map[string]any{"ref": ref(reference), "http_status": 200})
	return v, true, nil
}

func poll(ctx context.Context, step, operation, reference string, fetch func(context.Context) (any, error), done func(string) bool) map[string]any {
	var last map[string]any
	for attempt := 0; attempt < pollAttempts; attempt++ {
		v, ok, _ := call(step, operation, reference, false, func() (any, error) { return fetch(ctx) })
		if ok {
			last = fields(v)
			if done(text(last, "status")) {
				return last
			}
		}
		if attempt < pollAttempts-1 {
			time.Sleep(pollInterval)
		}
	}
	return last
}

// body decodes the payload into the request model and accepts it only when the body that will be
// sent carries the JSON integer 2 as its top-level "ambiente" (2.0, 2e0, "2" and 1 are refused).
func body[B any](raw []byte) (B, bool) {
	var b B
	if json.Unmarshal(raw, &b) != nil {
		return b, false
	}
	sent, e := json.Marshal(b)
	var top map[string]json.RawMessage
	if e != nil || json.Unmarshal(sent, &top) != nil || string(top["ambiente"]) != "2" {
		return b, false
	}
	return b, true
}

func api2Terminal(s string) bool { return s != "" && s != "processando" && s != "pendente" }

func nfeFlow(model string, issue func(context.Context, clients) (any, error)) flow {
	return flow{model: model, issueOp: "nfeIssueDocument", consultOp: "nfeGetDocument", cancelOp: "nfeCancelDocument", issue: issue,
		consult: func(ctx context.Context, c clients, d document) (any, error) {
			return c.nfe.GetDocument(ctx, sdk.NfeGetDocumentParams{Query: typed[f.NfeGetDocumentQuery](map[string]string{"uuid": d.uuid})})
		},
		cancel: func(ctx context.Context, c clients, d document) (any, error) {
			return c.nfe.CancelDocument(ctx, sdk.NfeCancelDocumentParams{Body: typed[f.NfeCancelDocumentBody](map[string]string{"uuid": d.uuid, "motivo": cancelReason})})
		},
		terminal: func(s string) bool { return s == "aprovado" || s == "reprovado" || s == "denegado" || s == "cancelado" }}
}

// prepare validates the payload for the model and returns its flow, or a blocked reason.
func prepare(model string, raw []byte, top map[string]json.RawMessage) (flow, string) {
	switch model {
	case "nfe":
		b, ok := body[f.Nfe55IssueRequest](raw)
		if !ok {
			return flow{}, "payload"
		}
		return nfeFlow(model, func(ctx context.Context, c clients) (any, error) { return c.nfe.IssueNfe(ctx, b) }), ""
	case "nfce":
		b, ok := body[f.Nfce65IssueRequest](raw)
		if !ok {
			return flow{}, "payload"
		}
		return nfeFlow(model, func(ctx context.Context, c clients) (any, error) { return c.nfe.IssueNfce(ctx, b) }), ""
	case "nfse":
		b, ok := body[nfse.EmitterIssueNfseBody](raw)
		if !ok {
			return flow{}, "payload"
		}
		return flow{model: model, issueOp: "emitterIssueNfse", consultOp: "emitterGetNfse", cancelOp: "emitterCancelNfse", api2: true,
			issue: func(ctx context.Context, c clients) (any, error) {
				return c.emitter.IssueNfse(ctx, sdk.EmitterIssueNfseParams{Body: b})
			},
			consult: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.GetNfse(ctx, sdk.EmitterGetNfseParams{Identifier: d.uuid})
			},
			cancel: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.CancelNfse(ctx, sdk.EmitterCancelNfseParams{Body: typed[nfse.EmitterCancelNfseBody](map[string]string{"uuid": d.uuid, "motivo": "1", "justificativa": cancelReason})})
			}, terminal: api2Terminal}, ""
	case "cte", "cte_os":
		if model == "cte_os" && string(top["modelo"]) != "67" && string(top["modelo"]) != `"67"` {
			return flow{}, "payload"
		}
		b, ok := body[cte.EmitterIssueCteBody](raw)
		if !ok {
			return flow{}, "payload"
		}
		return flow{model: model, issueOp: "emitterIssueCte", consultOp: "emitterGetCte", cancelOp: "emitterCancelCte", api2: true,
			issue: func(ctx context.Context, c clients) (any, error) {
				return c.emitter.IssueCte(ctx, sdk.EmitterIssueCteParams{Body: b})
			},
			consult: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.GetCte(ctx, sdk.EmitterGetCteParams{Identifier: d.uuid})
			},
			cancel: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.CancelCte(ctx, sdk.EmitterCancelCteParams{Body: typed[cte.EmitterCancelCteBody](map[string]string{"uuid": d.uuid, "justificativa": cancelReason})})
			}, terminal: api2Terminal}, ""
	case "mdfe":
		b, ok := body[mdfe.EmitterIssueMdfeBody](raw)
		if !ok {
			return flow{}, "payload"
		}
		return flow{model: model, issueOp: "emitterIssueMdfe", consultOp: "emitterGetMdfe", cancelOp: "emitterCancelMdfe", api2: true,
			issue: func(ctx context.Context, c clients) (any, error) {
				return c.emitter.IssueMdfe(ctx, sdk.EmitterIssueMdfeParams{Body: b})
			},
			consult: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.GetMdfe(ctx, sdk.EmitterGetMdfeParams{Key: d.uuid})
			},
			cancel: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.CancelMdfe(ctx, sdk.EmitterCancelMdfeParams{Body: typed[mdfe.EmitterCancelMdfeBody](map[string]string{"uuid": d.uuid, "justificativa": cancelReason})})
			}, terminal: api2Terminal}, ""
	case "nfcom":
		b, ok := body[nfcom.EmitterIssueNfcomBody](raw)
		if !ok {
			return flow{}, "payload"
		}
		return flow{model: model, issueOp: "emitterIssueNfcom", consultOp: "emitterGetNfcom", cancelOp: "emitterCancelNfcom", api2: true,
			issue: func(ctx context.Context, c clients) (any, error) {
				return c.emitter.IssueNfcom(ctx, sdk.EmitterIssueNfcomParams{Body: b})
			},
			consult: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.GetNfcom(ctx, sdk.EmitterGetNfcomParams{Identifier: d.uuid})
			},
			// The NFCom cancellation addresses the document by its access key.
			byKey: true,
			cancel: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.CancelNfcom(ctx, sdk.EmitterCancelNfcomParams{Body: typed[nfcom.EmitterCancelNfcomBody](map[string]string{"chave": d.chave, "justificativa": cancelReason})})
			}, terminal: api2Terminal}, ""
	case "dce":
		b, ok := body[dce.EmitterIssueDceBody](raw)
		if !ok {
			return flow{}, "payload"
		}
		return flow{model: model, issueOp: "emitterIssueDce", consultOp: "emitterGetDce", cancelOp: "emitterCancelDce", api2: true,
			issue: func(ctx context.Context, c clients) (any, error) {
				return c.emitter.IssueDce(ctx, sdk.EmitterIssueDceParams{Body: b})
			},
			consult: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.GetDce(ctx, sdk.EmitterGetDceParams{Identifier: d.uuid})
			},
			cancel: func(ctx context.Context, c clients, d document) (any, error) {
				return c.emitter.CancelDce(ctx, sdk.EmitterCancelDceParams{Body: typed[dce.EmitterCancelDceBody](map[string]string{"uuid": d.uuid, "justificativa": cancelReason})})
			}, terminal: api2Terminal}, ""
	}
	return flow{}, "model"
}

// cycle issues once, consults until a terminal status and cancels an approved document; only a
// consult that shows "cancelado" completes the cleanup.
func cycle(ctx context.Context, c clients, fl flow, id string) int {
	issued, ok, e := call(fl.model+".issue", fl.issueOp, id, true, func() (any, error) { return fl.issue(ctx, c) })
	if e != nil {
		ledger(fl.model, firstNonEmpty(id, "unidentified"), "unknown", fl.issueOp)
		return 1
	}
	if !ok {
		return 1
	}
	issuedFields := fields(issued)
	if items, isList := issuedFields["info_nfse"].([]any); isList && len(items) > 0 && text(issuedFields, "uuid") == "" {
		if first, isMap := items[0].(map[string]any); isMap {
			issuedFields = first
		}
	}
	doc := document{uuid: text(issuedFields, "uuid"), chave: text(issuedFields, "chave")}
	if doc.uuid == "" {
		// Accepted without an identifier: recorded for reconciliation by the payload ID.
		ledger(fl.model, firstNonEmpty(id, "unidentified"), firstNonEmpty(text(issuedFields, "status"), "unknown"), fl.issueOp)
		emit(fl.model+".issue", fl.issueOp, false, map[string]any{"kind": "no_identifier", "ref": ref(id)})
		return 1
	}
	ledger(fl.model, doc.uuid, firstNonEmpty(text(issuedFields, "status"), "processando"), fl.issueOp)
	consult := func(cc context.Context) (any, error) { return fl.consult(cc, c, doc) }
	current := poll(ctx, fl.model+".consult", fl.consultOp, doc.uuid, consult, fl.terminal)
	status := text(current, "status")
	if status != "aprovado" {
		// Rejected documents need no cancellation. One without a terminal status within the bound is
		// ledgered as unknown: reconciliation consults it later, and the issue is never resent.
		if !fl.terminal(status) {
			status = "unknown"
		}
		ledger(fl.model, doc.uuid, status, fl.consultOp)
		emit(fl.model+".consult", fl.consultOp, false, map[string]any{"kind": "not_approved", "ref": ref(doc.uuid)})
		return 1
	}
	doc.chave = firstNonEmpty(doc.chave, text(current, "chave"))
	if fl.byKey && doc.chave == "" {
		ledger(fl.model, doc.uuid, "aprovado", fl.cancelOp)
		emit(fl.model+".cancel", fl.cancelOp, false, map[string]any{"kind": "no_access_key", "ref": ref(doc.uuid)})
		return 1
	}
	_, cancelled, e := call(fl.model+".cancel", fl.cancelOp, doc.uuid, true, func() (any, error) { return fl.cancel(ctx, c, doc) })
	if e != nil {
		ledger(fl.model, doc.uuid, "unknown", fl.cancelOp)
		return 1
	}
	if !cancelled {
		ledger(fl.model, doc.uuid, "aprovado", fl.cancelOp)
		return 1
	}
	final := poll(ctx, fl.model+".consult_cancelled", fl.consultOp, doc.uuid, consult, func(s string) bool { return s == "cancelado" })
	status = firstNonEmpty(text(final, "status"), "unknown")
	ledger(fl.model, doc.uuid, status, fl.cancelOp)
	if status != "cancelado" {
		emit(fl.model+".consult_cancelled", fl.consultOp, false, map[string]any{"kind": "not_cancelled", "ref": ref(doc.uuid)})
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func run(args []string) int {
	flags := flag.NewFlagSet("homologation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	model := flags.String("model", "", "nfe|nfce|nfse|cte|cte_os|mdfe|nfcom|dce")
	file := flags.String("payload", "", "arquivo JSON privado completo com ambiente=2")
	execute := flags.Bool("execute", false, "enviar a emissão de homologação autorizada")
	if flags.Parse(args) != nil {
		return block("arguments")
	}
	if !*execute || *file == "" {
		return block("execute")
	}
	if !map[string]bool{"nfe": true, "nfce": true, "nfse": true, "cte": true, "cte_os": true, "mdfe": true, "nfcom": true, "dce": true}[*model] {
		return block("model")
	}
	api1 := firstNonEmpty(os.Getenv("WEBMANIA_API1_BASE_URL"), "https://webmania.com.br/api")
	api2 := firstNonEmpty(os.Getenv("WEBMANIA_API2_BASE_URL"), "https://api.webmania.com.br")
	// The allow list is required: without it no origin is trusted (fail closed, before any credential).
	allowed := map[string]bool{}
	for _, h := range strings.Split(os.Getenv("WEBMANIA_LIVE_ALLOWED_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			allowed[h] = true
		}
	}
	if len(allowed) == 0 {
		return block("allowed_hosts")
	}
	for _, raw := range []string{api1, api2} {
		u, e := url.Parse(raw)
		if e != nil || !allowed[strings.ToLower(u.Hostname())] {
			return block("origin_not_allowed")
		}
	}
	fp, e := os.Open(*file)
	if e != nil {
		return block("payload")
	}
	raw, e := io.ReadAll(io.LimitReader(fp, (8<<20)+1))
	fp.Close()
	var top map[string]json.RawMessage
	if e != nil || len(raw) > 8<<20 || json.Unmarshal(raw, &top) != nil || string(bytes.TrimSpace(top["ambiente"])) != "2" {
		return block("payload")
	}
	fl, reason := prepare(*model, raw, top)
	if reason != "" {
		return block(reason)
	}
	// An unknown outcome is recorded only in the ledger: it must be writable before the mutation.
	path := os.Getenv("WEBMANIA_LIVE_LEDGER")
	if path == "" {
		return block("ledger")
	}
	lf, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if e != nil {
		return block("ledger")
	}
	lf.Close()
	var c clients
	if fl.api2 {
		if os.Getenv("WEBMANIA_ISSUER_TOKEN") == "" {
			return block("credentials")
		}
		c.emitter, e = sdk.NewEmitterClient(sdk.IssuerCredentials{Token: os.Getenv("WEBMANIA_ISSUER_TOKEN")}, sdk.Config{BaseURL: api2})
	} else {
		for _, name := range []string{"WEBMANIA_CONSUMER_KEY", "WEBMANIA_CONSUMER_SECRET", "WEBMANIA_ACCESS_TOKEN", "WEBMANIA_ACCESS_TOKEN_SECRET"} {
			if os.Getenv(name) == "" {
				return block("credentials")
			}
		}
		c.nfe, e = sdk.NewNfeClient(sdk.CompanyCredentials{ConsumerKey: os.Getenv("WEBMANIA_CONSUMER_KEY"), ConsumerSecret: os.Getenv("WEBMANIA_CONSUMER_SECRET"), AccessToken: os.Getenv("WEBMANIA_ACCESS_TOKEN"), AccessTokenSecret: os.Getenv("WEBMANIA_ACCESS_TOKEN_SECRET")}, sdk.Config{BaseURL: api1})
	}
	if e != nil {
		emit("configuration", "", false, map[string]any{"kind": "configuration"})
		return 1
	}
	var id string
	_ = json.Unmarshal(top["ID"], &id)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return cycle(ctx, c, fl, id)
}

func main() { os.Exit(run(os.Args[1:])) }
