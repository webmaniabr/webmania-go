package webmania

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"
const xmlnsNamespace = "http://www.w3.org/2000/xmlns/"

var xmlEncoding = regexp.MustCompile(`(?i)(encoding\s*=\s*["'])utf-16(?:le|be)?(["'])`)

func xmlText(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		var order binary.ByteOrder = binary.BigEndian
		if data[0] == 0xff {
			order = binary.LittleEndian
		}
		data = data[2:]
		if len(data)%2 != 0 {
			return nil, newError("protocol")
		}
		runes := make([]rune, 0, len(data)/2)
		for p := 0; p < len(data); p += 2 {
			u := order.Uint16(data[p : p+2])
			r := rune(u)
			if u >= 0xd800 && u <= 0xdbff {
				if p+4 > len(data) {
					return nil, newError("protocol")
				}
				v := order.Uint16(data[p+2 : p+4])
				if v < 0xdc00 || v > 0xdfff {
					return nil, newError("protocol")
				}
				r = utf16.DecodeRune(r, rune(v))
				p += 2
			} else if u >= 0xdc00 && u <= 0xdfff {
				return nil, newError("protocol")
			}
			runes = append(runes, r)
		}
		data = []byte(string(runes))
		head := data
		if len(head) > 256 {
			head = head[:256]
		}
		if bytes.Contains(head, []byte("encoding")) && !xmlEncoding.Match(head) {
			return nil, newError("protocol")
		}
		data = xmlEncoding.ReplaceAll(data, []byte("${1}utf-8${2}"))
	}
	if !utf8.Valid(data) {
		return nil, newError("protocol")
	}
	return bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), nil
}

// RawToken plus an explicit namespace stack rejects duplicate expanded
// attributes and unbound prefixes that encoding/xml.Token otherwise accepts.
func inspectXML(data []byte, visit func(xml.StartElement, int) error) error {
	data, e := xmlText(data)
	if e != nil {
		return e
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	scopes := []map[string]string{{"xml": xmlNamespace, "": ""}}
	names := []xml.Name{}
	roots, tokens := 0, 0
	declaration := false
	for {
		tok, e := dec.RawToken()
		if e == io.EOF {
			break
		}
		if e != nil {
			return newError("protocol")
		}
		tokens++
		if tokens > 500000 {
			return newError("response_limit")
		}
		switch x := tok.(type) {
		case xml.Directive:
			return newError("protocol")
		case xml.ProcInst:
			if strings.EqualFold(x.Target, "xml") {
				if x.Target != "xml" || declaration || roots != 0 || tokens != 1 {
					return newError("protocol")
				}
				declaration = true
			}
		case xml.StartElement:
			if len(names) >= 128 || len(x.Attr) > 4096 {
				return newError("response_limit")
			}
			if len(names) == 0 {
				roots++
				if roots != 1 {
					return newError("protocol")
				}
			}
			// Each level owns only its declarations, avoiding quadratic copying
			// when hostile nested elements introduce many namespace bindings.
			scope := map[string]string{}
			rawAttrs := map[xml.Name]bool{}
			for _, a := range x.Attr {
				if rawAttrs[a.Name] {
					return newError("protocol")
				}
				rawAttrs[a.Name] = true
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					prefix := a.Name.Local
					if a.Name.Space == "" {
						prefix = ""
					}
					if prefix == "xmlns" || prefix == "xml" && a.Value != xmlNamespace || prefix != "xml" && a.Value == xmlNamespace || a.Value == xmlnsNamespace || prefix != "" && a.Value == "" {
						return newError("protocol")
					}
					scope[prefix] = a.Value
				}
			}
			expand := func(n xml.Name, attribute bool) (xml.Name, bool) {
				if n.Space == "" && attribute {
					return n, true
				}
				ns, ok := scope[n.Space]
				if !ok {
					for i := len(scopes) - 1; i >= 0; i-- {
						if ns, ok = scopes[i][n.Space]; ok {
							break
						}
					}
				}
				if !ok {
					return n, false
				}
				return xml.Name{Space: ns, Local: n.Local}, true
			}
			name, ok := expand(x.Name, false)
			if !ok {
				return newError("protocol")
			}
			attrs := []xml.Attr{}
			seen := map[xml.Name]bool{}
			for _, a := range x.Attr {
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					continue
				}
				n, ok := expand(a.Name, true)
				if !ok || seen[n] {
					return newError("protocol")
				}
				seen[n] = true
				attrs = append(attrs, xml.Attr{Name: n, Value: a.Value})
			}
			if visit != nil {
				if e := visit(xml.StartElement{Name: name, Attr: attrs}, len(names)); e != nil {
					return e
				}
			}
			names = append(names, x.Name)
			scopes = append(scopes, scope)
		case xml.EndElement:
			if len(names) == 0 || names[len(names)-1] != x.Name {
				return newError("protocol")
			}
			names = names[:len(names)-1]
			scopes = scopes[:len(scopes)-1]
		case xml.CharData:
			if len(names) == 0 && strings.TrimSpace(string(x)) != "" {
				return newError("protocol")
			}
		}
	}
	if roots != 1 || len(names) != 0 {
		return newError("protocol")
	}
	return nil
}
