package webmania

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const xlsxMediaType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
const maxExpandedSpreadsheetBytes uint64 = 64 << 20

// inspectCSV checks the record structure only. The bytes are never decoded or re-encoded:
// legacy exports may be Latin-1 (ISO-8859-1) as well as UTF-8 with or without a BOM.
func inspectCSV(data []byte) error {
	text := bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	trimmed := bytes.TrimSpace(text)
	if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("<")) || bytes.HasPrefix(trimmed, []byte("%PDF-")) || bytes.HasPrefix(trimmed, []byte("PK\x03\x04")) {
		return newError("protocol")
	}
	if json.Valid(trimmed) && (trimmed[0] == '{' || trimmed[0] == '[') {
		return newError("protocol")
	}
	for _, c := range text {
		if c < 32 && c != '\r' && c != '\n' && c != '\t' || c == 127 {
			return newError("protocol")
		}
	}
	// Pick a delimiter from the first record, then enforce its column count;
	// a malformed comma report must not become a single-column semicolon report.
	var selected *csv.Reader
	for _, delimiter := range []rune{',', ';', '\t'} {
		reader := csv.NewReader(bytes.NewReader(text))
		reader.Comma = delimiter
		first, e := reader.Read()
		if e != nil {
			continue
		}
		if selected == nil {
			selected = reader
		}
		if len(first) > 1 {
			selected = reader
			break
		}
	}
	if selected == nil {
		return newError("protocol")
	}
	for {
		_, e := selected.Read()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return newError("protocol")
		}
	}

}

const packageTypesNS = "http://schemas.openxmlformats.org/package/2006/content-types"
const packageRelationsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
const sheetNS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
const strictSheetNS = "http://purl.oclc.org/ooxml/spreadsheetml/main"
const officeNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
const strictOfficeNS = "http://purl.oclc.org/ooxml/officeDocument/relationships"
const workbookContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
const worksheetContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"

type workbookRelation struct {
	kind, target string
	external     bool
}

func packageTarget(base, target string) (string, bool) {
	if target == "" || strings.Contains(target, "//") || strings.ContainsAny(target, "\\?#") || strings.Contains(target, "%") {
		return "", false
	}
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "", false
	}
	name := path.Clean(path.Join(base, target))
	if strings.HasPrefix(target, "/") {
		name = path.Clean(strings.TrimPrefix(target, "/"))
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}

// XML parts are parsed once per actual part. Relationship multiplicity cannot
// multiply decompression or parsing work.
func inspectSpreadsheet(data []byte) error {
	archive, protected, e := inspectArchive(data, maxExpandedSpreadsheetBytes)
	if e != nil {
		return e
	}
	if protected {
		return newError("protected_file")
	}
	parts := map[string][]byte{}
	for _, part := range archive.File {
		name := part.Name
		if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || path.Clean(name) != strings.TrimSuffix(name, "/") {
			return newError("protocol")
		}
		r, e := part.Open()
		if e != nil {
			return newError("protocol")
		}
		b, e := io.ReadAll(io.LimitReader(r, int64(part.UncompressedSize64)+1))
		_ = r.Close()
		if e != nil || uint64(len(b)) != part.UncompressedSize64 {
			return newError("protocol")
		}
		parts[name] = b
	}
	attrs := func(node xml.StartElement) map[string]string {
		m := map[string]string{}
		for _, a := range node.Attr {
			if a.Name.Space == "" {
				m[a.Name.Local] = a.Value
			}
		}
		return m
	}
	content := map[string]string{}
	defaults := map[string]string{}
	e = inspectXML(parts["[Content_Types].xml"], func(node xml.StartElement, depth int) error {
		if node.Name.Space != packageTypesNS || depth == 0 && node.Name.Local != "Types" || depth > 1 {
			return newError("protocol")
		}
		a := attrs(node)
		if depth == 1 {
			switch node.Name.Local {
			case "Override":
				name, ok := packageTarget("", a["PartName"])
				if !ok || !strings.HasPrefix(a["PartName"], "/") || a["ContentType"] == "" || content[name] != "" {
					return newError("protocol")
				}
				content[name] = a["ContentType"]
			case "Default":
				if a["Extension"] == "" || a["ContentType"] == "" || defaults[a["Extension"]] != "" {
					return newError("protocol")
				}
				defaults[a["Extension"]] = a["ContentType"]
			default:
				return newError("protocol")
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if content["xl/workbook.xml"] != workbookContentType {
		return newError("protocol")
	}
	relations := func(name, base string) (map[string]workbookRelation, error) {
		kind := content[name]
		if kind == "" {
			kind = defaults[path.Ext(name)[1:]]
		}
		if kind != "application/vnd.openxmlformats-package.relationships+xml" {
			return nil, newError("protocol")
		}
		result := map[string]workbookRelation{}
		e := inspectXML(parts[name], func(node xml.StartElement, depth int) error {
			if node.Name.Space != packageRelationsNS || depth == 0 && node.Name.Local != "Relationships" || depth > 1 || depth == 1 && node.Name.Local != "Relationship" {
				return newError("protocol")
			}
			if depth == 1 {
				a := attrs(node)
				id := a["Id"]
				if _, exists := result[id]; id == "" || exists || a["Type"] == "" {
					return newError("protocol")
				}
				external := a["TargetMode"] == "External"
				if external {
					return newError("protocol")
				}
				if a["TargetMode"] != "" && a["TargetMode"] != "Internal" && !external {
					return newError("protocol")
				}
				target := a["Target"]
				if !external {
					var ok bool
					target, ok = packageTarget(base, target)
					if !ok || parts[target] == nil {
						return newError("protocol")
					}
				}
				result[id] = workbookRelation{a["Type"], target, external}
			}
			return nil
		})
		return result, e
	}
	rootRels, e := relations("_rels/.rels", "")
	if e != nil {
		return e
	}
	office := 0
	for _, r := range rootRels {
		if r.kind == officeNS+"/officeDocument" || r.kind == strictOfficeNS+"/officeDocument" {
			if r.external || r.target != "xl/workbook.xml" {
				return newError("protocol")
			}
			office++
		}
	}
	if office != 1 {
		return newError("protocol")
	}
	rels, e := relations("xl/_rels/workbook.xml.rels", "xl")
	if e != nil {
		return e
	}
	sheets := []string{}
	ids := map[uint32]bool{}
	names := map[string]bool{}
	workbookNS := ""
	docNS := ""
	sheetContainers := 0
	usedRIDs := map[string]bool{}
	ancestors := []xml.Name{}
	e = inspectXML(parts["xl/workbook.xml"], func(node xml.StartElement, depth int) error {
		if depth == 0 {
			workbookNS = node.Name.Space
			docNS = officeNS
			if workbookNS == strictSheetNS {
				docNS = strictOfficeNS
			}
			if node.Name.Local != "workbook" || workbookNS != sheetNS && workbookNS != strictSheetNS {
				return newError("protocol")
			}
		}
		if node.Name.Space != workbookNS {
			return newError("protocol")
		}
		if node.Name.Local == "sheets" {
			if depth != 1 {
				return newError("protocol")
			}
			sheetContainers++
		}
		if node.Name.Local == "sheet" {
			if depth != 2 || len(ancestors) < 2 || ancestors[1].Local != "sheets" {
				return newError("protocol")
			}
			a := attrs(node)
			rid := ""
			for _, v := range node.Attr {
				if v.Name.Local == "id" && v.Name.Space == docNS {
					rid = v.Value
				}
			}
			sheetID, parseErr := strconv.ParseUint(a["sheetId"], 10, 32)
			if parseErr != nil || sheetID == 0 || usedRIDs[rid] || rid == "" || a["sheetId"] == "" || ids[uint32(sheetID)] || a["name"] == "" || names[a["name"]] {
				return newError("protocol")
			}
			usedRIDs[rid] = true
			ids[uint32(sheetID)] = true
			names[a["name"]] = true
			sheets = append(sheets, rid)
		}
		if depth <= len(ancestors) {
			ancestors = append(ancestors[:depth], node.Name)
		}
		return nil
	})
	if e != nil {
		return e
	}
	if len(sheets) == 0 || sheetContainers != 1 {
		return newError("protocol")
	}
	for _, r := range rootRels {
		if (r.kind == officeNS+"/officeDocument" || r.kind == strictOfficeNS+"/officeDocument") && r.kind != docNS+"/officeDocument" {
			return newError("protocol")
		}
	}
	parsed := map[string]bool{}
	for _, id := range sheets {
		r, ok := rels[id]
		if !ok || r.external || r.kind != docNS+"/worksheet" || !strings.HasPrefix(r.target, "xl/worksheets/") || content[r.target] != worksheetContentType {
			return newError("protocol")
		}
		if parsed[r.target] {
			continue
		}
		parsed[r.target] = true
		e := inspectXML(parts[r.target], func(node xml.StartElement, depth int) error {
			if node.Name.Space != workbookNS || depth == 0 && node.Name.Local != "worksheet" {
				return newError("protocol")
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	for name, b := range parts {
		if name == "[Content_Types].xml" || name == "_rels/.rels" || name == "xl/workbook.xml" || name == "xl/_rels/workbook.xml.rels" || parsed[name] {
			continue
		}
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			if e := inspectXML(b, nil); e != nil {
				return e
			}
		}
	}

	return nil
}
