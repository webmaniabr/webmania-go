package webmania_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/webmaniabr/webmania-go"
)

// Data are attributed byte-for-byte in tests/fixtures/file-structure/ATTRIBUTION.
func TestAlpha3StructuralFiles(t *testing.T) {
	var cases []struct {
		ID       string `json:"id"`
		File     string `json:"file"`
		Format   string `json:"format"`
		Expected string `json:"expected"`
	}
	b, e := os.ReadFile("tests/fixtures/file-structure/cases.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, secure := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.ID+map[bool]string{true: "/TLS", false: "/HTTP"}[secure], func(t *testing.T) {
				data, e := os.ReadFile("tests/fixtures/file-structure/" + tc.File)
				if e != nil {
					t.Fatal(e)
				}
				mime := "application/pdf"
				if tc.Format != "pdf" {
					mime = sheetMIME
				}
				var count atomic.Int64
				_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
					w.Header().Set("Content-Type", mime)
					w.Write(data)
				})
				files, e := sdk.NewNfeFilesClient(&company, cfg)
				if e != nil {
					t.Fatal(e)
				}
				r, e := files.DownloadReport(context.Background(), sdk.FiscalDownloadReportParams{Uuid: uuid, Auth: sdk.PublicReportAuth()})
				t.Logf("CASE=%s BYTES=%d REQUESTS=%d EXPECT=%s ERROR=%v", tc.ID, len(data), count.Load(), tc.Expected, e)
				if count.Load() != 1 {
					t.Fatal("unexpected dispatch count")
				}
				if (e == nil) != (tc.Expected != "reject") {
					t.Fatalf("structural readiness mismatch: %v", e)
				}
				if e == nil && (!bytes.Equal(r.Data, data) || r.Protected != (tc.Expected == "ProtectedPdf")) {
					t.Fatal("bytes/protection changed")
				}
			})
		}
	}
}

// Worksheet identity is an unsigned numeric value, including lexical aliases.
// Each sheet keeps a distinct name/r:id and all eight share one valid target.
func TestAlpha3WorksheetNumericIdentity(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			ids   []string
			ready bool
		}{
			{"distinct_1_to_8", []string{"1", "2", "3", "4", "5", "6", "7", "8"}, true},
			{"duplicate_1_01", []string{"1", "01", "3", "4", "5", "6", "7", "8"}, false},
			{"duplicate_1_1", []string{"1", "1", "3", "4", "5", "6", "7", "8"}, false},
			{"uint32_max", []string{"1", "2", "3", "4", "5", "6", "7", "4294967295"}, true},
			{"uint32_overflow", []string{"1", "2", "3", "4", "5", "6", "7", "4294967296"}, false},
		} {
			t.Run(fmt.Sprintf("TLS=%t/%s", secure, tc.name), func(t *testing.T) {
				entries := reviewWorkbook()
				var sheets, relations strings.Builder
				sheets.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
				relations.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
				for i, id := range tc.ids {
					fmt.Fprintf(&sheets, `<sheet name="NumericSheet%d" sheetId="%s" r:id="numeric%d"/>`, i+1, id, i+1)
					fmt.Fprintf(&relations, `<Relationship Id="numeric%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>`, i+1)
				}
				sheets.WriteString(`</sheets></workbook>`)
				relations.WriteString(`</Relationships>`)
				entries["xl/workbook.xml"] = sheets.String()
				entries["xl/_rels/workbook.xml.rels"] = relations.String()
				data := reviewZip(t, entries)
				var count atomic.Int64
				_, cfg := reviewServer(t, secure, func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
					w.Header().Set("Content-Type", sheetMIME)
					w.Write(data)
				})
				files, e := sdk.NewNfeFilesClient(&company, cfg)
				if e != nil {
					t.Fatal(e)
				}
				r, e := files.DownloadReport(context.Background(), sdk.FiscalDownloadReportParams{Uuid: uuid, Auth: sdk.PublicReportAuth()})
				t.Logf("SHEET_IDS=%v DISTINCT_NAMES=8 DISTINCT_RIDS=8 TARGETS=1 REQUESTS=%d READY=%t ERROR=%v", tc.ids, count.Load(), e == nil, e)
				if count.Load() != 1 || (e == nil) != tc.ready {
					t.Fatal("numeric worksheet identity/readiness mismatch")
				}
				if e == nil && !bytes.Equal(r.Data, data) {
					t.Fatal("ready bytes changed")
				}
			})
		}
	}
}
