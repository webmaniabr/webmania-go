package webmania

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"sort"
	"unicode/utf8"
)

// Classic single-disk ZIP only. Unsupported ZIP64/split layouts fail closed.
// Payloads are streamed only into a bounded discard sink for size/CRC checks.
func inspectArchive(data []byte, limit uint64) (*zip.Reader, bool, error) {
	fail := func() (*zip.Reader, bool, error) { return nil, false, newError("protocol") }
	u16 := func(p int) uint16 { return binary.LittleEndian.Uint16(data[p : p+2]) }
	u32 := func(p int) uint32 { return binary.LittleEndian.Uint32(data[p : p+4]) }
	if len(data) < 22 {
		return fail()
	}
	end := -1
	for p := len(data) - 22; p >= 0 && p >= len(data)-65557; p-- {
		if u32(p) == 0x06054b50 && p+22+int(u16(p+20)) == len(data) {
			end = p
			break
		}
	}
	if end < 0 || u16(end+4) != 0 || u16(end+6) != 0 || u16(end+8) != u16(end+10) {
		return fail()
	}
	count := int(u16(end + 10))
	size, offset := uint64(u32(end+12)), uint64(u32(end+16))
	if count == 0 || count > 10000 || offset+size != uint64(end) {
		return fail()
	}
	p := int(offset)
	if p < 0 || p > end {
		return fail()
	}
	type span struct{ start, end uint64 }
	spans := make([]span, 0, count)
	names := map[string]bool{}
	var expanded uint64
	protected := false
	for i := 0; i < count; i++ {
		if p+46 > end || u32(p) != 0x02014b50 {
			return fail()
		}
		flags, method := u16(p+8), u16(p+10)
		if flags & ^uint16(0x080f) != 0 {
			return fail()
		}
		crc := u32(p + 16)
		compressed, plain := uint64(u32(p+20)), uint64(u32(p+24))
		nameLen, extraLen, commentLen := int(u16(p+28)), int(u16(p+30)), int(u16(p+32))
		local := uint64(u32(p + 42))
		next := p + 46 + nameLen + extraLen + commentLen
		if next > end || u16(p+34) != 0 || compressed == 0xffffffff || plain == 0xffffffff || local == 0xffffffff {
			return fail()
		}
		if !validZipExtra(data[p+46+nameLen : p+46+nameLen+extraLen]) {
			return fail()
		}
		name := string(data[p+46 : p+46+nameLen])
		if name == "" || names[name] || !utf8.ValidString(name) {
			return fail()
		}
		names[name] = true
		if plain > limit-expanded {
			return nil, false, newError("response_limit")
		}
		expanded += plain
		if local+30 > offset {
			return fail()
		}
		l := int(local)
		if u32(l) != 0x04034b50 || u16(l+6) != flags || u16(l+8) != method {
			return fail()
		}
		n, x := int(u16(l+26)), int(u16(l+28))
		start := local + 30 + uint64(n+x)
		finish := start + compressed
		if start > offset || finish > offset || n != nameLen || string(data[l+30:l+30+n]) != name {
			return fail()
		}
		if !validZipExtra(data[l+30+n : l+30+n+x]) {
			return fail()
		}
		lc, ls, lu := u32(l+14), uint64(u32(l+18)), uint64(u32(l+22))
		if flags&8 == 0 {
			if lc != crc || ls != compressed || lu != plain {
				return fail()
			}
		} else {
			if lc != 0 && lc != crc || ls != 0 && ls != compressed || lu != 0 && lu != plain {
				return fail()
			}
			found := false
			for _, signed := range []bool{true, false} {
				q := finish
				if signed {
					if q+4 > offset || u32(int(q)) != 0x08074b50 {
						continue
					}
					q += 4
				}
				if q+12 <= offset && u32(int(q)) == crc && uint64(u32(int(q)+4)) == compressed && uint64(u32(int(q)+8)) == plain {
					finish = q + 12
					found = true
					break
				}
			}
			if !found {
				return fail()
			}
		}
		if flags&1 != 0 {
			protected = true
		} else if method != zip.Store && method != zip.Deflate {
			return fail()
		}
		spans = append(spans, span{local, finish})
		p = next
	}
	if p != end {
		return fail()
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var previous uint64
	for _, s := range spans {
		if s.start != previous || s.end < s.start {
			return fail()
		}
		previous = s.end
	}
	if previous != offset {
		return fail()
	}
	zr, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil || len(zr.File) != count {
		return fail()
	}
	var actual uint64
	for _, entry := range zr.File {
		if entry.Flags&1 != 0 {
			continue
		}
		r, e := entry.Open()
		if e != nil {
			return fail()
		}
		remaining := limit - actual
		n, e := io.Copy(io.Discard, io.LimitReader(r, int64(remaining)+1))
		_ = r.Close()
		if uint64(n) > remaining {
			return nil, false, newError("response_limit")
		}
		actual += uint64(n)
		if e != nil || uint64(n) != entry.UncompressedSize64 {
			return fail()
		}
	}
	return zr, protected, nil
}

func validZipExtra(extra []byte) bool {
	for len(extra) > 0 {
		if len(extra) < 4 {
			return false
		}
		id := binary.LittleEndian.Uint16(extra)
		size := int(binary.LittleEndian.Uint16(extra[2:]))
		if size > len(extra)-4 || id == 1 {
			return false
		}
		extra = extra[4+size:]
	}
	return true
}
