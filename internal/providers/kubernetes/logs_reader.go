package kubernetes

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// maxLineBytes bounds one logical log line; longer lines keep their
// beginning (LineCut) and the rest up to the newline is discarded.
const maxLineBytes = 256 << 10

// lineReader splits a pods/log response (timestamps=true) into logical
// lines. The kubelet writes one timestamp per logical line: continuation
// records of a CRI partial line carry none and stay inside the same line
// (cri-client pkg/logs ReadLogs: the prefix is added only at a line start).
// Memory is bounded by maxLineBytes plus the bufio buffer — never by the
// length of what the container printed.
type lineReader struct {
	br *bufio.Reader
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{br: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next line. complete is false for a last fragment without
// a newline (err is then io.EOF or the read error): the caller decides
// whether a clean end makes it a line (non-follow EOF) or not (a cut or
// broken stream; the next request replays it).
func (lr *lineReader) next() (line provider.LogLine, complete bool, err error) {
	var buf []byte
	cut := false
	for {
		chunk, rerr := lr.br.ReadSlice('\n')
		if !cut {
			room := maxLineBytes - len(buf)
			if len(chunk) > room {
				buf = append(buf, chunk[:room]...)
				cut = true
			} else {
				buf = append(buf, chunk...)
			}
		}
		if rerr == nil {
			complete = true
			break
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue // a long line: keep reading (and discarding past the cap)
		}
		err = rerr
		break
	}
	if len(buf) == 0 && !complete {
		return provider.LogLine{}, false, err
	}
	line = parseLine(buf)
	if cut {
		line.Flags |= provider.LineCut
	}
	return line, complete, err
}

// buffered: bytes already read from the network but not returned yet. The
// caller hands a batch to the sink when nothing is buffered (the next read
// would wait), so lines are never held back while the pod is quiet.
func (lr *lineReader) buffered() int { return lr.br.Buffered() }

// parseLine splits "<RFC3339Nano> <text>\n". A line without a valid
// timestamp keeps its text and is flagged (it cannot move a cursor).
func parseLine(b []byte) provider.LogLine {
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	if i := bytes.IndexByte(b, ' '); i > 0 && i <= 40 {
		if _, err := time.Parse(time.RFC3339Nano, string(b[:i])); err == nil {
			return provider.LogLine{TS: string(b[:i]), Text: string(b[i+1:])}
		}
	}
	return provider.LogLine{Text: string(b), Flags: provider.LineNoTime}
}

// tsTime parses a line timestamp (zero if none).
func tsTime(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return t
}
