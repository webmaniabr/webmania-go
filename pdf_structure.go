package webmania

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// Conservative classic single-xref PDF structure recognition. Streams remain
// opaque; neither content rendering nor cryptographic validity is claimed.
type pdfName string
type pdfString int
type pdfRef struct{ number, generation int }
type pdfObject struct {
	value  any
	stream bool
	end    int
}
type pdfParser struct {
	data        []byte
	pos, budget int
	bad         bool
}

func pdfWhite(c byte) bool     { return c == 0 || c == 9 || c == 10 || c == 12 || c == 13 || c == 32 }
func pdfDelimiter(c byte) bool { return pdfWhite(c) || strings.ContainsRune("()<>[]{}/%", rune(c)) }
func (p *pdfParser) space() {
	for p.pos < len(p.data) {
		if pdfWhite(p.data[p.pos]) {
			p.pos++
			continue
		}
		if p.data[p.pos] == '%' {
			for p.pos < len(p.data) && p.data[p.pos] != '\n' && p.data[p.pos] != '\r' {
				p.pos++
			}
			continue
		}
		break
	}
}
func (p *pdfParser) word() string {
	p.space()
	a := p.pos
	for p.pos < len(p.data) && !pdfDelimiter(p.data[p.pos]) {
		p.pos++
	}
	if a == p.pos {
		p.bad = true
	}
	return string(p.data[a:p.pos])
}
func (p *pdfParser) take(s string) bool {
	p.space()
	if bytes.HasPrefix(p.data[p.pos:], []byte(s)) && (p.pos+len(s) == len(p.data) || pdfDelimiter(p.data[p.pos+len(s)])) {
		p.pos += len(s)
		return true
	}
	return false
}

var pdfInteger = regexp.MustCompile(`^[+-]?[0-9]+$`)
var pdfReal = regexp.MustCompile(`^[+-]?(?:[0-9]+\.[0-9]*|\.[0-9]+)$`)
var pdfHeader = regexp.MustCompile(`^%PDF-[12]\.[0-9](?:\r\n|\r|\n)`)
var pdfEnd = regexp.MustCompile(`startxref[\x00\t\n\f\r ]+([0-9]+)[\x00\t\n\f\r ]+%%EOF[\x00\t\n\f\r ]*$`)

func (p *pdfParser) value(depth int) any {
	p.budget--
	if depth > 64 || p.budget < 0 {
		p.bad = true
		return nil
	}
	p.space()
	if p.pos >= len(p.data) {
		p.bad = true
		return nil
	}
	c := p.data[p.pos]
	if bytes.HasPrefix(p.data[p.pos:], []byte("<<")) {
		p.pos += 2
		m := map[string]any{}
		for !p.bad {
			p.space()
			if bytes.HasPrefix(p.data[p.pos:], []byte(">>")) {
				p.pos += 2
				return m
			}
			k, ok := p.value(depth + 1).(pdfName)
			if !ok {
				p.bad = true
				break
			}
			if _, ok := m[string(k)]; ok {
				p.bad = true
				break
			}
			m[string(k)] = p.value(depth + 1)
		}
		return nil
	}
	if c == '[' {
		p.pos++
		a := []any{}
		for !p.bad {
			p.space()
			if p.pos < len(p.data) && p.data[p.pos] == ']' {
				p.pos++
				return a
			}
			a = append(a, p.value(depth+1))
		}
		return nil
	}
	if c == '/' {
		p.pos++
		var out []byte
		for p.pos < len(p.data) && !pdfDelimiter(p.data[p.pos]) {
			c := p.data[p.pos]
			p.pos++
			if c == '#' {
				if p.pos+2 > len(p.data) {
					p.bad = true
					break
				}
				v, e := strconv.ParseUint(string(p.data[p.pos:p.pos+2]), 16, 8)
				if e != nil {
					p.bad = true
					break
				}
				c = byte(v)
				p.pos += 2
			}
			out = append(out, c)
		}
		return pdfName(out)
	}
	if c == '(' {
		p.pos++
		level, n := 1, 0
		for p.pos < len(p.data) && level > 0 {
			c := p.data[p.pos]
			p.pos++
			if c == '\\' {
				if p.pos >= len(p.data) {
					p.bad = true
					break
				}
				p.pos++
				n++
				continue
			}
			if c == '(' {
				level++
			}
			if c == ')' {
				level--
			}
			if level > 64 {
				p.bad = true
				break
			}
			n++
		}
		if level != 0 {
			p.bad = true
		}
		return pdfString(n - 1)
	}
	if c == '<' {
		p.pos++
		n := 0
		for p.pos < len(p.data) && p.data[p.pos] != '>' {
			c := p.data[p.pos]
			p.pos++
			if pdfWhite(c) {
				continue
			}
			if !strings.ContainsRune("0123456789abcdefABCDEF", rune(c)) {
				p.bad = true
			}
			n++
		}
		if p.pos >= len(p.data) {
			p.bad = true
		} else {
			p.pos++
		}
		return pdfString((n + 1) / 2)
	}
	w := p.word()
	if pdfInteger.MatchString(w) {
		v, e := strconv.ParseInt(w, 10, 64)
		if e != nil {
			p.bad = true
			return nil
		}
		save := p.pos
		bad := p.bad
		second := p.word()
		if pdfInteger.MatchString(second) {
			g, e := strconv.ParseInt(second, 10, 32)
			if e == nil && p.take("R") {
				if v < 1 || v > 100000 || g < 0 || g > 65535 {
					p.bad = true
				}
				return pdfRef{int(v), int(g)}
			}
		}
		p.pos = save
		p.bad = bad
		return v
	}
	if pdfReal.MatchString(w) {
		v, e := strconv.ParseFloat(w, 64)
		if e != nil {
			p.bad = true
		}
		return v
	}
	if w == "true" {
		return true
	}
	if w == "false" {
		return false
	}
	if w == "null" {
		return nil
	}
	p.bad = true
	return nil
}
func inspectPDF(data []byte) (bool, error) {
	fail := func() (bool, error) { return false, newError("protocol") }
	h := pdfHeader.FindIndex(data)
	end := pdfEnd.FindSubmatchIndex(data)
	if h == nil || end == nil {
		return fail()
	}
	offset, e := strconv.Atoi(string(data[end[2]:end[3]]))
	if e != nil || offset < h[1] || offset >= end[0] {
		return fail()
	}
	p := &pdfParser{data: data, pos: offset, budget: 500000}
	if !p.take("xref") {
		return fail()
	}
	type entry struct {
		offset, generation int
		live               bool
	}
	entries := map[int]entry{}
	readInt := func() int {
		v, e := strconv.Atoi(p.word())
		if e != nil || v < 0 {
			p.bad = true
		}
		return v
	}
	for !p.bad {
		if p.take("trailer") {
			break
		}
		first, count := readInt(), readInt()
		if count < 1 || first+count > 100000 {
			p.bad = true
			break
		}
		for n := first; n < first+count; n++ {
			off, gen := readInt(), readInt()
			flag := p.word()
			if _, duplicate := entries[n]; duplicate || gen > 65535 || flag != "n" && flag != "f" {
				p.bad = true
			}
			entries[n] = entry{off, gen, flag == "n"}
		}
	}
	trailer, ok := p.value(0).(map[string]any)
	if p.bad || !ok {
		return fail()
	}
	p.space()
	if p.pos != end[0] {
		return fail()
	}
	size, ok := trailer["Size"].(int64)
	if !ok || size < 1 || size > 100000 {
		return fail()
	}
	if _, ok := trailer["Prev"]; ok {
		return fail()
	}
	if _, ok := trailer["XRefStm"]; ok {
		return fail()
	}
	for n := range entries {
		if int64(n) >= size {
			return fail()
		}
	}
	objects := map[pdfRef]pdfObject{}
	resolving := map[pdfRef]bool{}
	var objectAt func(pdfRef) (pdfObject, bool)
	objectAt = func(ref pdfRef) (pdfObject, bool) {
		if obj, ok := objects[ref]; ok {
			return obj, true
		}
		ent, ok := entries[ref.number]
		if !ok || !ent.live || ent.generation != ref.generation || ent.offset < h[1] || ent.offset >= offset || resolving[ref] || len(resolving) > 64 {
			return pdfObject{}, false
		}
		resolving[ref] = true
		defer delete(resolving, ref)
		q := &pdfParser{data: data, pos: ent.offset, budget: p.budget}
		n, e := strconv.Atoi(q.word())
		g, e2 := strconv.Atoi(q.word())
		if e != nil || e2 != nil || n != ref.number || g != ref.generation || !q.take("obj") {
			return pdfObject{}, false
		}
		v := q.value(0)
		stream := false
		if q.take("stream") {
			stream = true
			d, ok := v.(map[string]any)
			if !ok {
				return pdfObject{}, false
			}
			length := d["Length"]
			if r, ok := length.(pdfRef); ok {
				obj, valid := objectAt(r)
				if !valid || obj.stream {
					return pdfObject{}, false
				}
				length = obj.value
			}
			l, ok := length.(int64)
			if !ok || l < 0 || l > int64(len(data)) {
				return pdfObject{}, false
			}
			if bytes.HasPrefix(data[q.pos:], []byte("\r\n")) {
				q.pos += 2
			} else if q.pos < len(data) && (data[q.pos] == '\n' || data[q.pos] == '\r') {
				q.pos++
			} else {
				return pdfObject{}, false
			}
			if int64(q.pos)+l > int64(offset) {
				return pdfObject{}, false
			}
			q.pos += int(l)
			if q.pos < len(data) && data[q.pos] == '\r' {
				q.pos++
			}
			if q.pos < len(data) && data[q.pos] == '\n' {
				q.pos++
			}
			if !bytes.HasPrefix(data[q.pos:], []byte("endstream")) || q.pos+9 < len(data) && !pdfDelimiter(data[q.pos+9]) {
				return pdfObject{}, false
			}
			q.pos += 9
		}
		if q.bad || !q.take("endobj") || q.pos > offset {
			return pdfObject{}, false
		}
		if d, ok := v.(map[string]any); ok && (d["Type"] == pdfName("ObjStm") || d["Type"] == pdfName("XRef")) {
			return pdfObject{}, false
		}
		p.budget = q.budget
		if p.budget < 0 {
			return pdfObject{}, false
		}
		obj := pdfObject{v, stream, q.pos}
		objects[ref] = obj
		return obj, true
	}
	// Physical scan rejects xref targets hidden in comments, strings or streams.
	physical := map[int]bool{}
	q := &pdfParser{data: data, pos: h[1], budget: 500000}
	for {
		q.space()
		if q.pos == offset {
			break
		}
		if q.pos > offset {
			return fail()
		}
		start := q.pos
		n, e := strconv.Atoi(q.word())
		g, e2 := strconv.Atoi(q.word())
		if e != nil || e2 != nil || !q.take("obj") {
			return fail()
		}
		ref := pdfRef{n, g}
		ent, ok := entries[n]
		if !ok || !ent.live || ent.offset != start || ent.generation != g {
			return fail()
		}
		obj, ok := objectAt(ref)
		if !ok {
			return fail()
		}
		physical[start] = true
		q.pos = obj.end
	}
	for _, ent := range entries {
		if ent.live && !physical[ent.offset] {
			return fail()
		}
	}
	// Every indirect value must resolve to the exact physical live generation.
	var references func(any, int) bool
	references = func(v any, depth int) bool {
		if depth > 64 {
			return false
		}
		switch x := v.(type) {
		case pdfRef:
			_, ok := objectAt(x)
			return ok
		case []any:
			for _, c := range x {
				if !references(c, depth+1) {
					return false
				}
			}
		case map[string]any:
			for _, c := range x {
				if !references(c, depth+1) {
					return false
				}
			}
		}
		return true
	}
	for _, obj := range objects {
		if !references(obj.value, 0) {
			return fail()
		}
	}
	resolve := func(v any) (map[string]any, bool) {
		r, ok := v.(pdfRef)
		if !ok {
			return nil, false
		}
		obj, ok := objectAt(r)
		if !ok || obj.stream {
			return nil, false
		}
		d, ok := obj.value.(map[string]any)
		return d, ok
	}
	catalog, ok := resolve(trailer["Root"])
	if !ok || catalog["Type"] != pdfName("Catalog") {
		return fail()
	}
	visited := map[pdfRef]bool{}
	var pages func(any, any, any, any, int) (int, bool)
	pages = func(value, parent, media, resources any, depth int) (int, bool) {
		ref, ok := value.(pdfRef)
		if !ok || visited[ref] || depth > 64 {
			return 0, false
		}
		visited[ref] = true
		d, ok := resolve(value)
		if !ok || parent != nil && d["Parent"] != parent {
			return 0, false
		}
		if v, ok := d["MediaBox"]; ok {
			media = v
		}
		if v, ok := d["Resources"]; ok {
			resources = v
		}
		if d["Type"] == pdfName("Pages") {
			kids, ok := d["Kids"].([]any)
			count, ok2 := d["Count"].(int64)
			if !ok || !ok2 || count < 1 {
				return 0, false
			}
			total := 0
			for _, kid := range kids {
				n, ok := pages(kid, ref, media, resources, depth+1)
				if !ok {
					return 0, false
				}
				total += n
			}
			return total, int64(total) == count
		}
		if d["Type"] != pdfName("Page") {
			return 0, false
		}
		if content, exists := d["Contents"]; exists {
			items, ok := content.([]any)
			if !ok {
				items = []any{content}
			}
			for _, v := range items {
				ref, ok := v.(pdfRef)
				if !ok {
					return 0, false
				}
				obj, ok := objectAt(ref)
				if !ok || !obj.stream {
					return 0, false
				}
			}
		}
		if r, ok := media.(pdfRef); ok {
			obj, valid := objectAt(r)
			if !valid || obj.stream {
				return 0, false
			}
			media = obj.value
		}
		if r, ok := resources.(pdfRef); ok {
			obj, valid := objectAt(r)
			if !valid || obj.stream {
				return 0, false
			}
			resources = obj.value
		}
		box, ok := media.([]any)
		_, hasResources := resources.(map[string]any)
		if !ok || len(box) != 4 || !hasResources {
			return 0, false
		}
		nums := make([]float64, 4)
		for i, v := range box {
			switch x := v.(type) {
			case int64:
				nums[i] = float64(x)
			case float64:
				nums[i] = x
			default:
				return 0, false
			}
		}
		if nums[0] >= nums[2] || nums[1] >= nums[3] {
			return 0, false
		}
		return 1, true
	}
	if _, ok := pages(catalog["Pages"], nil, nil, nil, 0); !ok {
		return fail()
	}
	if encrypted, exists := trailer["Encrypt"]; exists {
		d, ok := resolve(encrypted)
		if !ok || d["Filter"] != pdfName("Standard") {
			return fail()
		}
		v, vok := d["V"].(int64)
		r, rok := d["R"].(int64)
		_, pok := d["P"].(int64)
		o, ook := d["O"].(pdfString)
		u, uok := d["U"].(pdfString)
		ids, iok := trailer["ID"].([]any)
		if !vok || !rok || !pok || !ook || !uok || o < 32 || u < 32 || v < 1 || v > 5 || r < 2 || r > 6 || !iok || len(ids) != 2 {
			return fail()
		}
		for _, id := range ids {
			n, ok := id.(pdfString)
			if !ok || n < 16 {
				return fail()
			}
		}
		return true, nil
	}
	return false, nil
}
