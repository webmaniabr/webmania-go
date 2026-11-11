package webmania

import (
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// maxChunkSizeLine bounds one chunk-size line (with extensions). It stays below the native
// decoder's 4096-byte line buffer so an oversized line is reported as a protocol error here.
const maxChunkSizeLine = 4000

const (
	chunkSize = iota
	chunkData
	chunkCRLF
	chunkTrailers
	chunkDone
)

// This bounded lexical observer accompanies net/http's native chunk decoder.
// It does not supply body bytes or decide HTTP validity. It observes complete
// Location lines before native trailer rejection and records observation gaps.
type chunkObservation struct {
	reader             io.Reader
	remaining          int
	state              int
	left               uint64
	line               strings.Builder
	locations          []string
	complete, exceeded bool
}

func (o *chunkObservation) Read(p []byte) (int, error) {
	if o.exceeded {
		if o.state == chunkTrailers {
			return 0, newError("header_limit")
		}
		return 0, newError("protocol")
	}
	n, e := o.reader.Read(p)
	for i, c := range p[:n] {
		switch o.state {
		case chunkData:
			if o.left > 0 {
				o.left--
			}
			if o.left == 0 {
				o.state = chunkCRLF
				o.left = 2
			}
		case chunkCRLF:
			o.left--
			if o.left == 0 {
				o.state = chunkSize
			}
		case chunkSize, chunkTrailers:
			// Chunk-size lines have their own per-line bound; only trailer bytes count
			// against the remaining header budget.
			if o.state == chunkTrailers {
				o.remaining--
				if o.remaining < 0 {
					o.exceeded = true
					return i, newError("header_limit")
				}
			} else if o.line.Len() >= maxChunkSizeLine {
				o.exceeded = true
				return i, newError("protocol")
			}
			o.line.WriteByte(c)
			if c != '\n' {
				continue
			}
			line := o.line.String()
			o.line.Reset()
			if o.state == chunkSize {
				size, _, _ := strings.Cut(strings.TrimSpace(line), ";")
				v, err := strconv.ParseUint(size, 16, 64)
				if err != nil {
					o.state = chunkDone
					continue
				}
				o.left = v
				if v == 0 {
					o.state = chunkTrailers
				} else {
					o.state = chunkData
				}
			} else {
				if line == "\r\n" {
					o.complete = true
					o.state = chunkDone
					continue
				}
				key, val, ok := strings.Cut(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), ":")
				if ok && strings.EqualFold(key, "Location") {
					o.locations = append(o.locations, textproto.TrimString(val))
				}
			}
		}
	}
	return n, e
}
