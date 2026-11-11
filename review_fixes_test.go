package webmania_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/webmaniabr/webmania-go"
	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	"github.com/webmaniabr/webmania-go/value"
)

const sheetMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

func reviewServer(t *testing.T, secure bool, handler http.HandlerFunc) (*httptest.Server, sdk.Config) {
	t.Helper()
	var server *httptest.Server
	if secure {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	cfg := sdk.Config{BaseURL: server.URL}
	if secure {
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		cfg.RootCAs = roots
	}
	return server, cfg
}
func reviewZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	keys := []string{}
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		file, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		io.WriteString(file, entries[name])
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return buf.Bytes()
}
func reviewWorkbook() map[string]string {
	return map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Report" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Report</t></is></c></row></sheetData></worksheet>`,
	}
}
func TestAlpha2ExactCaseProperties(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) {
			for _, body := range []string{`{"status":"online","STATUS":"offline"}`, `{"STATUS":"offline","status":"online"}`, `{"status":"online","sTaTuS":{"future":true}}`, `{"sTaTuS":{"future":true},"status":"online"}`, `{"status":"online","ERROR":{"future":true}}`} {
				t.Run(body, func(t *testing.T) {
					var count atomic.Int64
					_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, body)
					})
					client, e := sdk.NewNfeClient(company, cfg)
					if e != nil {
						t.Fatal(e)
					}
					r, e := client.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
					if e != nil {
						t.Fatal(e)
					}
					status, set := r.Value.Status.Get()
					if !set || status != "online" {
						t.Fatalf("known status overwritten: %s", status)
					}
					var wire map[string]json.RawMessage
					json.Unmarshal([]byte(body), &wire)
					for key, raw := range wire {
						if key == "status" {
							continue
						}
						if !bytes.Equal(r.Value.Extras[key], raw) {
							t.Error("case-distinct extension lost")
						}
					}
					if count.Load() != 1 {
						t.Error("call replayed")
					}
				})
			}
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprint("UUID-reverse=", reverse), func(t *testing.T) {
					other := "b7654321-4321-4321-8321-cba987654321"
					a := `"uuid":"` + uuid + `"`
					b := `"UUID":"` + other + `"`
					if reverse {
						a, b = b, a
					}
					body := "{" + a + "," + b + `,"status":1,"endereco":{"estado":"SP","ESTADO":{"future":true}}}`
					_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, body)
					})
					client, _ := sdk.NewMonitorClient(sdk.MonitorCredentials{Token: "synthetic-monitor"}, cfg)
					result, e := client.GetCompany(context.Background(), sdk.MonitorGetCompanyParams{Company: uuid})
					if e != nil {
						t.Fatal(e)
					}
					id, _ := result.Value.Uuid.Get()
					if id != uuid || string(result.Value.Extras["UUID"]) != `"`+other+`"` {
						t.Fatal("UUID projection differs from validated key")
					}
					address, _ := result.Value.Endereco.Get()
					state, _ := address.Estado.Get()
					if state != "SP" || string(address.Extras["ESTADO"]) != `{"future":true}` {
						t.Fatal("nested case-distinct field overwrote known field")
					}
				})
			}
		})
	}
}
func TestAlpha2ReportFormats(t *testing.T) {
	validWorkbook := reviewZip(t, reviewWorkbook())
	absoluteWorkbook := reviewWorkbook()
	absoluteWorkbook["xl/_rels/workbook.xml.rels"] = strings.ReplaceAll(absoluteWorkbook["xl/_rels/workbook.xml.rels"], `Target="worksheets/sheet1.xml"`, `Target="/xl/worksheets/sheet1.xml"`)
	badWorkbook := reviewWorkbook()
	badWorkbook["xl/worksheets/sheet1.xml"] = `<!DOCTYPE worksheet SYSTEM "https://untrusted.invalid/entity"><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`
	missingWorkbook := reviewWorkbook()
	delete(missingWorkbook, "xl/workbook.xml")
	oversized := append([]byte(nil), validWorkbook...)
	central := bytes.Index(oversized, []byte("PK\x01\x02"))
	if central < 0 {
		t.Fatal("fixture missing central directory")
	}
	binary.LittleEndian.PutUint32(oversized[central+24:], 65<<20)
	cases := []struct {
		name, mime string
		data       []byte
		valid      bool
		kind       string
	}{
		{"csv", "text/csv", []byte("uuid,status\r\n" + uuid + ",emitido\r\n"), true, ""}, {"csv-bom-quotes", "text/csv", []byte("\xef\xbb\xbfID,total\r\n\"one,part\",123.4500\r\n"), true, ""}, {"csv-semicolon", "text/csv", []byte("id;total\n1;123,45\n"), true, ""}, {"csv-tab", "text/csv", []byte("id\ttotal\n1\t123.45\n"), true, ""}, {"csv-single-column", "text/csv", []byte("id\n1\n"), true, ""}, {"csv-bad-quote", "text/csv", []byte("id,value\n1,\"unterminated\n"), false, "protocol"}, {"csv-bad-columns", "text/csv", []byte("id,value\n1\n"), false, "protocol"}, {"csv-json", "text/csv", []byte(`{"error":"not CSV"}`), false, "protocol"}, {"csv-html", "text/csv", []byte("<html>not CSV</html>"), false, "protocol"}, {"csv-pdf", "text/csv", []byte(pdf), false, "protocol"}, {"csv-nul", "text/csv", []byte("id,value\n1,\x00\n"), false, "protocol"},
		{"xlsx", sheetMIME, validWorkbook, true, ""}, {"xlsx-absolute-worksheet-relation", sheetMIME, reviewZip(t, absoluteWorkbook), true, ""}, {"xlsx-generic-zip", sheetMIME, reviewZip(t, map[string]string{"report.csv": "id,total\n1,2\n"}), false, "protocol"}, {"xlsx-bare-xml", sheetMIME, []byte(`<workbook/>`), false, "protocol"}, {"xlsx-missing-workbook", sheetMIME, reviewZip(t, missingWorkbook), false, "protocol"}, {"xlsx-dtd", sheetMIME, reviewZip(t, badWorkbook), false, "protocol"}, {"xlsx-expanded-limit", sheetMIME, oversized, false, "response_limit"},
		{"zip-control", "application/zip", reviewZip(t, map[string]string{"report.csv": "id,total\n1,2\n"}), true, ""}, {"pdf-control", "application/pdf", []byte(pdf), true, ""},
	}
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) {
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					var requests atomic.Int64
					_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						if r.Header.Get("Authorization") != "" || r.Header.Get("X-Consumer-Key") != "" {
							t.Error("public report forwarded credentials")
						}
						if !strings.Contains(r.Header.Get("Accept"), test.mime) {
							t.Error("declared representation missing from Accept")
						}
						w.Header().Set("Content-Type", test.mime)
						w.Write(test.data)
					})
					client, _ := sdk.NewNfeFilesClient(&company, cfg)
					file, e := client.DownloadReport(context.Background(), sdk.FiscalDownloadReportParams{Uuid: uuid, Auth: sdk.PublicReportAuth()})
					if test.valid {
						if e != nil || !bytes.Equal(file.Data, test.data) || file.MediaType != test.mime {
							t.Fatalf("valid report failed or bytes changed: %v", e)
						}
					} else {
						mustError(t, e, test.kind)
					}
					if requests.Load() != 1 {
						t.Error("report replayed")
					}
				})
			}
		})
	}
}
func TestAlpha2BinaryStatusContract(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) {
			for _, status := range []int{200, 201, 202, 206} {
				t.Run(fmt.Sprint(status), func(t *testing.T) {
					var count atomic.Int64
					_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						w.Header().Set("Content-Type", "application/pdf")
						w.WriteHeader(status)
						io.WriteString(w, pdf)
					})
					c, _ := sdk.NewNfeFilesClient(&company, cfg)
					file, e := c.DownloadDanfe(context.Background(), sdk.NfeDownloadDanfeParams{Identifier: uuid, Auth: sdk.CompanyFileAuth()})
					if status == 200 {
						if e != nil || file.Status != 200 {
							t.Fatal(e)
						}
					} else {
						mustError(t, e, "protocol")
					}
					if count.Load() != 1 {
						t.Error("status call replayed")
					}
				})
			}
		})
	}
}
func TestAlpha2ObjectAlternativeExtensions(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) {
			for _, field := range []string{"message", "code", "log"} {
				for _, shape := range []string{`{"new":"shape"}`, `["new","shape"]`, `false`, `123.4500`, `null`} {
					t.Run(field+shape, func(t *testing.T) {
						body := `{"status":"online","` + field + `":` + shape + `}`
						_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, body)
						})
						client, _ := sdk.NewNfeClient(company, cfg)
						result, e := client.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
						if e != nil {
							t.Fatalf("valid success extension rejected: %v", e)
						}
						status, _ := result.Value.Status.Get()
						if status != "online" {
							t.Error("common typed property lost")
						}
						encoded, e := json.Marshal(result.Value)
						if e != nil {
							t.Fatal(e)
						}
						var roundtrip map[string]json.RawMessage
						json.Unmarshal(encoded, &roundtrip)
						if string(roundtrip[field]) != shape {
							t.Fatalf("alternative extension changed: %s", roundtrip[field])
						}
					})
				}
			}
			_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(w, map[string]any{"status": "online", "message": "typed future message"})
			})
			client, _ := sdk.NewNfeClient(company, cfg)
			result, e := client.GetSefazStatus(context.Background(), sdk.NfeGetSefazStatusParams{})
			if e != nil {
				t.Fatal(e)
			}
			message, set := result.Value.Message.Get()
			if !set || message != "typed future message" {
				t.Error("alternative-specific typed field lost")
			}
		})
	}
	common := fiscal.NfeCancelRequest{Uuid: value.Some(uuid), Motivo: value.Some("Typed common cancellation reason")}
	if _, e := json.Marshal(common); e != nil {
		t.Fatal(e)
	}
}
func TestAlpha2ExactIntegralResponses(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) {
			for _, lexeme := range []string{"1", "1.0", "1e0", "10e-1", "2.000", "1.0000000000000000000000000000000000000000001", `"1"`} {
				t.Run(lexeme, func(t *testing.T) {
					body := `{"uuid":"` + uuid + `","status":` + lexeme + `,"future":9007199254740993.1234567890123456789}`
					_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, body)
					})
					client, _ := sdk.NewMonitorClient(sdk.MonitorCredentials{Token: "synthetic-monitor"}, cfg)
					result, e := client.GetCompany(context.Background(), sdk.MonitorGetCompanyParams{Company: uuid})
					if strings.HasPrefix(lexeme, `"`) || strings.Contains(lexeme, "0000001") {
						mustError(t, e, "response_schema")
						return
					}
					if e != nil {
						t.Fatal(e)
					}
					status, set := result.Value.Status.Get()
					want := int64(1)
					if lexeme == "2.000" {
						want = 2
					}
					if !set || status != want {
						t.Fatal("exact integral value changed")
					}
					encoded, e := json.Marshal(result.Value)
					if e != nil {
						t.Fatal(e)
					}
					var raw map[string]json.RawMessage
					json.Unmarshal(encoded, &raw)
					if string(raw["status"]) != lexeme || string(raw["future"]) != "9007199254740993.1234567890123456789" {
						t.Fatalf("raw numeric representation changed: %s", encoded)
					}
				})
			}
		})
	}
}
func TestAlpha2ExactPaginationMetadata(t *testing.T) {
	_, cfg := reviewServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"uuid":"`+uuid+`","status":1.0},{"uuid":"`+uuid+`","status":1e0}],"meta":{"current_page":1e0,"last_page":1.0,"per_page":50.0,"total":2.0}}`)
	})
	client, _ := sdk.NewMonitorClient(sdk.MonitorCredentials{Token: "synthetic-monitor"}, cfg)
	iterator, e := client.Companies(sdk.MonitorListCompaniesParams{}, sdk.PaginationOptions{})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		_, ok, e := iterator.Next(context.Background())
		if !ok || e != nil {
			t.Fatal(e)
		}
	}
	_, ok, e := iterator.Next(context.Background())
	if ok || e != nil || iterator.StopReason() != "complete" {
		t.Fatal("integral notation broke pagination")
	}
}
