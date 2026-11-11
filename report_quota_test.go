package webmania_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/webmaniabr/webmania-go"
	files "github.com/webmaniabr/webmania-go/model/api1files"
	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	"github.com/webmaniabr/webmania-go/value"
)

const quotaCapability = "d8604a71-32c6-4a38-9a10-00000000e703"

type quotaRequest struct {
	method, path string
	headers      http.Header
	body         []byte
}

// An ordinary peer admits the complete request before dropping or replying.
// It remains listening until close, so an extra SDK connection is counted.
type quotaPeer struct {
	listener    net.Listener
	stop        chan struct{}
	done        chan struct{}
	admitted    chan struct{}
	workers     sync.WaitGroup
	mu          sync.Mutex
	requests    []quotaRequest
	connections int
	err         error
}

func newQuotaPeer(t *testing.T, reply []byte, delay time.Duration) *quotaPeer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &quotaPeer{listener: l, stop: make(chan struct{}), done: make(chan struct{}), admitted: make(chan struct{})}
	go func() {
		defer close(p.done)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.connections++
			p.mu.Unlock()
			p.workers.Add(1)
			go func() {
				defer p.workers.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				r, err := http.ReadRequest(bufio.NewReader(conn))
				var body []byte
				if err == nil {
					body, err = io.ReadAll(io.LimitReader(r.Body, 4097))
					_ = r.Body.Close()
					if err == nil && (len(body) > 4096 || int64(len(body)) != r.ContentLength) {
						err = fmt.Errorf("request body was not complete and bounded")
					}
				}
				p.mu.Lock()
				if err != nil {
					p.err = err
				} else {
					p.requests = append(p.requests, quotaRequest{r.Method, r.URL.RequestURI(), r.Header.Clone(), body})
					if len(p.requests) == 1 {
						close(p.admitted)
					}
				}
				p.mu.Unlock()
				if err != nil {
					return
				}
				if delay > 0 {
					select {
					case <-time.After(delay):
					case <-p.stop:
						return
					}
				}
				if reply != nil {
					_, _ = conn.Write(reply)
				}
			}()
		}
	}()
	return p
}

func (p *quotaPeer) origin() string { return "http://" + p.listener.Addr().String() }
func (p *quotaPeer) close(t *testing.T, count int) []quotaRequest {
	t.Helper()
	close(p.stop)
	_ = p.listener.Close()
	<-p.done
	p.workers.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil || len(p.requests) != count || p.connections != count {
		t.Fatalf("quota peer: requests=%d want=%d connections=%d fixture_error=%v", len(p.requests), count, p.connections, p.err)
	}
	return p.requests
}

func quotaReply(status int, media, body string) []byte {
	return []byte(fmt.Sprintf("HTTP/1.1 %d Fixture\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, media, len(body), body))
}

func TestReportQuotaEffect(t *testing.T) {
	creds := sdk.CompanyCredentials{ConsumerKey: "QuotaKeyOnly", ConsumerSecret: "QuotaSecretOnly", AccessToken: "QuotaAccessOnly", AccessTokenSecret: "QuotaAccessSecretOnly"}
	cases := []struct {
		name, operation, kind, outcome string
		reply                          []byte
		delay                          time.Duration
		cancelled, success             bool
		requests                       int
		cancelAfterAdmission           bool
	}{
		{"report-drop", "report", "transport", "unknown", nil, 0, false, false, 1, false},
		{"passive-get-drop", "passive", "transport", "", nil, 0, false, false, 1, false},
		{"typed-post-drop", "mutation", "transport", "unknown", nil, 0, false, false, 1, false},
		{"report-known-json422", "report", "http", "", quotaReply(422, "application/json", `{"error":"known rejection"}`), 0, false, false, 1, false},
		{"report-presend-cancel", "report", "cancelled", "", nil, 0, true, false, 0, false},
		{"report-valid-csv", "report", "", "", quotaReply(200, "text/csv", "id,status\n1,ok\n"), 0, false, true, 1, false},
		{"report-protocol200", "report", "protocol", "unknown", quotaReply(200, "application/pdf", "not a PDF"), 0, false, false, 1, false},
		{"report-timeout-after-admission", "report", "cancelled", "unknown", nil, 2 * time.Second, false, false, 1, false},
		{"report-business200", "report", "business", "unknown", quotaReply(200, "application/json", `{"error":false}`), 0, false, false, 1, false},
		{"report-cancel-after-admission", "report", "cancelled", "unknown", nil, 2 * time.Second, false, false, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := newQuotaPeer(t, tc.reply, tc.delay)
			timeout := 3 * time.Second
			if tc.delay > 0 {
				timeout = time.Second
			}
			cfg := sdk.Config{BaseURL: peer.origin(), Timeout: timeout}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			cancelDone := make(chan bool, 1)
			if tc.cancelAfterAdmission {
				go func() {
					select {
					case <-peer.admitted:
						cancel()
						cancelDone <- true
					case <-time.After(2 * time.Second):
						cancelDone <- false
					}
				}()
			}
			var err error
			var data []byte
			if tc.operation == "report" {
				client, e := sdk.NewNfeFilesClient(&creds, cfg)
				if e != nil {
					t.Fatal(e)
				}
				result, e := client.DownloadReport(ctx, sdk.FiscalDownloadReportParams{Uuid: files.Uuid(quotaCapability), Auth: sdk.PublicReportAuth()})
				err, data = e, result.Data
			} else {
				client, e := sdk.NewNfeClient(creds, cfg)
				if e != nil {
					t.Fatal(e)
				}
				if tc.operation == "passive" {
					_, err = client.GetSefazStatus(ctx, sdk.NfeGetSefazStatusParams{})
				} else {
					body := fiscal.FiscalCompanyUpdateRequest{Cnae: value.Null[json.RawMessage]()}
					_, err = client.UpdateCompany(ctx, sdk.FiscalUpdateCompanyParams{Body: body})
				}
			}
			requests := peer.close(t, tc.requests)
			if tc.cancelAfterAdmission && !<-cancelDone {
				t.Fatal("request admission was not observed before cancellation")
			}
			if tc.success {
				if err != nil || !bytes.Equal(data, []byte("id,status\n1,ok\n")) {
					t.Fatal("valid report CSV was not preserved")
				}
			} else {
				var e *sdk.Error
				if !errors.As(err, &e) || e.Kind != tc.kind || e.Outcome != tc.outcome {
					t.Fatalf("kind/outcome mismatch: %v", err)
				}
				if tc.name == "report-timeout-after-admission" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("timeout lost its deadline cause")
				}
				if (tc.cancelled || tc.cancelAfterAdmission) && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost its context cause")
				}
				if tc.operation == "report" && e.Reference != nil {
					t.Fatal("report capability must not become a recovery selector")
				}
				visible, marshalErr := json.Marshal(e)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				for _, secret := range []string{quotaCapability, creds.ConsumerKey, creds.ConsumerSecret, creds.AccessToken, creds.AccessTokenSecret} {
					if strings.Contains(fmt.Sprint(err)+string(visible), secret) {
						t.Fatal("private diagnostic value leaked")
					}
				}
			}
			for _, r := range requests {
				if tc.operation == "report" {
					if r.method != "GET" || r.path != "/relatorio/"+quotaCapability+"/" || len(r.body) != 0 {
						t.Fatal("report request differed from the fixed contract")
					}
					for _, h := range []string{"Authorization", "X-Consumer-Key", "X-Consumer-Secret", "X-Access-Token", "X-Access-Token-Secret"} {
						if r.headers.Get(h) != "" {
							t.Fatal("public report forwarded credentials")
						}
					}
				} else if tc.operation == "mutation" && (r.method != "POST" || !bytes.Equal(r.body, []byte(`{"cnae":null}`))) {
					t.Fatal("typed mutation control changed")
				}
			}
			t.Logf("quota case=%s requests=%d kind=%s outcome=%s", tc.name, len(requests), tc.kind, tc.outcome)
		})
	}
}
