package streams

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/spk/spk-ocular/internal/provider"
)

// Terminal protocol over one WebSocket (docs/plans/2026-09-29-p3-exec-
// portforward.md). Binary messages are terminal bytes: page→server input,
// server→page output. Text messages are JSON control:
//
//	page→server  {"k":"resize","cols":N,"rows":N}  {"k":"ack","n":N}  {"k":"intr"}
//	server→page  {"k":"state","state":"connecting|running"}  {"k":"iack","n":N}
//	             {"k":"exit","code":N}  {"k":"end",...}  then close 1000
//
// Both directions are credit-based with cumulative counters (a repeated
// counter is harmless): the server sends output only while fewer than
// termOutWindow bytes are unacknowledged by the page ("ack": bytes xterm has
// processed), and the page keeps at most termInWindow input bytes not yet
// confirmed by "iack" (bytes written to the command's stdin). A page that
// breaks the input window or sends an impossible counter is closed with
// statusProtocol. "iack" counts input written to stdin or dropped.
//
// "intr" is Ctrl+C: input still queued here is dropped (a pending paste; a
// TTY flushes its input on ^C too) and ^C is the next byte written to
// stdin, outside the input window — so it gets through even when the
// window is exhausted by a command that does not read. Bytes already
// being written cannot be recalled.
const (
	termOutWindow  = 1 << 20
	termInWindow   = 256 << 10
	termMaxMessage = 32 << 10 // one input message; also bounds control
	termMaxControl = 4 << 10
	termReadChunk  = 32 << 10
	maxTermSide    = 1000

	statusProtocol   websocket.StatusCode = 4002
	statusNotReading websocket.StatusCode = 4003
)

type termTimings struct {
	ping, pingTimeout time.Duration // dead page detection
	ackTimeout        time.Duration // output window full and no ack for this long
	drain             time.Duration // after the command ended, to deliver its output
	write             time.Duration // one network write
	hangup            time.Duration // for the command to end after ^C ^D
}

var defaultTermTimings = termTimings{
	ping: 20 * time.Second, pingTimeout: 15 * time.Second,
	ackTimeout: 60 * time.Second, drain: 10 * time.Second, write: writeTimeout,
	hangup: 2 * time.Second,
}

// hangupKeys end a terminal that is going away the way a user would:
// ^C interrupts the foreground job, then ^D (EOF on the emptied line)
// ends the shell. Closing the connection alone does not: container
// runtimes (containerd on kind, measured) keep an exec'd process and its
// children running after the client is gone, and closing stdin does not
// reach a TTY as EOF (docs/plans/2026-09-29-p3-exec-portforward.md).
var hangupKeys = [][]byte{{0x03}, {0x04}}

const hangupKeyGap = 100 * time.Millisecond

var (
	errPageGone   = errors.New("the page closed the terminal")
	errNotReading = errors.New("the page is not reading the terminal's output")
	errPeerDead   = errors.New("the page stopped responding")
	errDrained    = errors.New("the command's last output was not delivered in time")
	errTermDone   = errors.New("terminal finished")
)

type protocolError struct{ msg string }

func (e *protocolError) Error() string { return "terminal protocol violation: " + e.msg }

type outMsg struct {
	typ  websocket.MessageType
	data []byte
	fin  chan struct{} // the sentinel: everything before it is written
}

type termBridge struct {
	conn     *websocket.Conn
	tm       termTimings
	classify Classifier

	ctx    context.Context
	cancel context.CancelCauseFunc
	// readCtx is the reader's own: coder/websocket closes the connection
	// when a Read's context ends, and ending the terminal must still allow
	// a close handshake (the reader receives the page's close frame).
	readCtx    context.Context
	readCancel context.CancelFunc
	out        chan outMsg

	mu       sync.Mutex
	sent     int64 // output bytes sent (reserved before sending)
	acked    int64 // output bytes the page processed
	credit   chan struct{}
	inQ      [][]byte
	inBytes  int   // received, not yet written to stdin
	inDone   int64 // written to stdin
	inSig    chan struct{}
	intr     bool // ^C is due before any queued input
	running  sync.Once
	attached atomic.Bool // the command side started reading input or writing output

	sizes *sizeBox
}

func newTermBridge(conn *websocket.Conn, tm termTimings, classify Classifier, size provider.TermSize) *termBridge {
	return &termBridge{
		conn: conn, tm: tm, classify: classify,
		out:    make(chan outMsg, 16),
		credit: make(chan struct{}, 1),
		inSig:  make(chan struct{}, 1),
		sizes:  newSizeBox(size),
	}
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

type runResult struct {
	st  provider.ExitStatus
	err error
}

// run serves the terminal until the command ends, the page goes away or
// parent is cancelled (revoked says which end frame that deserves). It
// returns when every goroutine it started has finished.
func (b *termBridge) run(parent context.Context, sess TermSession, revoked func() bool) {
	b.ctx, b.cancel = context.WithCancelCause(parent)
	b.readCtx, b.readCancel = context.WithCancel(context.Background())
	defer b.readCancel()
	ctx := b.ctx
	b.sizes.done = ctx.Done()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	// The command's own context outlives the bridge's by the hang-up:
	// ending the terminal first asks the command to end in-band.
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	// Once the command is told to stop, unblock it if it is stuck writing
	// output or reading input.
	stopPipes := context.AfterFunc(runCtx, func() {
		_ = outR.CloseWithError(errTermDone)
		_ = inR.CloseWithError(errTermDone)
	})
	defer stopPipes()
	var wg sync.WaitGroup
	start := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	writerDone := make(chan struct{})
	go func() { defer close(writerDone); b.writeLoop() }()
	start(b.readLoop)
	start(func() { b.stdinLoop(inW, runCtx) })
	start(b.pingLoop)
	outDone := make(chan struct{})
	go func() { defer close(outDone); b.outputLoop(outR) }()

	b.send(websocket.MessageText, termState("connecting"))
	runDone := make(chan runResult, 1)
	go func() {
		st, err := sess.Run(runCtx, provider.Terminal{
			Stdin:  &firstRead{r: inR, first: b.markRunning},
			Stdout: outW,
			Sizes:  b.sizes,
		})
		runDone <- runResult{st, err}
	}()
	var res runResult
	select {
	case res = <-runDone:
	case <-ctx.Done():
		res = b.hangup(runDone, runCancel)
	}
	st, runErr := res.st, res.err
	_ = outW.Close() // the output loop sees EOF after what is buffered
	drain := time.NewTimer(b.tm.drain)
	select {
	case <-outDone:
	case <-ctx.Done():
	case <-drain.C:
		b.cancel(errDrained)
	}
	drain.Stop()

	if ctx.Err() == nil {
		// The command ended and its output is out: exit, end, close.
		end := End{K: "end", Reason: "done"}
		switch {
		case runErr != nil && !errors.Is(runErr, context.Canceled):
			end.Reason = "error"
			end.Class, end.Message = b.classify(runErr)
		case runErr == nil && st.Known:
			b.send(websocket.MessageText, mustJSON(map[string]any{"k": "exit", "code": st.Code}))
		}
		b.send(websocket.MessageText, mustJSON(end))
		fin := make(chan struct{})
		select {
		case b.out <- outMsg{fin: fin}:
			select {
			case <-fin:
			case <-ctx.Done():
			}
		case <-ctx.Done():
		}
		if ctx.Err() == nil {
			b.cancel(errTermDone)
			_ = b.conn.Close(websocket.StatusNormalClosure, "")
		}
	}
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errTermDone):
	case revoked():
		b.finalEnd(End{K: "end", Reason: "gone"})
		_ = b.conn.Close(websocket.StatusGoingAway, "closed")
	case errors.Is(cause, errDrained):
		b.finalEnd(End{K: "end", Reason: "error", Class: "unavailable", Message: errDrained.Error()})
		_ = b.conn.Close(statusNotReading, "output not delivered")
	case errors.Is(cause, errNotReading):
		_ = b.conn.Close(statusNotReading, "output not read")
	default:
		var pe *protocolError
		if errors.As(cause, &pe) {
			_ = b.conn.Close(statusProtocol, truncate(pe.msg, 120))
		} else {
			_ = b.conn.CloseNow() // the page is gone or dead: nobody to tell
		}
	}
	_ = b.conn.CloseNow()
	b.readCancel()
	runCancel()
	_ = inW.CloseWithError(errTermDone)
	<-writerDone
	<-outDone
	wg.Wait()
}

// hangup ends a command whose terminal is going away: the stdin loop
// drops queued input and types ^C ^D (hangupInput; the output keeps being
// read and dropped meanwhile, so the command is not stuck writing), then,
// if it has not ended within the hang-up time, cancellation.
func (b *termBridge) hangup(runDone <-chan runResult, runCancel context.CancelFunc) runResult {
	if b.attached.Load() {
		t := time.NewTimer(b.tm.hangup)
		defer t.Stop()
		select {
		case r := <-runDone:
			return r
		case <-t.C:
		}
	}
	runCancel()
	return <-runDone
}

// finalEnd tries to tell a still-listening page why the terminal ended
// (bounded: the page may be the reason).
func (b *termBridge) finalEnd(e End) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = b.conn.Write(ctx, websocket.MessageText, mustJSON(e))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func termState(state string) []byte { return mustJSON(map[string]string{"k": "state", "state": state}) }

// send queues a message for the ordered writer (false once the terminal
// is ending).
func (b *termBridge) send(typ websocket.MessageType, data []byte) bool {
	select {
	case b.out <- outMsg{typ: typ, data: data}:
		return true
	case <-b.ctx.Done():
		return false
	}
}

func (b *termBridge) writeLoop() {
	for {
		select {
		case m := <-b.out:
			if m.fin != nil {
				close(m.fin)
				continue
			}
			if b.ctx.Err() != nil {
				return
			}
			// Not derived from b.ctx: coder/websocket closes the connection
			// when a write's context ends, and ending the terminal must not
			// cut a write short before the close frame.
			wctx, cancel := context.WithTimeout(context.Background(), b.tm.write)
			err := b.conn.Write(wctx, m.typ, m.data)
			timedOut := wctx.Err() == context.DeadlineExceeded
			cancel()
			if err != nil {
				if timedOut {
					b.cancel(errNotReading)
				} else {
					b.cancel(errPageGone)
				}
				return
			}
		case <-b.ctx.Done():
			return
		}
	}
}

func (b *termBridge) markRunning() {
	b.attached.Store(true)
	b.running.Do(func() { b.send(websocket.MessageText, termState("running")) })
}

// readLoop is the only reader of the socket. It never blocks on the
// command: input goes to a bounded queue, sizes to a latest-wins box,
// acks to the credit counter.
func (b *termBridge) readLoop() {
	b.conn.SetReadLimit(termMaxMessage)
	for {
		typ, data, err := b.conn.Read(b.readCtx)
		if err != nil {
			b.cancel(errPageGone)
			return
		}
		if typ == websocket.MessageBinary {
			if !b.pushInput(data) {
				b.cancel(&protocolError{"input window exceeded"})
				return
			}
			continue
		}
		if len(data) > termMaxControl {
			b.cancel(&protocolError{"control message too large"})
			return
		}
		if msg := b.control(data); msg != "" {
			b.cancel(&protocolError{msg})
			return
		}
	}
}

// control applies one control message; a non-empty result is a violation.
// Unknown kinds are ignored (a newer page).
func (b *termBridge) control(data []byte) string {
	var m struct {
		K    string   `json:"k"`
		Cols *float64 `json:"cols"`
		Rows *float64 `json:"rows"`
		N    *float64 `json:"n"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "bad control message"
	}
	switch m.K {
	case "resize":
		c, okC := side(m.Cols)
		r, okR := side(m.Rows)
		if !okC || !okR {
			return "bad terminal size"
		}
		b.sizes.set(provider.TermSize{Cols: c, Rows: r})
	case "intr":
		b.interrupt()
	case "ack":
		if m.N == nil || *m.N != math.Trunc(*m.N) || *m.N < 0 || *m.N > 1<<53 {
			return "bad ack"
		}
		if !b.ack(int64(*m.N)) {
			return "ack out of range"
		}
	}
	return ""
}

func side(v *float64) (uint16, bool) {
	if v == nil || *v != math.Trunc(*v) || *v < 1 || *v > maxTermSide {
		return 0, false
	}
	return uint16(*v), true
}

// ack records the page's cumulative processed-output counter: equal is a
// no-op, lower or beyond what was sent is impossible.
func (b *termBridge) ack(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < b.acked || n > b.sent {
		return false
	}
	if n > b.acked {
		b.acked = n
		signal(b.credit)
	}
	return true
}

func (b *termBridge) pushInput(p []byte) bool {
	if len(p) == 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inBytes+len(p) > termInWindow {
		return false
	}
	b.inQ = append(b.inQ, p)
	b.inBytes += len(p)
	signal(b.inSig)
	return true
}

// interrupt drops the queued input and puts ^C first (one at a time: a
// page repeating Ctrl+C cannot grow the queue).
func (b *termBridge) interrupt() {
	b.mu.Lock()
	b.dropInputLocked()
	b.intr = true
	b.mu.Unlock()
	signal(b.inSig)
}

// dropInputLocked forgets the queued input; it counts as done for the
// page's window.
func (b *termBridge) dropInputLocked() {
	for _, c := range b.inQ {
		b.inDone += int64(len(c))
	}
	b.inQ, b.inBytes = nil, 0
}

// stdinLoop is the only writer of the command's stdin: queued input, ^C
// on "intr", and — once the terminal is ending — the hang-up keys, in that
// order and never interleaved. Each written or dropped chunk is confirmed
// to the page ("iack"), which frees its input window.
func (b *termBridge) stdinLoop(w *io.PipeWriter, runCtx context.Context) {
	for {
		if b.ctx.Err() != nil {
			b.hangupInput(w, runCtx)
			return
		}
		b.mu.Lock()
		var chunk []byte
		intr := b.intr
		switch {
		case intr:
			b.intr = false
			chunk = hangupKeys[0]
		case len(b.inQ) > 0:
			chunk = b.inQ[0]
			b.inQ[0] = nil
			b.inQ = b.inQ[1:]
		}
		b.mu.Unlock()
		if chunk == nil {
			select {
			case <-b.inSig:
			case <-b.ctx.Done():
			}
			continue
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
		b.mu.Lock()
		if !intr {
			b.inBytes -= len(chunk)
			b.inDone += int64(len(chunk))
		}
		n := b.inDone
		b.mu.Unlock()
		b.send(websocket.MessageText, mustJSON(map[string]any{"k": "iack", "n": n})) // false: ending, see the top
	}
}

// hangupInput drops what is still queued and, if the command is attached,
// types ^C, then ^D after a short gap. A write the command does not take
// ends when its run is cancelled (the pipe closes).
func (b *termBridge) hangupInput(w io.Writer, runCtx context.Context) {
	b.mu.Lock()
	b.dropInputLocked()
	b.intr = false
	b.mu.Unlock()
	if !b.attached.Load() {
		return
	}
	for i, k := range hangupKeys {
		if i > 0 {
			t := time.NewTimer(hangupKeyGap)
			select {
			case <-t.C:
			case <-runCtx.Done():
				t.Stop()
				return
			}
		}
		if _, err := w.Write(k); err != nil {
			return
		}
	}
}

// outputLoop sends the command's output only as the page has credit for
// it, so a page that falls behind stops the command instead of growing
// buffers. At most one chunk is read ahead (so the end of the output is
// seen even while the window is full). Once the terminal is ending the
// output is read and dropped until the command is gone (it must not get
// stuck writing while it is being hung up).
func (b *termBridge) outputLoop(r io.Reader) {
	buf := make([]byte, termReadChunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b.markRunning()
			if b.ctx.Err() == nil {
				b.deliver(buf[:n])
			}
		}
		if err != nil {
			return
		}
	}
}

// deliver sends p as credit allows; false once the terminal is ending.
func (b *termBridge) deliver(p []byte) bool {
	for len(p) > 0 {
		avail, ok := b.waitCredit()
		if !ok {
			return false
		}
		k := min(avail, len(p))
		b.mu.Lock()
		b.sent += int64(k)
		b.mu.Unlock()
		if !b.send(websocket.MessageBinary, append([]byte(nil), p[:k]...)) {
			return false
		}
		p = p[k:]
	}
	return true
}

// waitCredit returns how many output bytes may be sent now, waiting while
// the window is full; no ack for ackTimeout ends the terminal.
func (b *termBridge) waitCredit() (int, bool) {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		b.mu.Lock()
		avail := termOutWindow - (b.sent - b.acked)
		b.mu.Unlock()
		if avail > 0 {
			return int(avail), true
		}
		if timer == nil {
			timer = time.NewTimer(b.tm.ackTimeout)
		}
		select {
		case <-b.credit:
		case <-timer.C:
			b.cancel(errNotReading)
			return 0, false
		case <-b.ctx.Done():
			return 0, false
		}
	}
}

func (b *termBridge) pingLoop() {
	t := time.NewTicker(b.tm.ping)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			pctx, cancel := context.WithTimeout(b.ctx, b.tm.pingTimeout)
			err := b.conn.Ping(pctx)
			cancel()
			if err != nil && b.ctx.Err() == nil {
				b.cancel(errPeerDead)
				return
			}
		case <-b.ctx.Done():
			return
		}
	}
}

// firstRead calls first on the first Read: the command's side started
// consuming stdin, so it is attached.
type firstRead struct {
	r     io.Reader
	first func()
	once  sync.Once
}

func (f *firstRead) Read(p []byte) (int, error) {
	f.once.Do(f.first)
	return f.r.Read(p)
}

// sizeBox is a latest-wins terminal size queue.
type sizeBox struct {
	mu    sync.Mutex
	cur   provider.TermSize
	fresh bool
	sig   chan struct{}
	done  <-chan struct{}
}

func newSizeBox(initial provider.TermSize) *sizeBox {
	return &sizeBox{cur: initial, fresh: true, sig: make(chan struct{}, 1)}
}

// set queues v; the size the command has or will get anyway is no change
// (the page reports its size again after connecting).
func (s *sizeBox) set(v provider.TermSize) {
	s.mu.Lock()
	if v == s.cur {
		s.mu.Unlock()
		return
	}
	s.cur, s.fresh = v, true
	s.mu.Unlock()
	signal(s.sig)
}

// Next returns the initial size, then the latest after each change; nil
// once the terminal is gone.
func (s *sizeBox) Next() *provider.TermSize {
	for {
		s.mu.Lock()
		if s.fresh {
			s.fresh = false
			v := s.cur
			s.mu.Unlock()
			return &v
		}
		s.mu.Unlock()
		select {
		case <-s.sig:
		case <-s.done:
			return nil
		}
	}
}
