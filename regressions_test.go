package webmania_test

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/webmaniabr/webmania-go"
	filemodels "github.com/webmaniabr/webmania-go/model/api1files"
	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	docs "github.com/webmaniabr/webmania-go/model/api2monitordocuments"
	management "github.com/webmaniabr/webmania-go/model/api2monitormanagement"
	reads "github.com/webmaniabr/webmania-go/model/api2monitorreads"
	"github.com/webmaniabr/webmania-go/value"
)

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func mustNfe(t *testing.T, url string) *sdk.NfeClient {
	t.Helper()
	c, e := sdk.NewNfeClient(company, sdk.Config{BaseURL: url})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func mustMonitor(t *testing.T, url string) *sdk.MonitorClient {
	t.Helper()
	c, e := sdk.NewMonitorClient(sdk.MonitorCredentials{Token: "synthetic-monitor"}, sdk.Config{BaseURL: url})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func mustError(t *testing.T, e error, kind string) *sdk.Error {
	t.Helper()
	var er *sdk.Error
	if !errors.As(e, &er) {
		t.Fatalf("expected structured error %s, got %v", kind, e)
	}
	if er.Kind != kind {
		t.Fatalf("expected %s, got %#v", kind, er)
	}
	return er
}
func cancelParams(id string) sdk.NfeCancelDocumentParams {
	return sdk.NfeCancelDocumentParams{Body: fiscal.NfeCancelDocumentBody{Uuid: value.Some(id), Motivo: value.Some("Synthetic cancellation reason")}}
}
func rawServer(t *testing.T, response func(net.Conn, *http.Request), count *atomic.Int64) (string, func()) {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				r, e := http.ReadRequest(bufio.NewReader(c))
				if e != nil {
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				count.Add(1)
				response(c, r)
			}()
		}
	}()
	return "http://" + ln.Addr().String(), func() { close(done); _ = ln.Close(); wg.Wait() }
}
func TestHTTP01Framing(t *testing.T) {
	for _, response := range []string{"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 19\r\nConnection: close\r\n\r\n{\"status\":\"online\"}", "HTTP/1.0 200 OK\r\nContent-Type: application/json\r\n\r\n{\"status\":\"online\"}", "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n{\"status\":\"online\"}", "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n13\r\n{\"status\":\"online\"}\r\n0\r\n\r\n"} {
		var count atomic.Int64
		url, close := rawServer(t, func(c net.Conn, _ *http.Request) { _, _ = io.WriteString(c, response) }, &count)
		c := mustNfe(t, url)
		for n := 0; n < 2; n++ {
			if _, e := c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{}); e != nil {
				t.Fatalf("complete framing: %v", e)
			}
		}
		close()
		if count.Load() != 2 {
			t.Fatal("replayed framing request")
		}
	}
	for _, response := range []string{"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 99\r\n\r\n{\"status\":\"online\"}", "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n13\r\n{\"status\":\"online\"}\r\n"} {
		var count atomic.Int64
		url, close := rawServer(t, func(c net.Conn, _ *http.Request) { io.WriteString(c, response) }, &count)
		c := mustNfe(t, url)
		_, e := c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
		if e == nil {
			t.Error("truncation accepted")
		}
		close()
		if count.Load() != 1 {
			t.Error("truncation replayed")
		}
	}
}
func TestHTTP02InformationAndAmbiguity(t *testing.T) {
	cases := []struct {
		response string
		valid    bool
	}{{"HTTP/1.1 103 Early Hints\r\nLink: </a>\r\n\r\nHTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\nTrailer: X-Finished\r\n\r\n13\r\n{\"status\":\"online\"}\r\n0\r\nX-Finished: yes\r\n\r\n", true}, {"HTTP/1.1 101 Switching Protocols\r\n\r\n", false}, {"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 19\r\nTransfer-Encoding: chunked\r\n\r\n", false}, {"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 19\r\nContent-Length: 19\r\n\r\n{\"status\":\"online\"}", false}}
	for _, tc := range cases {
		var count atomic.Int64
		url, close := rawServer(t, func(c net.Conn, _ *http.Request) { io.WriteString(c, tc.response) }, &count)
		_, e := mustNfe(t, url).GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
		if (e == nil) != tc.valid {
			t.Errorf("valid=%v got %v", tc.valid, e)
		}
		close()
		if count.Load() != 1 {
			t.Fatal("wrong request count")
		}
	}
}
func TestHTTP03NoImplicitReplayAndRecovery(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			var count atomic.Int64
			var sockets sync.Map
			url, close := rawServer(t, func(conn net.Conn, r *http.Request) {
				if r.Method != method {
					t.Error("wrong wire verb")
				}
				sockets.Store(conn.RemoteAddr().String(), true)
			}, &count)
			c := mustNfe(t, url)
			for n := 0; n < 2; n++ {
				var e error
				switch method {
				case "GET":
					f, _ := sdk.NewEmitterFilesClient(&sdk.IssuerCredentials{Token: "issuer"}, sdk.Config{BaseURL: url})
					_, e = f.GetNfseXml(context.Background(), sdk.FileGetNfseXmlParams{Identifier: uuid, Auth: sdk.IssuerFileAuth()})
				case "POST":
					_, e = c.UpdateCompany(context.Background(), sdk.FiscalUpdateCompanyParams{Body: fiscal.FiscalUpdateCompanyBody{RazaoSocial: value.Some("Synthetic company")}})
				case "PUT":
					_, e = c.CancelDocument(context.Background(), cancelParams(uuid))
				case "DELETE":
					_, e = c.DeleteTaxClasses(context.Background(), sdk.NfeDeleteTaxClassesParams{Body: fiscal.NfeDeleteTaxClassesBody{Referencia: value.Some(json.RawMessage(`"synthetic"`))}})
				}
				var er *sdk.Error
				if !errors.As(e, &er) || er.Outcome != "unknown" {
					t.Fatalf("sent effects lost: %#v", e)
				}
				if method == "PUT" {
					if er.Reference == nil || er.Reference.Reveal()["uuid"] != uuid {
						t.Fatal("safe recovery selector unavailable")
					}
					b, _ := json.Marshal(er)
					if strings.Contains(string(b), uuid) {
						t.Fatal("recovery selector leaked")
					}
				}
			}
			close()
			if count.Load() != 2 {
				t.Fatalf("replayed: %d requests", count.Load())
			}
			n := 0
			sockets.Range(func(k, v any) bool { n++; return true })
			if n != 2 {
				t.Fatal("fresh connections not proved")
			}
		})
	}
}
func TestERR01PresenceDiscriminator(t *testing.T) {
	for _, v := range []any{false, nil, json.Number("0"), "", []any{}, map[string]any{}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, map[string]any{"status": "online", "error": v})
		}))
		_, e := mustNfe(t, s.URL).GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
		mustError(t, e, "business")
		s.Close()
	}
}
func TestERR02SerializedSnapshotDepthAndKeys(t *testing.T) {
	private := "synthetic-private-extension"
	var nested any = map[string]any{"12345": private}
	for n := 0; n < 48; n++ {
		nested = map[string]any{fmt.Sprint(n): nested}
	}
	b, _ := json.Marshal(nested)
	var count atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("X-Correlation-Id", private+" 1.2345e4")
		jsonResponse(w, map[string]any{"error": private, "detail": map[string]any{"12345": json.Number("12345"), private: private}, "safe": "Use a valid CFOP"})
	}))
	defer s.Close()
	c := mustNfe(t, s.URL)
	_, e := c.UpdateCompany(context.Background(), sdk.FiscalUpdateCompanyParams{Body: fiscal.FiscalUpdateCompanyBody{Extras: map[string]json.RawMessage{private: b}}})
	er := mustError(t, e, "business")
	diag, _ := json.Marshal(er)
	for _, secret := range []string{private, "12345", "1.2345e4"} {
		if strings.Contains(string(diag), secret) {
			t.Fatalf("private snapshot value leaked: %s", secret)
		}
	}
	if !strings.Contains(string(diag), "Use a valid CFOP") {
		t.Error("safe diagnostic lost")
	}
	for n := 0; n < 70; n++ {
		nested = map[string]any{"level": nested}
	}
	b, _ = json.Marshal(nested)
	_, e = c.UpdateCompany(context.Background(), sdk.FiscalUpdateCompanyParams{Body: fiscal.FiscalUpdateCompanyBody{Extras: map[string]json.RawMessage{"extra": b}}})
	mustError(t, e, "validation")
	if count.Load() != 1 {
		t.Error("excessive input dispatched")
	}
}
func TestERR03UUIDCapabilityAndOpaqueCredentialCases(t *testing.T) {
	capability := "F00DBABE-0000-4000-8000-00000000CAFE"
	var correlation string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Correlation-Id", correlation)
		w.WriteHeader(500)
	}))
	defer s.Close()
	f, _ := sdk.NewNfeFilesClient(nil, sdk.Config{BaseURL: s.URL})
	correlation = strings.ToLower(capability)
	_, e := f.DownloadReport(context.Background(), sdk.FiscalDownloadReportParams{Uuid: capability, Auth: sdk.PublicReportAuth()})
	er := mustError(t, e, "http")
	if strings.Contains(er.CorrelationID, strings.ToLower(capability)) {
		t.Error("UUID capability case leaked")
	}
	opaque := sdk.CompanyCredentials{ConsumerKey: capability, ConsumerSecret: "synthetic-independent-secret", AccessToken: "synthetic-independent-access", AccessTokenSecret: "synthetic-independent-access-secret"}
	c, _ := sdk.NewNfeClient(opaque, sdk.Config{BaseURL: s.URL})
	_, e = c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
	er = mustError(t, e, "http")
	if er.CorrelationID != correlation {
		t.Errorf("opaque credential case was reinterpreted: %s", er.CorrelationID)
	}
}
func TestERR04RecoveryExplicitConsultationAndCollision(t *testing.T) {
	var mutation, consult atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			mutation.Add(1)
			w.WriteHeader(503)
			return
		}
		consult.Add(1)
		if r.URL.Query().Get("uuid") != uuid {
			t.Error("recovery changed selector")
		}
		jsonResponse(w, map[string]any{"uuid": uuid, "status": "processing"})
	}))
	defer s.Close()
	c := mustNfe(t, s.URL)
	_, e := c.CancelDocument(context.Background(), cancelParams(uuid))
	er := mustError(t, e, "http")
	selector := er.Reference.Reveal()["uuid"].(string)
	_, e = c.GetDocument(context.Background(), sdk.NfeGetDocumentParams{Query: fiscal.NfeGetDocumentQuery{Uuid: value.Some(selector)}})
	if e != nil || mutation.Load() != 1 || consult.Load() != 1 {
		t.Fatal("explicit consultation failed")
	}
	collision := company
	collision.ConsumerKey = uuid
	c, _ = sdk.NewNfeClient(collision, sdk.Config{BaseURL: s.URL})
	_, e = c.CancelDocument(context.Background(), cancelParams(uuid))
	er = mustError(t, e, "http")
	if er.Reference != nil {
		t.Error("credential collision retained")
	}
}
func TestERR05EffectsPolicy(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer s.Close()
	c := mustMonitor(t, s.URL)
	for _, refresh := range []bool{false, true} {
		q := docs.MonitorGetDocumentQuery{}
		if refresh {
			q.AtualizarStatus = value.Some(value.Number("1"))
		}
		_, e := c.GetDocument(context.Background(), sdk.MonitorGetDocumentParams{Fd: uuid, Query: q})
		er := mustError(t, e, "http")
		if (er.Outcome == "unknown") != refresh {
			t.Error("conditional effect incorrect")
		}
	}
	_, e := mustNfe(t, s.URL).GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
	if mustError(t, e, "http").Outcome != "" {
		t.Fatal("passive read marked mutation")
	}
}
func TestREDIR01ProtectedMetadataNoForward(t *testing.T) {
	var destination atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1) }))
	defer other.Close()
	for _, location := range []string{other.URL + "/private", "/darps/" + uuid} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", location); w.WriteHeader(302) }))
		c, _ := sdk.NewEmitterFilesClient(&sdk.IssuerCredentials{Token: "issuer"}, sdk.Config{BaseURL: s.URL})
		r, e := c.GetNfsePdf(context.Background(), sdk.FileGetNfsePdfParams{Identifier: uuid, Auth: sdk.IssuerFileAuth()})
		if e != nil || r.Location == nil || r.Location.Reveal() != location || len(r.Data) != 0 {
			t.Fatalf("documented redirect unavailable: %v", e)
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), location) {
			t.Error("location serialized")
		}
		s.Close()
	}
	if destination.Load() != 0 {
		t.Error("redirect followed")
	}
}
func TestREDIR02UnexpectedSentMutation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", "/other"); w.WriteHeader(303) }))
	defer s.Close()
	c := mustNfe(t, s.URL)
	_, e := c.CancelDocument(context.Background(), cancelParams(uuid))
	er := mustError(t, e, "redirect")
	if er.Outcome != "unknown" || er.Reference == nil {
		t.Error("sent mutation redirect lost recovery")
	}
	_, e = c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
	if mustError(t, e, "redirect").Outcome != "" {
		t.Error("read redirect uncertainty")
	}
}
func TestREDIR03LexicalObservationAndBoundaries(t *testing.T) {
	capability := "F00DBABE-0000-4000-8000-00000000CAFE"
	cases := []struct {
		location, echo  string
		collide, redact bool
	}{{"https://[invalid/private/prefix-" + capability + ".pdf", strings.ToLower(capability), false, true}, {"https://[invalid/private/%46" + capability[1:] + "%ZZ", strings.ToLower(capability), false, true}, {"https://[invalid/plain?token=" + capability, strings.ToLower(capability), false, false}, {"https://user:" + capability + "@[invalid/plain", capability, false, true}, {"https://[invalid/private/" + uuid, uuid, true, true}}
	for _, tc := range cases {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", tc.location)
			w.Header().Set("X-Correlation-Id", tc.echo)
			w.WriteHeader(302)
		}))
		_, e := mustNfe(t, s.URL).CancelDocument(context.Background(), cancelParams(uuid))
		er := mustError(t, e, "protocol")
		if (er.CorrelationID != tc.echo) != tc.redact {
			t.Errorf("contextual redaction: got %s", er.CorrelationID)
		}
		if tc.collide && er.Reference != nil {
			t.Error("observed capability collision retained")
		}
		s.Close()
	}
	for _, boundary := range []string{"header", "body", "none"} {
		var count atomic.Int64
		url, close := rawServer(t, func(c net.Conn, r *http.Request) {
			switch boundary {
			case "header":
				io.WriteString(c, "HTTP/1.1 503 Busy\r\nX-Padding: "+strings.Repeat("x", 2048))
			case "body":
				io.WriteString(c, "HTTP/1.1 503 Busy\r\nContent-Type: application/json\r\nContent-Length: 2048\r\n\r\n"+strings.Repeat("x", 2048))
			}
		}, &count)
		c, _ := sdk.NewNfeClient(company, sdk.Config{BaseURL: url, MaxHeaderBytes: 1024, MaxResponseBytes: 1024})
		_, e := c.CancelDocument(context.Background(), cancelParams(uuid))
		var er *sdk.Error
		if !errors.As(e, &er) {
			t.Fatal(e)
		}
		if (er.Reference == nil) != (boundary == "header") {
			t.Errorf("boundary %s recovery incorrect", boundary)
		}
		close()
	}
}
func TestPAGE01ConstantPageSizeAndContinuation(t *testing.T) {
	var requests atomic.Int64
	var sizes []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		sizes = append(sizes, r.URL.Query().Get("per_page"))
		page := r.URL.Query().Get("page")
		start, end := 0, 50
		if page == "2" {
			start, end = 50, 80
		}
		data := []any{}
		for n := start; n < end; n++ {
			data = append(data, map[string]any{"uuid": fmt.Sprintf("00000000-0000-4000-8000-%012d", n), "status": 1, "razao_social": fmt.Sprint(n)})
		}
		jsonResponse(w, map[string]any{"data": data, "meta": map[string]any{"current_page": json.Number(page), "last_page": 2, "per_page": 50, "total": 80}})
	}))
	defer s.Close()
	c := mustMonitor(t, s.URL)
	it, e := c.Companies(sdk.MonitorListCompaniesParams{Query: reads.MonitorListCompaniesQuery{PerPage: value.Some[int64](50)}}, sdk.PaginationOptions{MaxItems: 75})
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 75; n++ {
		v, ok, e := it.Next(context.Background())
		if e != nil || !ok {
			t.Fatalf("item %d failed %v", n, e)
		}
		label, _ := v.RazaoSocial.Get()
		if label != fmt.Sprint(n) {
			t.Error("offset skipped item")
		}
	}
	_, ok, e := it.Next(context.Background())
	if ok || e != nil || requests.Load() != 2 || fmt.Sprint(sizes) != "[50 50]" || it.Continuation() != (sdk.Continuation{Page: 2, Index: 25}) || it.StopReason() != "item_limit" {
		t.Fatalf("pagination limits failed: %#v %s", it.Continuation(), it.StopReason())
	}
}
func TestPAGE02ExplicitPageMetadataAndBufferedCancellation(t *testing.T) {
	var count atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		page := r.URL.Query().Get("page")
		jsonResponse(w, map[string]any{"data": []any{}, "meta": map[string]any{"current_page": json.Number(page), "last_page": 1, "per_page": 50, "total": 0}})
	}))
	defer s.Close()
	c := mustMonitor(t, s.URL)
	it, e := c.Companies(sdk.MonitorListCompaniesParams{Query: reads.MonitorListCompaniesQuery{Page: value.Some[int64](2147483648)}}, sdk.PaginationOptions{})
	if e != nil {
		t.Fatal(e)
	}
	_, ok, e := it.Next(context.Background())
	if e != nil || ok || it.StopReason() != "complete" || count.Load() != 1 {
		t.Fatal("exact out-of-range start was not preserved")
	}
	_, e = c.Companies(sdk.MonitorListCompaniesParams{Query: reads.MonitorListCompaniesQuery{Page: value.Null[int64]()}}, sdk.PaginationOptions{})
	mustError(t, e, "validation")
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, map[string]any{"data": []any{map[string]any{"uuid": uuid, "status": 1, "razao_social": "one"}, map[string]any{"uuid": uuid, "status": 1, "razao_social": "two"}}, "meta": map[string]any{"current_page": 1, "last_page": 1, "per_page": 50, "total": 2}})
	}))
	defer s2.Close()
	c = mustMonitor(t, s2.URL)
	it, _ = c.Companies(sdk.MonitorListCompaniesParams{}, sdk.PaginationOptions{})
	_, ok, e = it.Next(context.Background())
	if e != nil || !ok {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, e = it.Next(ctx)
	if !errors.Is(e, context.Canceled) {
		t.Fatal("buffered cancellation lost")
	}
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, map[string]any{"data": []any{}, "meta": map[string]any{"current_page": 1, "last_page": 1, "per_page": 20, "total": 0}})
	}))
	defer s3.Close()
	it, _ = mustMonitor(t, s3.URL).Companies(sdk.MonitorListCompaniesParams{}, sdk.PaginationOptions{})
	_, _, e = it.Next(context.Background())
	mustError(t, e, "pagination_protocol")
}
func TestPOLL01FrozenReportContext(t *testing.T) {
	capability := "F00DBABE-0000-4000-8000-00000000CAFE"
	var calls atomic.Int64
	var bodies []string
	var times []time.Time
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		times = append(times, time.Now())
		n := calls.Add(1)
		if n == 1 {
			jsonResponse(w, map[string]any{"uuid": capability, "status": "processando", "url": "https://untrusted.invalid/reports/" + capability})
			return
		}
		w.Header().Set("X-Correlation-Id", strings.ToLower(capability))
		jsonResponse(w, map[string]any{"error": strings.ToLower(capability)})
	}))
	defer s.Close()
	c := mustNfe(t, s.URL)
	p := sdk.FiscalCreateOrPollReportParams{Body: fiscal.FiscalCreateOrPollReportBody{DataInicio: value.Some("2026-01-01"), DataFinal: value.Some("2026-01-02"), Modelo: value.Some("nfe"), Relatorio: value.Some("danfe")}}
	_, e := c.WaitReport(context.Background(), p, sdk.WaitOptions{MaxAttempts: 2, Timeout: 10 * time.Second})
	er := mustError(t, e, "business")
	if len(bodies) != 2 || bodies[0] != bodies[1] || times[1].Sub(times[0]) < 5*time.Second || strings.Contains(er.CorrelationID, strings.ToLower(capability)) {
		t.Error("report workflow lost frozen request or capability")
	}
	_, e = c.WaitReport(context.Background(), p, sdk.WaitOptions{Interval: time.Second})
	mustError(t, e, "validation")
	_, e = c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
	er = mustError(t, e, "business")
	if er.CorrelationID != strings.ToLower(capability) {
		t.Error("capability context leaked between workflows")
	}
}
func TestCANCEL01AfterSendVersusPreSend(t *testing.T) {
	received := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(received)
		<-r.Context().Done()
	}))
	defer s.Close()
	c := mustNfe(t, s.URL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-received; cancel() }()
	_, e := c.CancelDocument(ctx, cancelParams(uuid))
	er := mustError(t, e, "cancelled")
	if !errors.Is(e, context.Canceled) || er.Outcome != "unknown" || er.Reference == nil {
		t.Error("sent cancellation context lost")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, e = c.CancelDocument(ctx, cancelParams(uuid))
	er = mustError(t, e, "cancelled")
	if er.Outcome != "" || er.Reference != nil {
		t.Error("pre-send cancellation marked sent")
	}
}
func TestTYPE01TypedMonitorWrappers(t *testing.T) {
	var count atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"razao_social":"Synthetic company"`) {
			t.Error("body lost typed field")
		}
		if r.Method == "PUT" && r.URL.Path != "/2/monitor/empresas/"+uuid {
			t.Error("path lost wrapper field")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			w.WriteHeader(201)
		}
		jsonResponse(w, map[string]any{"uuid": uuid, "status": 1, "razao_social": "Synthetic company"})
	}))
	defer s.Close()
	c := mustMonitor(t, s.URL)
	create := management.MonitorCreateCompanyRequest{Cnpj: value.Some("00000000000000"), RazaoSocial: value.Some("Synthetic company"), Cep: value.Some("12345678"), CertificadoBase64: value.Some("synthetic-pfx"), CertificadoSenha: value.Some("synthetic-password"), DfeRetroativas: value.Some(false)}
	if _, e := c.CreateCompany(context.Background(), sdk.MonitorCreateCompanyParams{Body: create}); e != nil {
		t.Fatal(e)
	}
	update := management.MonitorUpdateCompanyRequest{RazaoSocial: value.Some("Synthetic company"), DfeRetroativas: value.Some(false)}
	if _, e := c.UpdateCompany(context.Background(), sdk.MonitorUpdateCompanyParams{Company: uuid, Body: update}); e != nil {
		t.Fatal(e)
	}
	if count.Load() != 2 {
		t.Fatal("wrong request count")
	}
}
func TestTYPE02CommonFieldsPresenceAndExtras(t *testing.T) {
	b := fiscal.NfeCancelRequest{Uuid: value.Some(uuid), Motivo: value.Some("Synthetic cancellation reason")}
	raw, e := json.Marshal(b)
	if e != nil || !strings.Contains(string(raw), "motivo") || !strings.Contains(string(raw), uuid) {
		t.Fatal("common fields absent")
	}
	m := management.MonitorUpdateCompanyBody{DfeRetroativas: value.Some(false), Status: value.Some(value.Number("0"))}
	raw, e = json.Marshal(m)
	if e != nil || !strings.Contains(string(raw), `"dfe_retroativas":false`) || !strings.Contains(string(raw), `"status":0`) {
		t.Fatal("false/zero lost")
	}
	m.Extras = map[string]json.RawMessage{"status": json.RawMessage("1")}
	if _, e = json.Marshal(m); e == nil {
		t.Error("extension collision accepted")
	}
	var future reads.MonitorCompany
	if e = json.Unmarshal([]byte(`{"future_field":{"9007199254740993":0.1234567890123456789}}`), &future); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(future)
	if !strings.Contains(string(raw), "0.1234567890123456789") {
		t.Fatal("exact future field lost")
	}
	q := filemodels.NfeDownloadXmlQuery{Download: value.Some[int64](1)}
	_ = q
}
func TestFILE01SniffingAndProtection(t *testing.T) {
	cases := []struct {
		mime, data       string
		valid, protected bool
	}{{"application/pdf", "<html>not PDF</html>", false, false}, {"application/pdf", `{"error":"not PDF"}`, false, false}, {"application/pdf", pdf, true, false}, {"application/pdf", protectedPDF, true, true}, {"application/xml", "<?xml version=\"1.0\"?><!--comment--><nfeProc/>", true, false}, {"application/xml", "<!DOCTYPE nfeProc SYSTEM \"http://127.0.0.1/secret\"><nfeProc/>", false, false}, {"application/xml", "<html/>", false, false}}
	for _, tc := range cases {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.mime)
			io.WriteString(w, tc.data)
		}))
		c, _ := sdk.NewNfeFilesClient(&company, sdk.Config{BaseURL: s.URL})
		var r sdk.FileResponse
		var e error
		if tc.mime == "application/pdf" {
			r, e = c.DownloadDanfe(context.Background(), sdk.NfeDownloadDanfeParams{Identifier: uuid, Auth: sdk.CompanyFileAuth()})
		} else {
			r, e = c.DownloadXml(context.Background(), sdk.NfeDownloadXmlParams{Identifier: uuid, Auth: sdk.CompanyFileAuth()})
		}
		if (e == nil) != tc.valid || tc.valid && r.Protected != tc.protected {
			t.Errorf("file result valid=%v got %v protected=%v", tc.valid, e, r.Protected)
		}
		s.Close()
	}
}
func TestENV01ApprovedTLSAndURLConstraints(t *testing.T) {
	var count atomic.Int64
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/approved/1/nfe/") {
			t.Error("approved prefix lost")
		}
		jsonResponse(w, map[string]any{"status": "online"})
	}))
	defer s.Close()
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	c, e := sdk.NewNfeClient(company, sdk.Config{BaseURL: s.URL + "/approved", RootCAs: pool})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{}); e != nil {
		t.Fatal(e)
	}
	c, _ = sdk.NewNfeClient(company, sdk.Config{BaseURL: s.URL})
	_, e = c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
	mustError(t, e, "transport")
	c, _ = sdk.NewNfeClient(company, sdk.Config{BaseURL: strings.Replace(s.URL, "127.0.0.1", "localhost", 1), RootCAs: pool})
	_, e = c.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
	mustError(t, e, "transport")
	for _, u := range []string{"http://remote.invalid", "https://user:pass@remote.invalid", "https://remote.invalid?query=1", "https://remote.invalid/#fragment", "https://remote.invalid/../path", "https://remote.invalid/%2e%2e/path"} {
		if _, e = sdk.NewNfeClient(company, sdk.Config{BaseURL: u}); e == nil {
			t.Errorf("unsafe base accepted %s", u)
		}
	}
	if count.Load() != 1 {
		t.Fatal("untrusted TLS sent HTTP request")
	}
}
func TestAUTH01ExplicitModesNoFallback(t *testing.T) {
	var requests atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/relatorio/"+uuid+"/" {
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Consumer-Key") != "" {
				t.Error("public report credentials leaked")
			}
			w.Header().Set("Content-Type", "application/zip")
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(401)
	}))
	defer s.Close()
	f, _ := sdk.NewNfeFilesClient(&company, sdk.Config{BaseURL: s.URL})
	_, e := f.DownloadReport(context.Background(), sdk.FiscalDownloadReportParams{Uuid: uuid, Auth: sdk.PublicReportAuth()})
	mustError(t, e, "http")
	ef, _ := sdk.NewEmitterFilesClient(&sdk.IssuerCredentials{Token: "issuer"}, sdk.Config{BaseURL: s.URL})
	_, e = ef.GetMdfeXml(context.Background(), sdk.FileGetMdfeXmlParams{Identifier: uuid, Auth: sdk.AnonymousFileAuth()})
	mustError(t, e, "configuration")
	_, e = ef.GetNfseXml(context.Background(), sdk.FileGetNfseXmlParams{Identifier: uuid, Auth: sdk.DocumentTokenAuth("synthetic-token")})
	mustError(t, e, "http")
	if requests.Load() != 2 {
		t.Fatal("forbidden mode dispatched or auth fallback occurred")
	}
}

func TestPAGEBufferedDeadlineAndFailedPageDoesNotAdvance(t *testing.T) {
	var calls atomic.Int64
	var pages []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "2" {
			w.WriteHeader(503)
			return
		}
		data := []any{}
		for n := 0; n < 50; n++ {
			data = append(data, map[string]any{"uuid": uuid, "status": 1, "razao_social": "Synthetic"})
		}
		jsonResponse(w, map[string]any{"data": data, "meta": map[string]any{"current_page": 1, "last_page": 2, "per_page": 50, "total": 80}})
	}))
	defer s.Close()
	c := mustMonitor(t, s.URL)
	it, _ := c.Companies(sdk.MonitorListCompaniesParams{}, sdk.PaginationOptions{})
	for n := 0; n < 50; n++ {
		_, ok, e := it.Next(context.Background())
		if !ok || e != nil {
			t.Fatal(e)
		}
	}
	for n := 0; n < 2; n++ {
		_, _, e := it.Next(context.Background())
		mustError(t, e, "http")
		if it.Continuation() != (sdk.Continuation{Page: 2, Index: 0}) {
			t.Error("failed page changed continuation")
		}
	}
	if strings.Join(pages, ",") != "1,2,2" {
		t.Fatal("explicit Next skipped failed page")
	}
	it, _ = c.Companies(sdk.MonitorListCompaniesParams{}, sdk.PaginationOptions{Timeout: 50 * time.Millisecond})
	_, _, e := it.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(60 * time.Millisecond)
	_, _, e = it.Next(context.Background())
	mustError(t, e, "pagination_timeout")
}

func TestDeadlinePreservesRuntimeInterruptionAndSentContext(t *testing.T) {
	received := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(received)
		<-r.Context().Done()
	}))
	defer s.Close()
	c, _ := sdk.NewNfeClient(company, sdk.Config{BaseURL: s.URL, Timeout: 40 * time.Millisecond})
	_, e := c.CancelDocument(context.Background(), cancelParams(uuid))
	er := mustError(t, e, "cancelled")
	if !errors.Is(e, context.DeadlineExceeded) || er.Outcome != "unknown" || er.Reference == nil {
		t.Fatal("deadline lost sent operation context")
	}
	select {
	case <-received:
	default:
		t.Fatal("timeout was not after send")
	}
}

func TestCancellationOverVerifiedTLS(t *testing.T) {
	received := make(chan struct{})
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(received)
		<-r.Context().Done()
	}))
	defer s.Close()
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	c, _ := sdk.NewNfeClient(company, sdk.Config{BaseURL: s.URL, RootCAs: pool})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-received; cancel() }()
	_, e := c.CancelDocument(ctx, cancelParams(uuid))
	er := mustError(t, e, "cancelled")
	if !errors.Is(e, context.Canceled) || er.Outcome != "unknown" || er.Reference == nil {
		t.Fatal("TLS cancellation lost sent context")
	}
}
