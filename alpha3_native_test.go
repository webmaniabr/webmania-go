package webmania_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/webmaniabr/webmania-go"
	filemodels "github.com/webmaniabr/webmania-go/model/api1files"
	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	"github.com/webmaniabr/webmania-go/value"
)

func TestAlpha3NativeTrailerRecovery(t *testing.T) {
	for _, mode := range []string{"complete-distinct", "complete-collision", "truncated", "rejected", "trailer-budget", "body-budget", "interim-budget", "interim-truncated", "framing-rejected"} {
		t.Run(mode, func(t *testing.T) {
			capability := "F00DBABE-0000-4000-8000-00000000CAFE"
			selector := uuid
			if mode == "complete-collision" {
				selector = capability
			}
			var requests atomic.Int64
			u, stop := rawServer(t, func(conn net.Conn, _ *http.Request) {
				if mode == "interim-budget" || mode == "interim-truncated" {
					n := 1
					if mode == "interim-budget" {
						n = 9
					}
					for i := 0; i < n; i++ {
						io.WriteString(conn, "HTTP/1.1 103 Info\r\nX-Correlation-Id: public-correlation\r\n\r\n")
					}
					return
				}
				if mode == "framing-rejected" {
					io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\nTransfer-Encoding: chunked\r\n\r\n")
					return
				}
				body := `{"error":"` + strings.ToLower(capability) + `"}`
				trailer := "Location: https://invalid.test/" + capability + "\r\n\r\n"
				if mode == "truncated" {
					trailer = "X-Filler: partial\r\n"
				}
				if mode == "rejected" {
					trailer = "bad trailer\r\n" + trailer
				}
				if mode == "trailer-budget" {
					trailer = "X-Filler: " + strings.Repeat("x", 300) + "\r\n" + trailer
				}
				io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nX-Correlation-Id: public-correlation\r\nTransfer-Encoding: chunked\r\nTrailer: Location\r\n\r\n"+fmt.Sprintf("%x\r\n%s\r\n0\r\n", len(body), body)+trailer)
			}, &requests)
			defer stop()
			cfg := sdk.Config{BaseURL: u}
			if mode == "trailer-budget" {
				cfg.MaxHeaderBytes = 256
			}
			if mode == "body-budget" {
				cfg.MaxResponseBytes = 16
			}
			c, e := sdk.NewNfeClient(company, cfg)
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.CancelDocument(context.Background(), cancelParams(selector))
			var er *sdk.Error
			if !errors.As(e, &er) {
				t.Fatalf("structured failure required: %v", e)
			}
			b, _ := json.Marshal(er)
			t.Logf("MODE=%s REQUESTS=%d ERROR=%s", mode, requests.Load(), b)
			if requests.Load() != 1 || er.Outcome != "unknown" {
				t.Fatal("effect/request metadata lost")
			}
			if mode == "complete-distinct" {
				if er.CorrelationID != "public-correlation" {
					t.Fatal("complete safe correlation lost")
				}
				if er.Reference == nil || er.Reference.Reveal()["uuid"] != uuid {
					t.Fatal("complete safe trailer lost distinct recovery")
				}
				if strings.Contains(string(b), strings.ToLower(capability)) {
					t.Fatal("capability leaked")
				}
			} else if er.Reference != nil {
				t.Fatal("uncertain/colliding metadata retained recovery")
			}
			if !strings.HasPrefix(mode, "complete-") && er.CorrelationID != "" {
				t.Fatal("observation gap exported potentially colliding correlation")
			}
		})
	}
}
func TestAlpha3UTF8BeforePublicSerialization(t *testing.T) {
	var n atomic.Int64
	s, cfg := reviewServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, "<nfeProc/>")
	})
	_ = s
	files, _ := sdk.NewNfeFilesClient(&company, cfg)
	_, e := files.DownloadXml(context.Background(), sdk.NfeDownloadXmlParams{Identifier: strings.Repeat("a", 35) + string([]byte{0xff}), Auth: sdk.CompanyFileAuth(), Query: filemodels.NfeDownloadXmlQuery{}})
	var er *sdk.Error
	if !errors.As(e, &er) || er.Kind != "validation" || n.Load() != 0 {
		t.Fatal("invalid Go path string passed lossy serialization")
	}
	c, _ := sdk.NewNfeClient(company, cfg)
	for _, body := range []fiscal.FiscalUpdateCompanyBody{{RazaoSocial: value.Some(string([]byte{0xff}))}, {Extras: map[string]json.RawMessage{string([]byte{0xff}): json.RawMessage(`"valid"`)}}, {Extras: map[string]json.RawMessage{"valid": json.RawMessage(`"\ud800"`)}}} {
		_, e := c.UpdateCompany(context.Background(), sdk.FiscalUpdateCompanyParams{Body: body})
		if !errors.As(e, &er) || er.Kind != "validation" || n.Load() != 0 {
			t.Fatal("invalid body text reached network")
		}
	}
	_, e = files.DownloadXml(context.Background(), sdk.NfeDownloadXmlParams{Identifier: strings.Repeat("é", 36), Auth: sdk.CompanyFileAuth()})
	if e != nil || n.Load() != 1 {
		t.Fatal("valid Unicode path refused")
	}
}
