package webmania

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config approves one service base URL and optionally a private CA. No proxy, redirects or retries.
type Config struct {
	BaseURL          string
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRequestBytes  int64
	MaxHeaderBytes   int
	RootCAs          *x509.CertPool
}
type transport struct {
	base                        *url.URL
	timeout                     time.Duration
	responseLimit, requestLimit int64
	headerLimit                 int
	roots                       *x509.CertPool
}
type wireResponse struct {
	status            int
	headers           http.Header
	raw               []byte
	sent              bool
	headersComplete   bool
	incompleteHeaders bool
	locations         []string
}

func newTransport(cfg Config, defaultURL string) (*transport, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultURL
	}
	u, e := url.Parse(cfg.BaseURL)
	if e != nil || u.Opaque != "" || u.User != nil || !validHost(u.Hostname()) || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(cfg.BaseURL, "\\\r\n\t ") || strings.Contains(u.EscapedPath(), "%") {
		return nil, newError("configuration")
	}
	if !validAuthority(u.Host) {
		return nil, newError("configuration")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, newError("configuration")
		}
	}
	for _, s := range strings.Split(u.Path, "/") {
		if s == "." || s == ".." {
			return nil, newError("configuration")
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = 32 << 20
	}
	if cfg.MaxRequestBytes == 0 {
		cfg.MaxRequestBytes = 8 << 20
	}
	if cfg.MaxHeaderBytes == 0 {
		cfg.MaxHeaderBytes = 64 << 10
	}
	if cfg.Timeout < 0 || cfg.MaxResponseBytes < 1 || cfg.MaxRequestBytes < 1 || cfg.MaxHeaderBytes < 128 {
		return nil, newError("configuration")
	}
	var roots *x509.CertPool
	if cfg.RootCAs != nil {
		roots = cfg.RootCAs.Clone()
	}
	return &transport{u, cfg.Timeout, cfg.MaxResponseBytes, cfg.MaxRequestBytes, cfg.MaxHeaderBytes, roots}, nil
}
func (t *transport) send(ctx context.Context, verb, path string, body []byte, headers http.Header) (out wireResponse, err error) {
	if e := ctx.Err(); e != nil {
		return out, canceledError(e)
	}
	if int64(len(body)) > t.requestLimit {
		return out, newError("request_limit")
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	port := t.base.Port()
	if port == "" {
		if t.base.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	conn, e := dialOwned(ctx, t.base.Hostname(), port)
	if e != nil {
		return out, transportContextError(ctx)
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	defer func() { close(stop); <-stopped }()
	go func(closeConn net.Conn) {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = closeConn.Close()
		case <-stop:
		}
	}(conn)
	if t.base.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.base.Hostname(), RootCAs: t.roots, NextProtos: []string{"http/1.1"}})
		if e = tc.HandshakeContext(ctx); e != nil {
			return out, transportContextError(ctx)
		}
		conn = tc
	}
	u := *t.base
	uri, e := url.ParseRequestURI(path)
	if e != nil {
		return out, newError("validation")
	}
	u.Path = uri.Path
	u.RawPath = uri.RawPath
	u.RawQuery = uri.RawQuery
	req, e := http.NewRequestWithContext(ctx, verb, u.String(), bytes.NewReader(body))
	if e != nil {
		return out, newError("configuration")
	}
	req.Header = headers.Clone()
	req.Close = true
	if e = ctx.Err(); e != nil {
		return out, canceledError(e)
	}
	out.sent = true
	// Request.Write uses precisely this fresh socket; http.Transport and its retry loop are never used.
	if e = req.Write(conn); e != nil {
		return out, transportContextError(ctx)
	}
	br := bufio.NewReader(conn)
	total := 0
	for interim := 0; interim < 9; interim++ {
		var block bytes.Buffer
		lines := 0
		seen := map[string]int{}
		hasTE, hasCL := false, false
		for {
			var lineBuilder strings.Builder
			var e error
			for {
				fragment, readErr := br.ReadSlice('\n')
				total += len(fragment)
				if total > t.headerLimit {
					out.incompleteHeaders = true
					return out, newError("header_limit")
				}
				lineBuilder.Write(fragment)
				if readErr == bufio.ErrBufferFull {
					continue
				}
				e = readErr
				break
			}
			line := lineBuilder.String()
			block.WriteString(line)
			if e != nil {
				out.incompleteHeaders = block.Len() > 0 || interim > 0
				return out, transportContextError(ctx)
			}
			if lines > 0 && line != "\r\n" {
				if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
					out.incompleteHeaders = true
					return out, newError("protocol")
				}
				key, val, ok := strings.Cut(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), ":")
				if !ok {
					out.incompleteHeaders = true
					return out, newError("protocol")
				}
				key = strings.ToLower(key)
				seen[key]++
				if key == "location" {
					out.locations = append(out.locations, textproto.TrimString(val))
				}
				if key == "content-length" {
					hasCL = true
				}
				if key == "transfer-encoding" {
					hasTE = true
				}
			}
			lines++
			if lines > 256 {
				out.incompleteHeaders = true
				return out, newError("header_limit")
			}
			if line == "\r\n" {
				break
			}
		}
		out.headersComplete = true
		if hasCL && hasTE || seen["content-length"] > 1 || seen["content-type"] > 1 {
			out.incompleteHeaders = true
			return out, newError("protocol")
		}
		observer := &chunkObservation{reader: br, remaining: t.headerLimit - total, state: chunkSize}
		var bodyWire io.Reader = br
		if hasTE {
			bodyWire = observer
		}
		r, e := http.ReadResponse(bufio.NewReader(io.MultiReader(bytes.NewReader(block.Bytes()), bodyWire)), req)
		if e != nil {
			out.incompleteHeaders = true
			return out, newError("protocol")
		}
		out.status = r.StatusCode
		out.headers = r.Header
		if r.StatusCode >= 100 && r.StatusCode < 200 {
			if r.StatusCode == 101 {
				out.incompleteHeaders = true
				return out, newError("protocol")
			}
			if interim == 8 {
				out.incompleteHeaders = true
				return out, newError("header_limit")
			}
			continue
		}
		data, e := io.ReadAll(io.LimitReader(r.Body, t.responseLimit+1))
		if e != nil || int64(len(data)) > t.responseLimit {
			_ = conn.Close()
		}
		_ = r.Body.Close()
		out.raw = data
		if hasTE {
			out.locations = append(out.locations, observer.locations...)
			if !observer.complete || e != nil || int64(len(data)) > t.responseLimit {
				out.incompleteHeaders = true
			}
			if observer.exceeded {
				// Trailers overran the header budget; anything else is an oversized chunk-size line.
				if observer.state == chunkTrailers {
					return out, newError("header_limit")
				}
				return out, newError("protocol")
			}
			if e == nil {
				for _, loc := range r.Trailer.Values("Location") {
					out.locations = append(out.locations, loc)
				}
			}
		}
		if int64(len(data)) > t.responseLimit {
			return out, newError("response_limit")
		}
		if e != nil {
			return out, transportContextError(ctx)
		}
		return out, nil
	}
	return out, newError("protocol")
}
func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case interface{ String() string }:
		return x.String(), true
	}
	return "", false
}

func transportContextError(ctx context.Context) *Error {
	e := ctx.Err()
	if e == nil {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			e = context.DeadlineExceeded
		}
	}
	return canceledError(e)
}

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return host != ""
}

// net/url accepts an empty or out of range port; client construction does not.
func validAuthority(authority string) bool {
	if strings.HasSuffix(authority, ":") {
		return false
	}
	u, e := url.Parse("https://" + authority)
	if e != nil || u.Host != authority || u.User != nil || u.Path != "" {
		return false
	}
	if strings.HasPrefix(authority, "[") {
		i := strings.IndexByte(authority, ']')
		if i < 0 || net.ParseIP(authority[1:i]) == nil {
			return false
		}
		if rest := authority[i+1:]; rest != "" && !strings.HasPrefix(rest, ":") {
			return false
		}
	} else if strings.ContainsAny(authority, "[]") || strings.Count(authority, ":") > 1 {
		return false
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
