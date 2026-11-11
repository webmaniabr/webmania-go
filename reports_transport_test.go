package webmania_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/webmaniabr/webmania-go"
)

const reportUUID = "00000000-0000-4000-8000-000000000123"

// FILE-07: only the legacy report operation accepts text/xml, classified by its bytes.
func TestFILE07LegacyReportTextXML(t *testing.T) {
	workbook := reviewZip(t, reviewWorkbook())
	latin1 := []byte("numero;destinatario\r\n1;Jo\xe3o\r\n")
	cases := []struct {
		name  string
		data  []byte
		mime  string
		valid bool
	}{
		{"csv-utf8", []byte("uuid,status\r\n" + uuid + ",emitido\r\n"), "text/csv", true},
		{"csv-latin1", latin1, "text/csv", true},
		{"xlsx", workbook, sheetMIME, true},
		{"real-xml", []byte(`<?xml version="1.0"?><nfeProc><NFe/></nfeProc>`), "", false},
		{"html", []byte("<!doctype html><html></html>"), "", false},
		{"json", []byte(`  {"error":"x"}`), "", false},
		{"utf16-html", []byte("\xff\xfe<\x00h\x00/\x00>\x00"), "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int64
			_, cfg := reviewServer(t, false, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/xml")
				w.Write(test.data)
			})
			client, _ := sdk.NewNfeFilesClient(nil, cfg)
			file, e := client.DownloadReport(context.Background(), sdk.FiscalDownloadReportParams{Uuid: reportUUID, Auth: sdk.PublicReportAuth()})
			if test.valid {
				if e != nil || !bytes.Equal(file.Data, test.data) || file.MediaType != test.mime {
					t.Fatalf("report labeled text/xml failed or changed: %v %q", e, file.MediaType)
				}
			} else {
				er := mustError(t, e, "protocol")
				if er.Outcome != "unknown" {
					t.Error("a possibly quota-consuming download lost its uncertain outcome")
				}
			}
			if requests.Load() != 1 {
				t.Error("report replayed")
			}
		})
	}
	// No other file operation gains the tolerance.
	_, cfg := reviewServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.Write(latin1)
	})
	client, _ := sdk.NewNfeFilesClient(&company, cfg)
	_, e := client.DownloadXml(context.Background(), sdk.NfeDownloadXmlParams{Identifier: uuid, Auth: sdk.CompanyFileAuth()})
	mustError(t, e, "protocol")
}

// POLL-04 / fiscalDownloadReportV2: explicit access mode, declared formats must match the bytes.
func TestReportV2Downloads(t *testing.T) {
	var seen []http.Header
	var body []byte
	mime := "text/csv"
	_, cfg := reviewServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relatorios/"+reportUUID {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		seen = append(seen, r.Header.Clone())
		w.Header().Set("Content-Type", mime)
		w.Write(body)
	})
	client, e := sdk.NewReportFilesClient(&company, cfg)
	if e != nil {
		t.Fatal(e)
	}
	body = []byte("id,total\n1,2\n")
	for _, auth := range []sdk.FileAuth{sdk.AnonymousFileAuth(), sdk.CompanyFileAuth()} {
		file, e := client.DownloadReportV2(context.Background(), sdk.FiscalDownloadReportV2Params{Uuid: reportUUID, Auth: auth})
		if e != nil || !bytes.Equal(file.Data, body) {
			t.Fatalf("report download failed: %v", e)
		}
	}
	if seen[0].Get("X-Consumer-Key") != "" || seen[0].Get("Authorization") != "" {
		t.Error("anonymous report download sent credentials")
	}
	if seen[1].Get("X-Consumer-Key") != company.ConsumerKey {
		t.Error("company report download lost its explicit headers")
	}
	if _, e := client.DownloadReportV2(context.Background(), sdk.FiscalDownloadReportV2Params{Uuid: reportUUID, Auth: sdk.IssuerFileAuth()}); e == nil {
		t.Error("Bearer access was accepted on a client without a Bearer")
	}
	// ReportCompanyBearer: the company-scoped API2 Bearer is reachable through its own client.
	issuer := sdk.IssuerCredentials{Token: "report-bearer-value"}
	bearer, e := sdk.NewIssuerReportFilesClient(issuer, cfg)
	if e != nil {
		t.Fatal(e)
	}
	seen = nil
	for _, auth := range []sdk.FileAuth{sdk.IssuerFileAuth(), sdk.AnonymousFileAuth()} {
		if file, e := bearer.DownloadReportV2(context.Background(), sdk.FiscalDownloadReportV2Params{Uuid: reportUUID, Auth: auth}); e != nil || !bytes.Equal(file.Data, body) {
			t.Fatalf("Bearer report download failed: %v", e)
		}
	}
	if seen[0].Get("Authorization") != "Bearer "+issuer.Token || seen[0].Get("X-Consumer-Key") != "" {
		t.Error("Bearer report download did not send exactly the company Bearer")
	}
	if seen[1].Get("Authorization") != "" {
		t.Error("anonymous report download sent the Bearer")
	}
	if _, e := bearer.DownloadReportV2(context.Background(), sdk.FiscalDownloadReportV2Params{Uuid: reportUUID, Auth: sdk.CompanyFileAuth()}); e == nil {
		t.Error("company access was accepted on a client without company credentials")
	}
	if len(seen) != 2 {
		t.Error("a refused access mode reached the network")
	}
	if _, e := sdk.NewIssuerReportFilesClient(sdk.IssuerCredentials{}, cfg); e == nil {
		t.Error("an empty Bearer was accepted")
	}
	formats := []struct {
		mime  string
		data  []byte
		valid bool
	}{
		{sheetMIME, reviewZip(t, reviewWorkbook()), true},
		{"text/html", []byte("<html><body>r</body></html>"), true},
		{"application/xml", []byte("<relatorio/>"), true},
		{"application/octet-stream", []byte{1, 2, 3}, true},
		{"application/zip", []byte("not a zip"), false},
		{"application/pdf", []byte("%PDX"), false},
		{"text/csv", []byte("<x/>"), false},
	}
	for _, f := range formats {
		mime, body = f.mime, f.data
		file, e := client.DownloadReportV2(context.Background(), sdk.FiscalDownloadReportV2Params{Uuid: reportUUID, Auth: sdk.AnonymousFileAuth()})
		if f.valid && (e != nil || file.MediaType != f.mime || !bytes.Equal(file.Data, f.data)) {
			t.Errorf("%s: declared report format refused: %v", f.mime, e)
		}
		if !f.valid {
			mustError(t, e, "protocol")
		}
	}
	mime, body = "application/json", []byte(`{"error":"processando"}`)
	_, e = client.DownloadReportV2(context.Background(), sdk.FiscalDownloadReportV2Params{Uuid: reportUUID, Auth: sdk.AnonymousFileAuth()})
	mustError(t, e, "business")
}

// HTTP-05: chunk-size lines are bounded per line, not against the header budget. The lines are
// plain sizes: net/http itself refuses chunk extensions whose overhead dwarfs the data.
func TestHTTP05ChunkLinesHaveTheirOwnBudget(t *testing.T) {
	xml := []byte(`<?xml version="1.0"?><!-- ` + strings.Repeat("synthetic ", 2400) + `--><nfeProc><NFe/></nfeProc>`)
	serve := func(wire []byte) (string, *atomic.Int64, func()) {
		var count atomic.Int64
		url, stop := rawServer(t, func(c net.Conn, r *http.Request) {
			fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Type: text/xml\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
			c.Write(wire)
		}, &count)
		return url, &count, stop
	}
	var many bytes.Buffer
	for _, b := range xml {
		many.WriteString("1\r\n")
		many.WriteByte(b)
		many.WriteString("\r\n")
	}
	many.WriteString("0\r\n\r\n")
	if many.Len() <= 64<<10 {
		t.Fatal("fixture must exceed one 64 KiB header budget")
	}
	url, count, stop := serve(many.Bytes())
	defer stop()
	client, _ := sdk.NewNfeFilesClient(&company, sdk.Config{BaseURL: url})
	file, e := client.DownloadXml(context.Background(), sdk.NfeDownloadXmlParams{Identifier: uuid, Auth: sdk.CompanyFileAuth()})
	if e != nil || !bytes.Equal(file.Data, xml) {
		t.Fatalf("many-chunk body was not delivered byte-exact: %v", e)
	}
	if count.Load() != 1 {
		t.Error("request repeated")
	}
	url2, count2, stop2 := serve([]byte("1;" + strings.Repeat("x", 16<<10) + "\r\nA\r\n0\r\n\r\n"))
	defer stop2()
	client2, _ := sdk.NewNfeFilesClient(&company, sdk.Config{BaseURL: url2})
	_, e = client2.DownloadXml(context.Background(), sdk.NfeDownloadXmlParams{Identifier: uuid, Auth: sdk.CompanyFileAuth()})
	mustError(t, e, "protocol")
	if count2.Load() != 1 {
		t.Error("request repeated")
	}
}

// CANCEL-05: a sent mutation whose complete 2xx headers arrive but whose body never completes.
func TestCANCEL05StalledSuccessfulMutationIsUnknown(t *testing.T) {
	var count atomic.Int64
	release := make(chan struct{})
	url, stop := rawServer(t, func(c net.Conn, r *http.Request) {
		fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 64\r\n\r\n{\"")
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
	}, &count)
	defer stop()
	defer close(release)
	c := mustNfe(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, e := c.CancelDocument(ctx, cancelParams(uuid))
	var er *sdk.Error
	if e == nil || !asError(e, &er) || er.Outcome != "unknown" || er.Reference == nil {
		t.Fatalf("stalled successful mutation is not unknown: %#v", e)
	}
	if count.Load() != 1 {
		t.Error("mutation replayed")
	}
}

func asError(e error, target **sdk.Error) bool {
	v, ok := e.(*sdk.Error)
	if ok {
		*target = v
	}
	return ok
}
