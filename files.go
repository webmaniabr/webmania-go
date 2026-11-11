package webmania

import (
	"bytes"
	"encoding/xml"
)

var fiscalRoots = map[string]bool{"NFe": true, "nfeProc": true, "procEventoNFe": true, "evento": true, "retEvento": true, "procInutNFe": true, "inutNFe": true, "CTe": true, "cteProc": true, "procEventoCTe": true, "CTeOS": true, "MDFe": true, "mdfeProc": true, "procEventoMDFe": true, "NFCom": true, "nfcomProc": true, "DCe": true, "dceProc": true, "NFSe": true, "Nfse": true, "CompNfse": true, "ConsultarNfseResposta": true, "EnviarLoteRpsResposta": true}

func inspectFile(status int, mime string, data []byte, allowed map[string]bool) (FileResponse, error) {
	f := FileResponse{Status: status, MediaType: mime, Data: append([]byte(nil), data...)}
	if !allowed[mime] {
		return FileResponse{}, newError("protocol")
	}
	switch mime {
	case "text/csv":
		if e := inspectCSV(data); e != nil {
			return FileResponse{}, e
		}
	case xlsxMediaType:
		if e := inspectSpreadsheet(data); e != nil {
			return FileResponse{}, e
		}
	case "application/octet-stream":
		if len(data) == 0 {
			return FileResponse{}, newError("protocol")
		}
	case "application/pdf":
		protected, e := inspectPDF(data)
		if e != nil {
			return FileResponse{}, e
		}
		f.Protected = protected
	case "application/xml", "text/xml":
		e := inspectXML(data, func(node xml.StartElement, depth int) error {
			if depth == 0 && !fiscalRoots[node.Name.Local] {
				return newError("protocol")
			}
			return nil
		})
		if e != nil {
			return FileResponse{}, e
		}
	case "application/zip":
		_, protected, e := inspectArchive(data, maxExpandedSpreadsheetBytes)
		if e != nil {
			return FileResponse{}, e
		}
		f.Protected = protected

	default:
		return FileResponse{}, newError("protocol")
	}
	return f, nil
}

// inspectReport validates report files: the declared media type and the bytes must agree.
// Only the legacy report operation accepts text/xml: a ZIP local-file signature is XLSX and any
// other body is CSV returned byte-exact (never decoded); XML, HTML and JSON bodies are refused.
// FileResponse.MediaType carries the detected format in that case.
func inspectReport(oid string, status int, mime string, data []byte, allowed map[string]bool) (FileResponse, error) {
	if !allowed[mime] || len(data) == 0 {
		return FileResponse{}, newError("protocol")
	}
	zipped := bytes.HasPrefix(data, []byte("PK"))
	switch {
	case mime == "text/xml" && oid == "fiscalDownloadReport":
		if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return inspectFile(status, xlsxMediaType, data, map[string]bool{xlsxMediaType: true})
		}
		return inspectFile(status, "text/csv", data, map[string]bool{"text/csv": true})
	case mime == "text/html":
		// A declared HTML rendering of the report itself: kept as bytes, never executed.
		if zipped || bytes.IndexByte(data, 0) >= 0 {
			return FileResponse{}, newError("protocol")
		}
		return FileResponse{Status: status, MediaType: mime, Data: append([]byte(nil), data...)}, nil
	case mime == "application/xml" || mime == "text/xml":
		if zipped {
			return FileResponse{}, newError("protocol")
		}
		if e := inspectXML(data, func(xml.StartElement, int) error { return nil }); e != nil {
			return FileResponse{}, e
		}
		return FileResponse{Status: status, MediaType: mime, Data: append([]byte(nil), data...)}, nil
	case mime == "application/octet-stream":
		if zipped {
			return inspectFile(status, "application/zip", data, map[string]bool{"application/zip": true})
		}
	}
	f, e := inspectFile(status, mime, data, allowed)
	if e == nil && mime == "application/octet-stream" {
		f.MediaType = mime
	}
	return f, e
}
