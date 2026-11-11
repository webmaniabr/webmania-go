// Exemplo neutro de homologação (ambiente 2) com o SDK Go.
// Lê a configuração somente de variáveis WEBMANIA_*, imprime uma linha JSON mascarada por passo
// e nunca repete uma mutação. Sem WEBMANIA_LIVE=1 não faz nenhuma requisição (saída 77).
// Um documento cujas consultas não terminam em "aprovado" encerra com 1 e não é cancelado; sem
// status final dentro do limite de consultas, fica no ledger com status "unknown".
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	sdk "github.com/webmaniabr/webmania-go"
	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	nfse "github.com/webmaniabr/webmania-go/model/api2nfseissuance"
)

const blocked = 77

var (
	pollAttempts = 24
	pollInterval = 5 * time.Second
	failed       = 0
	terminalNfe  = map[string]bool{"aprovado": true, "reprovado": true, "denegado": true, "cancelado": true}
)

type unknownOutcome struct{}

func (unknownOutcome) Error() string { return "outcome unknown" }

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

func ledger(model, identifier, status, operation string) {
	path := os.Getenv("WEBMANIA_LIVE_LEDGER")
	if path == "" || identifier == "" {
		return
	}
	// A ledger that cannot be written is reported (masked) and fails the run: the operator would
	// otherwise lack the identifier needed for cleanup.
	failure := func() {
		emit("ledger", operation, false, map[string]any{"kind": "ledger", "ref": ref(identifier)})
	}
	f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if e != nil {
		failure()
		return
	}
	b, _ := json.Marshal(map[string]string{"model": model, "identifier": identifier, "status": status, "operation": operation})
	_, e = f.Write(append(b, '\n'))
	if e = errors.Join(e, f.Close()); e != nil {
		failure()
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
	var list []map[string]any
	if json.Unmarshal(b, &list) == nil && len(list) > 0 {
		return list[0]
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

func fileSummary(f sdk.FileResponse) map[string]any {
	if f.Location != nil {
		return map[string]any{"redirect": true}
	}
	format := "other"
	switch {
	case bytes.HasPrefix(f.Data, []byte("%PDF-")):
		format = "pdf"
	case bytes.HasPrefix(f.Data, []byte("PK")):
		format = "zip"
	case bytes.HasPrefix(bytes.TrimLeft(bytes.TrimPrefix(f.Data, []byte("\xef\xbb\xbf")), " \t\r\n"), []byte("<")):
		format = "xml"
	}
	s := sha256.Sum256(f.Data)
	return map[string]any{"size": len(f.Data), "sha256": hex.EncodeToString(s[:]), "format": format}
}

// call runs one step; a sent mutation whose outcome is unknown stops the whole run.
func call[T any](step, operation, reference string, mutation bool, fn func() (T, error)) (T, bool, error) {
	v, e := fn()
	if e != nil {
		var er *sdk.Error
		extra := map[string]any{"ref": ref(reference)}
		if errors.As(e, &er) {
			extra["kind"], extra["http_status"] = er.Kind, nullable(er.Status)
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
	extra := map[string]any{"ref": ref(reference), "http_status": 200}
	if f, ok := any(v).(sdk.FileResponse); ok {
		extra["file"], extra["http_status"] = fileSummary(f), nullable(f.Status)
	}
	emit(step, operation, true, extra)
	return v, true, nil
}

func nullable(status int) any {
	if status == 0 {
		return nil
	}
	return status
}

// terminalNfse is a final NFS-e status: anything but empty, processando or pendente.
func terminalNfse(s string) bool { return s != "" && s != "processando" && s != "pendente" }

// notApproved reports a document whose consults did not end on "aprovado" and fails the run; it is
// never cancelled and its issue is never resent. A refused or denied document keeps its status in
// the ledger; one without a terminal status within the bound is ledgered as "unknown", for
// reconciliation by consult.
func notApproved(model, operation, uuid, status string, terminal bool) {
	if !terminal {
		status = "unknown"
	}
	ledger(model, uuid, status, operation)
	emit(model+".consult", operation, false, map[string]any{"kind": "not_approved", "ref": ref(uuid)})
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

func payload(name string) map[string]any {
	path := os.Getenv(name)
	if path == "" {
		return nil
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil || v["ambiente"] != float64(2) {
		return nil
	}
	return v
}

func nfeCycle(ctx context.Context, nfe *sdk.NfeClient, files *sdk.NfeFilesClient, document map[string]any) error {
	id, _ := document["ID"].(string)
	issued, ok, e := call("nfe.issue", "nfeIssueDocument", id, true, func() (*sdk.Result[fiscal.NfeIssueDocumentResponse], error) {
		return nfe.IssueDocument(ctx, sdk.NfeIssueDocumentParams{Body: typed[fiscal.NfeIssueDocumentBody](document)})
	})
	if e != nil {
		ledger("nfe", firstNonEmpty(id, "unidentified"), "unknown", "nfeIssueDocument")
		return e
	}
	if !ok {
		return nil
	}
	issuedFields := fields(issued.Value)
	uuid := text(issuedFields, "uuid")
	if uuid == "" {
		return nil
	}
	ledger("nfe", uuid, firstNonEmpty(text(issuedFields, "status"), "processando"), "nfeIssueDocument")
	consult := func(c context.Context) (any, error) {
		return nfe.GetDocument(c, sdk.NfeGetDocumentParams{Query: typed[fiscal.NfeGetDocumentQuery](map[string]string{"uuid": uuid})})
	}
	current := poll(ctx, "nfe.consult", "nfeGetDocument", uuid, consult, func(s string) bool { return terminalNfe[s] })
	if status := text(current, "status"); status != "aprovado" {
		notApproved("nfe", "nfeGetDocument", uuid, status, terminalNfe[status])
		return nil
	}
	auth := sdk.CompanyFileAuth()
	call("nfe.query", "nfeQueryDocument", uuid, false, func() (any, error) {
		return nfe.QueryDocument(ctx, sdk.NfeQueryDocumentParams{Body: typed[fiscal.NfeQueryDocumentBody](map[string]string{"uuid": uuid})})
	})
	call("nfe.danfe", "nfeDownloadDanfe", uuid, false, func() (sdk.FileResponse, error) {
		return files.DownloadDanfe(ctx, sdk.NfeDownloadDanfeParams{Identifier: uuid, Auth: auth})
	})
	call("nfe.danfe_simple", "nfeDownloadSimpleDanfe", uuid, false, func() (sdk.FileResponse, error) {
		return files.DownloadSimpleDanfe(ctx, sdk.NfeDownloadSimpleDanfeParams{Identifier: uuid, Auth: auth})
	})
	call("nfe.xml", "nfeDownloadXml", uuid, false, func() (sdk.FileResponse, error) {
		return files.DownloadXml(ctx, sdk.NfeDownloadXmlParams{Identifier: uuid, Auth: auth})
	})
	// An approved document is always cancelled, even after a failed step above.
	_, cancelled, e := call("nfe.cancel", "nfeCancelDocument", uuid, true, func() (any, error) {
		return nfe.CancelDocument(ctx, sdk.NfeCancelDocumentParams{Body: typed[fiscal.NfeCancelDocumentBody](map[string]string{"uuid": uuid, "motivo": "Cancelamento de teste em homologacao."})})
	})
	if e != nil {
		ledger("nfe", uuid, "unknown", "nfeCancelDocument")
		return e
	}
	if !cancelled {
		ledger("nfe", uuid, "aprovado", "nfeCancelDocument")
		return nil
	}
	final := poll(ctx, "nfe.consult_cancelled", "nfeGetDocument", uuid, consult, func(s string) bool { return s == "cancelado" })
	status := firstNonEmpty(text(final, "status"), "unknown")
	ledger("nfe", uuid, status, "nfeCancelDocument")
	if status != "cancelado" {
		// Only a consult that shows the cancellation counts as success.
		emit("nfe.consult_cancelled", "nfeGetDocument", false, map[string]any{"kind": "not_cancelled", "ref": ref(uuid)})
		return nil
	}
	call("nfe.cancellation_xml", "nfeDownloadCancellationXml", uuid, false, func() (sdk.FileResponse, error) {
		return files.DownloadCancellationXml(ctx, sdk.NfeDownloadCancellationXmlParams{Identifier: uuid, Auth: auth})
	})
	return nil
}

func nfseCycle(ctx context.Context, emitter *sdk.EmitterClient, files *sdk.EmitterFilesClient, document map[string]any) error {
	id, _ := document["ID"].(string)
	issued, ok, e := call("nfse.issue", "emitterIssueNfse", id, true, func() (any, error) {
		return emitter.IssueNfse(ctx, sdk.EmitterIssueNfseParams{Body: typed[nfse.EmitterIssueNfseBody](document)})
	})
	if e != nil {
		ledger("nfse", firstNonEmpty(id, "unidentified"), "unknown", "emitterIssueNfse")
		return e
	}
	if !ok {
		return nil
	}
	issuedFields := fields(issued)
	uuid := text(issuedFields, "uuid")
	if uuid == "" {
		if items, isList := issuedFields["info_nfse"].([]any); isList && len(items) > 0 {
			if first, isMap := items[0].(map[string]any); isMap {
				uuid = text(first, "uuid")
			}
		}
	}
	if uuid == "" {
		return nil
	}
	ledger("nfse", uuid, firstNonEmpty(text(issuedFields, "status"), "processando"), "emitterIssueNfse")
	consult := func(c context.Context) (any, error) {
		r, e := emitter.GetNfse(c, sdk.EmitterGetNfseParams{Identifier: uuid})
		if e != nil {
			return nil, e
		}
		return r.Value, nil
	}
	current := poll(ctx, "nfse.consult", "emitterGetNfse", uuid, consult, terminalNfse)
	if status := text(current, "status"); status != "aprovado" {
		notApproved("nfse", "emitterGetNfse", uuid, status, terminalNfse(status))
		return nil
	}
	auth := sdk.IssuerFileAuth()
	call("nfse.xml", "fileGetNfseXml", uuid, false, func() (sdk.FileResponse, error) {
		return files.GetNfseXml(ctx, sdk.FileGetNfseXmlParams{Identifier: uuid, Auth: auth})
	})
	if text(current, "pdf_nfse") != "" {
		call("nfse.pdf", "fileGetNfsePdf", uuid, false, func() (sdk.FileResponse, error) {
			return files.GetNfsePdf(ctx, sdk.FileGetNfsePdfParams{Identifier: uuid, Auth: auth})
		})
	} else {
		emit("nfse.pdf", "fileGetNfsePdf", true, map[string]any{"kind": "not_offered", "ref": ref(uuid)})
	}
	if text(current, "pdf_rps") != "" {
		call("nfse.rps_pdf", "fileGetNfseRpsPdf", uuid, false, func() (sdk.FileResponse, error) {
			return files.GetNfseRpsPdf(ctx, sdk.FileGetNfseRpsPdfParams{Identifier: uuid, Auth: auth})
		})
	} else {
		emit("nfse.rps_pdf", "fileGetNfseRpsPdf", true, map[string]any{"kind": "not_offered", "ref": ref(uuid)})
	}
	_, cancelled, e := call("nfse.cancel", "emitterCancelNfse", uuid, true, func() (any, error) {
		return emitter.CancelNfse(ctx, sdk.EmitterCancelNfseParams{Body: typed[nfse.EmitterCancelNfseBody](map[string]string{"uuid": uuid, "motivo": "1", "justificativa": "Cancelamento de teste em homologacao."})})
	})
	if e != nil {
		ledger("nfse", uuid, "unknown", "emitterCancelNfse")
		return e
	}
	if !cancelled {
		ledger("nfse", uuid, "aprovado", "emitterCancelNfse")
		return nil
	}
	final := poll(ctx, "nfse.consult_cancelled", "emitterGetNfse", uuid, consult, func(s string) bool { return s == "cancelado" })
	status := firstNonEmpty(text(final, "status"), "unknown")
	ledger("nfse", uuid, status, "emitterCancelNfse")
	if status != "cancelado" {
		emit("nfse.consult_cancelled", "emitterGetNfse", false, map[string]any{"kind": "not_cancelled", "ref": ref(uuid)})
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func block(reason string) int {
	b, _ := json.Marshal(map[string]string{"result": "blocked", "reason": reason})
	os.Stdout.Write(append(b, '\n'))
	return blocked
}

func origin(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func run() int {
	if os.Getenv("WEBMANIA_LIVE") != "1" {
		os.Stdout.WriteString(`{"result":"blocked"}` + "\n")
		return blocked
	}
	api1 := firstNonEmpty(os.Getenv("WEBMANIA_API1_BASE_URL"), "https://webmania.com.br/api")
	filesBase := firstNonEmpty(os.Getenv("WEBMANIA_FILES_BASE_URL"), "https://nfe.webmania.com.br")
	api2 := firstNonEmpty(os.Getenv("WEBMANIA_API2_BASE_URL"), "https://api.webmania.com.br")
	scope := firstNonEmpty(os.Getenv("WEBMANIA_LIVE_SCOPE"), "readonly")
	if scope != "readonly" && scope != "nfe" && scope != "nfse" && scope != "all" {
		return block("scope")
	}
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
	for _, raw := range []string{api1, filesBase, api2} {
		u, e := url.Parse(raw)
		if e != nil || !allowed[strings.ToLower(u.Hostname())] {
			return block("origin_not_allowed")
		}
	}
	for _, name := range []string{"WEBMANIA_CONSUMER_KEY", "WEBMANIA_CONSUMER_SECRET", "WEBMANIA_ACCESS_TOKEN", "WEBMANIA_ACCESS_TOKEN_SECRET", "WEBMANIA_ISSUER_TOKEN"} {
		if os.Getenv(name) == "" {
			return block("credentials")
		}
	}
	var nfeDocument, nfseDocument map[string]any
	if scope == "nfe" || scope == "all" {
		if nfeDocument = payload("WEBMANIA_LIVE_NFE_PAYLOAD"); nfeDocument == nil {
			return block("payload")
		}
	}
	if scope == "nfse" || scope == "all" {
		if nfseDocument = payload("WEBMANIA_LIVE_NFSE_PAYLOAD"); nfseDocument == nil {
			return block("payload")
		}
	}
	if nfeDocument != nil || nfseDocument != nil {
		// An unknown outcome is recorded only in the ledger: it must be writable before any mutation.
		path := os.Getenv("WEBMANIA_LIVE_LEDGER")
		if path == "" {
			return block("ledger")
		}
		f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if e != nil {
			return block("ledger")
		}
		f.Close()
	}
	company := sdk.CompanyCredentials{ConsumerKey: os.Getenv("WEBMANIA_CONSUMER_KEY"), ConsumerSecret: os.Getenv("WEBMANIA_CONSUMER_SECRET"), AccessToken: os.Getenv("WEBMANIA_ACCESS_TOKEN"), AccessTokenSecret: os.Getenv("WEBMANIA_ACCESS_TOKEN_SECRET")}
	issuer := sdk.IssuerCredentials{Token: os.Getenv("WEBMANIA_ISSUER_TOKEN")}
	nfe, e1 := sdk.NewNfeClient(company, sdk.Config{BaseURL: api1})
	emitter, e2 := sdk.NewEmitterClient(issuer, sdk.Config{BaseURL: api2})
	nfeFiles, e3 := sdk.NewNfeFilesClient(&company, sdk.Config{BaseURL: filesBase})
	emitterFiles, e4 := sdk.NewEmitterFilesClient(&issuer, sdk.Config{BaseURL: origin(api2)})
	if e := errors.Join(e1, e2, e3, e4); e != nil {
		emit("configuration", "", false, map[string]any{"kind": "configuration"})
		return 1
	}
	ctx := context.Background()
	call("readonly.sefaz", "nfeGetSefazStatus", "", false, func() (any, error) {
		return nfe.GetSefazStatus(ctx, sdk.NfeGetSefazStatusParams{})
	})
	call("readonly.certificate", "nfeGetCertificateValidity", "", false, func() (any, error) {
		return nfe.GetCertificateValidity(ctx, sdk.NfeGetCertificateValidityParams{})
	})
	call("readonly.tax_classes", "nfeListTaxClasses", "", false, func() (any, error) {
		return nfe.ListTaxClasses(ctx, sdk.NfeListTaxClassesParams{})
	})
	call("readonly.nfse_provider", "emitterGetNfseProviderStatus", "", false, func() (any, error) {
		return emitter.GetNfseProviderStatus(ctx, sdk.EmitterGetNfseProviderStatusParams{})
	})
	if nfeDocument != nil {
		if errors.Is(nfeCycle(ctx, nfe, nfeFiles, nfeDocument), unknownOutcome{}) {
			return 1
		}
	}
	if nfseDocument != nil {
		if errors.Is(nfseCycle(ctx, emitter, emitterFiles, nfseDocument), unknownOutcome{}) {
			return 1
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }
