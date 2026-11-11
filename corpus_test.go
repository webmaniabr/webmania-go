package webmania_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	sdk "github.com/webmaniabr/webmania-go"
	"github.com/webmaniabr/webmania-go/value"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

var company = sdk.CompanyCredentials{ConsumerKey: "synthetic-company-key", ConsumerSecret: "synthetic-company-secret", AccessToken: "synthetic-company-access", AccessTokenSecret: "synthetic-company-access-secret"}
var resellerCred = sdk.ResellerCredentials{ConsumerKey: "synthetic-reseller-key", ConsumerSecret: "synthetic-reseller-secret", AccessToken: "synthetic-reseller-access", AccessTokenSecret: "synthetic-reseller-access-secret"}

const uuid = "00000000-0000-4000-8000-000000000001"

var pdf = validPDF()

// Coherent classic protected PDF from independent Rust alpha5 fixture, SHA256
// bf33973954954251d6095a7f5281b33a3ddc4b178457ca8bc6fb08f0ecb21c2b.
const protectedPDF = "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] /Resources << >> >>\nendobj\n4 0 obj\n<< /Filter /Standard /V 1 /R 2 /Length 40 /O <0000000000000000000000000000000000000000000000000000000000000000> /U <0000000000000000000000000000000000000000000000000000000000000000> /P -4 >>\nendobj\nxref\n0 5\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \n0000000115 00000 n \n0000000201 00000 n \ntrailer\n<< /Size 5 /Root 1 0 R /Encrypt 4 0 R /ID [<00000000000000000000000000000000><00000000000000000000000000000000>] >>\nstartxref\n407\n%%EOF\n"

func validPDF() string {
	out := "%PDF-1.4\n"
	offsets := []int{0}
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", "<< /Length 0 >>\nstream\n\nendstream"}
	for i, obj := range objects {
		offsets = append(offsets, len(out))
		out += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := len(out)
	out += "xref\n0 5\n0000000000 65535 f \n"
	for _, n := range offsets[1:] {
		out += fmt.Sprintf("%010d 00000 n \n", n)
	}
	out += fmt.Sprintf("trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return out
}

func TestAllOperationsOverHTTPAndTLS(t *testing.T) {
	b, e := os.ReadFile("tests/operations.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Cases []struct {
			Operation, Client string
			HTTPMethod        string `json:"http_method"`
			Path              string
			PathParams        map[string]any `json:"path_params"`
			Query             map[string]any
			Request           any
			HasRequest        bool `json:"has_request"`
			Status            int
			MediaType         string `json:"media_type"`
			Response          any
			Binary, Enabled   bool
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&corpus); e != nil {
		t.Fatal(e)
	}
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) {
			for _, c := range corpus.Cases {
				c := c
				t.Run(c.Operation, func(t *testing.T) {
					var count atomic.Int64
					h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						path := c.Path
						for k, v := range c.PathParams {
							path = strings.ReplaceAll(path, "{"+k+"}", fmt.Sprint(v))
						}
						if r.Method != c.HTTPMethod || r.URL.Path != path {
							t.Errorf("wire route got %s %s", r.Method, r.URL.Path)
						}
						if c.HasRequest {
							raw, _ := io.ReadAll(r.Body)
							v, e := value.Decode(raw, 64)
							if e != nil || !reflect.DeepEqual(v, c.Request) {
								t.Errorf("wire body differs: %v", e)
							}
						}
						switch c.Client {
						case "Nfe", "NfeFiles":
							if c.Operation == "fiscalDownloadReport" {
								if r.Header.Get("X-Consumer-Key") != "" || r.Header.Get("Authorization") != "" {
									t.Error("public capability sent credentials")
								}
							} else if r.Header.Get("X-Consumer-Key") != company.ConsumerKey {
								t.Error("missing company auth")
							}
						case "Reseller":
							if r.Header.Get("X-Consumer-Key") != resellerCred.ConsumerKey {
								t.Error("wrong reseller credentials")
							}
						case "Monitor":
							if r.Header.Get("Authorization") != "Bearer synthetic-monitor" {
								t.Error("wrong monitor auth")
							}
						default:
							if r.Header.Get("Authorization") != "Bearer synthetic-issuer" {
								t.Error("wrong issuer auth")
							}
						}
						w.Header().Set("Content-Type", c.MediaType)
						w.WriteHeader(c.Status)
						if c.Binary {
							switch c.MediaType {
							case "application/octet-stream":
								io.WriteString(w, "synthetic report bytes")
							case "application/pdf":
								io.WriteString(w, pdf)
							case "application/xml", "text/xml":
								io.WriteString(w, "<?xml version=\"1.0\"?><!--synthetic--><nfeProc/>")
							case "application/zip":
								var buf bytes.Buffer
								z := zip.NewWriter(&buf)
								f, _ := z.Create("synthetic.xml")
								io.WriteString(f, "<nfeProc/>")
								_ = z.Close()
								_, _ = w.Write(buf.Bytes())
							default:
								t.Errorf("unsupported fixture MIME %s", c.MediaType)
							}
						} else {
							_ = json.NewEncoder(w).Encode(c.Response)
						}
					})
					var s *httptest.Server
					if secure {
						s = httptest.NewTLSServer(h)
					} else {
						s = httptest.NewServer(h)
					}
					defer s.Close()
					cfg := sdk.Config{BaseURL: s.URL}
					if secure {
						pool := x509.NewCertPool()
						pool.AddCert(s.Certificate())
						cfg.RootCAs = pool
					}
					nfe, e := sdk.NewNfeClient(company, cfg)
					if e != nil {
						t.Fatal(e)
					}
					emitter, _ := sdk.NewEmitterClient(sdk.IssuerCredentials{Token: "synthetic-issuer"}, cfg)
					monitor, _ := sdk.NewMonitorClient(sdk.MonitorCredentials{Token: "synthetic-monitor"}, cfg)
					reseller, _ := sdk.NewResellerClient(resellerCred, cfg)
					nf, _ := sdk.NewNfeFilesClient(&company, cfg)
					ef, _ := sdk.NewEmitterFilesClient(&sdk.IssuerCredentials{Token: "synthetic-issuer"}, cfg)
					rf, _ := sdk.NewReportFilesClient(&company, cfg)
					p := map[string]any{"Query": c.Query, "Body": c.Request}
					for k, v := range c.PathParams {
						p[strings.ToUpper(k[:1])+k[1:]] = v
					}
					b, _ := json.Marshal(p)
					auth := sdk.CompanyFileAuth()
					if c.Client == "EmitterFiles" {
						auth = sdk.IssuerFileAuth()
					}
					if c.Operation == "fiscalDownloadReport" {
						auth = sdk.PublicReportAuth()
					}
					e = dispatch(context.Background(), c.Operation, b, auth, nfe, emitter, monitor, reseller, nf, ef, rf)
					if !c.Enabled {
						if e == nil || count.Load() != 0 {
							t.Fatalf("disabled route dispatched: count %d error %v", count.Load(), e)
						}
						return
					}
					if e != nil {
						t.Fatalf("method failed: %#v", e)
					}
					if count.Load() != 1 {
						t.Fatalf("request count %d", count.Load())
					}
				})
			}
		})
	}
}
