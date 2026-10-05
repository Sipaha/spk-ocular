package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

const (
	// cursorRing: timestamps of the last delivered lines kept per source to
	// skip the replay after a reconnect (sinceTime has second precision).
	cursorRing = 10_000
	// allTail is what "all lines" means: the UI keeps at most 50k per tab.
	allTail = 50_000
	// startupTail bounds the backlog of a new container instance (a restart
	// or a pod discovered later), fetched from its start.
	startupTail = 10_000
	// A batch goes to the sink at this size, or as soon as nothing more is
	// already buffered (a quiet pod's lines are never held back).
	batchLines = 500
	batchBytes = 64 << 10
	// maxLogRequests bounds pods/log requests open at once in the app (8
	// tabs × 20 container streams would be 160).
	maxLogRequests = 64
	retryFirst     = time.Second
	retryCap       = 10 * time.Second
)

// cursor remembers what one container instance has delivered, in file order.
type cursor struct {
	ring  []string // timestamps, circular
	start int
	n     int
	lost  bool // the ring overflowed at least once
}

func (c *cursor) reset() { *c = cursor{} }

func (c *cursor) add(ts string) {
	if c.ring == nil {
		c.ring = make([]string, cursorRing)
	}
	if c.n < len(c.ring) {
		c.ring[(c.start+c.n)%len(c.ring)] = ts
		c.n++
		return
	}
	c.ring[c.start] = ts
	c.start = (c.start + 1) % len(c.ring)
	c.lost = true
}

func (c *cursor) at(i int) string { return c.ring[(c.start+i)%len(c.ring)] }

func (c *cursor) last() string {
	if c.n == 0 {
		return ""
	}
	return c.at(c.n - 1)
}

// replay plans a resume: the kubelet answers sinceTime (whole seconds) with
// every line of the file whose timestamp is ≥ it, in file order — so the
// lines already delivered with such timestamps come first and are skipped
// by count, each checked against its recorded timestamp. covered is false
// when some of them may have fallen out of the ring.
func (c *cursor) replay(cutoff time.Time) (threshold time.Time, expect []string, covered bool) {
	threshold = tsTime(c.last()).Truncate(time.Second)
	if cutoff.After(threshold) {
		threshold = cutoff
	}
	covered = true
	for i := 0; i < c.n; i++ {
		ts := c.at(i)
		if !tsTime(ts).Before(threshold) {
			if i == 0 && c.lost {
				covered = false
			}
			expect = append(expect, ts)
		}
	}
	return threshold, expect, covered
}

// skipper drops the replayed prefix of a resumed stream. The first line
// that does not match what was delivered ends skipping: everything from
// there is shown (a possible repeat beats a possible loss) and the seam is
// reported as a gap.
type skipper struct {
	expect   []string
	i        int
	done     bool
	mismatch bool
	reported bool // the gap was announced
}

func (s *skipper) drop(l provider.LogLine) bool {
	if s == nil || s.done {
		return false
	}
	if s.i >= len(s.expect) {
		s.done = true
		return false
	}
	if l.TS != "" && l.TS == s.expect[s.i] {
		s.i++
		return true
	}
	s.done, s.mismatch = true, true
	return false
}

// clean: the replay matched what was delivered (as far as it went).
func (s *skipper) clean(ended bool) bool {
	if s == nil {
		return true
	}
	return !s.mismatch && (s.done || !ended || s.i == len(s.expect))
}

// podSource streams one container of one pod.
type podSource struct {
	id                int
	ns, pod, uid, ctr string
	fetch             logFetcher
	slots             chan struct{} // nil: unlimited
	box               *obsBox       // follow only; nil for one-shot requests
	sink              provider.LogSink
	q                 provider.LogQuery
	cur               cursor
	incarnation       string // container ID the cursor belongs to
	opened            bool   // a request was answered
	// late: the source joined after the stream began (a new pod, a freed
	// slot): its first request reads the instance from its start.
	late                 bool
	sent                 provider.LogState
	retryFirst, retryCap time.Duration
}

func newPodSource(id int, ns, pod, uid, ctr string, fetch logFetcher, box *obsBox, sink provider.LogSink, q provider.LogQuery) *podSource {
	return &podSource{id: id, ns: ns, pod: pod, uid: uid, ctr: ctr, fetch: fetch, box: box, sink: sink, q: q, retryFirst: retryFirst, retryCap: retryCap}
}

// staleIncarnation: the instance read is known to be older than the
// observed one (never equal to a container ID).
const staleIncarnation = "\x00stale"

// open takes one of the app's log request slots for the life of the
// response (waiting, and saying so, while all are taken).
func (s *podSource) open(ctx context.Context, req podLogRequest) (io.ReadCloser, error) {
	if s.slots != nil {
		select {
		case s.slots <- struct{}{}:
		default:
			if err := s.state(provider.LogWaiting, "", fmt.Sprintf("%d log streams are open at once; waiting for one to close", cap(s.slots))); err != nil {
				return nil, err
			}
			select {
			case s.slots <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	rc, err := s.fetch(ctx, req)
	if err != nil {
		s.release()
		return nil, err
	}
	return &slotBody{ReadCloser: rc, release: s.release}, nil
}

func (s *podSource) release() {
	if s.slots != nil {
		<-s.slots
	}
}

type slotBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *slotBody) Close() error {
	b.once.Do(b.release)
	return b.ReadCloser.Close()
}

// errSourceDone ends a source whose final state was reported.
var errSourceDone = errors.New("source finished")

func (s *podSource) state(state string, class provider.ErrorClass, msg string) error {
	st := provider.LogState{State: state, Class: class, Message: msg}
	if st == s.sent {
		return nil
	}
	s.sent = st
	return s.sink.State(s.id, st)
}

// finish reports a terminal state; run then returns nil.
func (s *podSource) finish(state string, class provider.ErrorClass, msg string) error {
	if err := s.state(state, class, msg); err != nil {
		return err
	}
	return errSourceDone
}

// run streams until ctx ends, the sink fails (the page went away: that
// error is returned) or the source is finished (nil).
func (s *podSource) run(ctx context.Context) error {
	var err error
	if !s.q.Follow {
		err = s.once(ctx)
	} else {
		err = s.follow(ctx)
	}
	if errors.Is(err, errSourceDone) {
		return nil
	}
	return err
}

func tailOf(q provider.LogQuery) int64 {
	if q.Archive {
		return 0
	}
	if q.TailLines == provider.TailAll || q.TailLines > allTail {
		return allTail
	}
	return int64(q.TailLines)
}

// once: a complete, non-follow answer (current or previous instance).
func (s *podSource) once(ctx context.Context) error {
	// A finite answer has a deadline (a follow does not): it must not hang
	// after its headers.
	octx, cancel := context.WithTimeout(ctx, onceTimeout)
	defer cancel()
	rc, err := s.open(octx, podLogRequest{
		Namespace: s.ns, Pod: s.pod, Container: s.ctr, Previous: s.q.Previous,
		TailLines: tailOf(s.q), SinceTime: s.q.SinceTime,
	})
	if err != nil {
		return s.requestFailed(ctx, err)
	}
	defer func() { _ = rc.Close() }()
	n, readErr, err := s.pump(octx, rc, nil)
	switch {
	case err != nil && ctx.Err() == nil && octx.Err() != nil:
		return s.finish(provider.LogError, provider.ClassUnavailable, fmt.Sprintf("the answer did not complete within %s; %d lines arrived", onceTimeout, n))
	case err != nil:
		return err
	case readErr != nil && !errors.Is(readErr, io.EOF):
		return s.finish(provider.LogError, provider.ClassUnavailable, fmt.Sprintf("the answer broke off after %d lines: %v", n, readErr))
	}
	return s.finish(provider.LogEnded, "", "")
}

// onceTimeout bounds a non-follow answer (a var for tests).
var onceTimeout = 2 * time.Minute

// requestFailed reports a failed request that cannot be retried.
func (s *podSource) requestFailed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	class, msg := classify(err)
	switch kindOfLogError(err) {
	case logErrNoPrev:
		return s.finish(provider.LogEnded, provider.ClassNotFound, "there is no previous instance of this container")
	case logErrWaiting:
		return s.finish(provider.LogWaiting, "", msg)
	case logErrForbidden:
		return s.finish(provider.LogError, provider.ClassForbidden, msg)
	case logErrNotFound:
		return s.finish(provider.LogEnded, provider.ClassNotFound, "the pod was deleted")
	case logErrBadCtr:
		return s.finish(provider.LogError, provider.ClassNotFound, msg)
	}
	return s.finish(provider.LogError, class, msg)
}

// firstSyncWait: the first request waits this long at most for the pod
// observation, so the instance it reads is known (else a later restart
// could not be told from the same file).
var firstSyncWait = 3 * time.Second

func (s *podSource) follow(ctx context.Context) error {
	backoff := s.retryFirst
	if err := s.awaitFirstSync(ctx); err != nil {
		return err
	}
	for {
		obs, synced, blind, changed := s.box.get()
		tracked := synced && blind == ""
		var c ctrObs
		if tracked {
			if err := s.checkPod(obs); err != nil {
				return err
			}
			c = obs.ctrs[s.ctr]
			if c.id == "" && !c.running { // never started (yet)
				reason := c.waiting
				if reason == "" {
					reason = "the container has not started yet"
				}
				if err := s.state(provider.LogWaiting, "", reason); err != nil {
					return err
				}
				if err := wait(ctx, changed, 0); err != nil {
					return err
				}
				continue
			}
		}
		req, sk := s.plan(c, tracked)
		rc, err := s.open(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch kindOfLogError(err) {
			case logErrForbidden, logErrNotFound, logErrBadCtr:
				return s.requestFailed(ctx, err)
			case logErrWaiting:
				if !tracked {
					return s.finish(provider.LogWaiting, "", err.Error()+" — restart tracking is unavailable ("+blindReason(blind)+"); reopen to continue")
				}
				if err := s.state(provider.LogWaiting, "", statusMessage(err)); err != nil {
					return err
				}
				if err := wait(ctx, changed, 0); err != nil {
					return err
				}
				continue
			}
			class, msg := classify(err)
			if err := s.state(provider.LogError, class, msg); err != nil {
				return err
			}
			if err := wait(ctx, changed, jitter(backoff)); err != nil {
				return err
			}
			backoff = min(backoff*2, s.retryCap)
			continue
		}
		s.opened = true
		if err := s.state(provider.LogStreaming, "", ""); err != nil {
			_ = rc.Close()
			return err
		}
		n, _, err := s.pump(ctx, rc, sk) // any end of a follow: decide below
		_ = rc.Close()
		if err != nil {
			return err
		}
		if !sk.clean(true) && !sk.reported {
			// the replay ended before matching what was delivered
			if err := s.gap(); err != nil {
				return err
			}
		}
		if n > 0 {
			backoff = s.retryFirst
		}
		if err := s.afterEOF(ctx, &backoff); err != nil {
			return err
		}
	}
}

func (s *podSource) awaitFirstSync(ctx context.Context) error {
	limit := time.NewTimer(firstSyncWait)
	defer limit.Stop()
	for {
		_, synced, blind, changed := s.box.get()
		if synced || blind != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-limit.C:
			return nil
		case <-changed:
		}
	}
}

func blindReason(b string) string {
	if b == "" {
		return "the pod list is not available yet"
	}
	return b
}

// checkPod ends the source when its pod is gone or replaced.
func (s *podSource) checkPod(obs podObs) error {
	if !obs.exists {
		return s.finish(provider.LogEnded, "", "the pod was deleted")
	}
	if s.uid != "" && obs.uid != s.uid {
		return s.finish(provider.LogEnded, "", "the pod was replaced by a new one with the same name")
	}
	if _, ok := obs.ctrs[s.ctr]; !ok {
		return s.finish(provider.LogError, provider.ClassNotFound, fmt.Sprintf("the pod has no container %q", s.ctr))
	}
	return nil
}

// afterEOF decides what a finished response means, from the latest
// observation (never from "the next event": it may have come already).
func (s *podSource) afterEOF(ctx context.Context, backoff *time.Duration) error {
	for {
		obs, synced, blind, changed := s.box.get()
		if blind != "" {
			return s.finish(provider.LogEnded, "", "the log stream ended; restart tracking is unavailable ("+blind+"); reopen to continue")
		}
		if !synced { // the pod list is still loading: decide when it is there
			if err := wait(ctx, changed, jitter(*backoff)); err != nil {
				return err
			}
			*backoff = min(*backoff*2, s.retryCap)
			continue
		}
		if err := s.checkPod(obs); err != nil {
			return err
		}
		c := obs.ctrs[s.ctr]
		if s.incarnation == "" && c.id != "" {
			// The first request went out before the pod was observed: the
			// instance read was the observed one unless that one started
			// after the last line read.
			if c.running && s.cur.last() != "" && tsTime(s.cur.last()).Before(c.startedAt.Add(-time.Second)) {
				s.incarnation = staleIncarnation
			} else {
				s.incarnation = c.id
			}
		}
		switch {
		case c.id != "" && s.incarnation != "" && c.id != s.incarnation:
			return nil // a newer instance is already known: open it now
		case c.running:
			// The same instance still runs: the connection broke (or the
			// cache has not seen the exit yet — a change wakes us early).
			if err := wait(ctx, changed, jitter(*backoff)); err != nil {
				return err
			}
			*backoff = min(*backoff*2, s.retryCap)
			return nil
		case obs.restartExpected(c):
			if err := s.state(provider.LogWaiting, "", fmt.Sprintf("the container exited (code %d); waiting for it to restart", c.exitCode)); err != nil {
				return err
			}
			if err := wait(ctx, changed, 0); err != nil {
				return err
			}
			// loop: re-decide on the new observation
		default:
			return s.finish(provider.LogEnded, "", fmt.Sprintf("the container finished (exit code %d)", c.exitCode))
		}
	}
}

// plan builds the next follow request. The first one uses the user's
// window; a reconnect to the same instance resumes after the cursor; a new
// instance is read from its start (bounded), never from "when we noticed it".
func (s *podSource) plan(c ctrObs, tracked bool) (podLogRequest, *skipper) {
	req := podLogRequest{Namespace: s.ns, Pod: s.pod, Container: s.ctr, Follow: true, SinceTime: s.q.SinceTime}
	if !s.opened {
		s.incarnation = c.id
		req.TailLines = tailOf(s.q)
		if s.late {
			req.TailLines = startupTail
		}
		return req, nil
	}
	// An unknown instance (the first request went out before the pod was
	// observed) is resumed as the same file; the skipper checks it.
	same := !tracked || s.incarnation == "" || c.id == s.incarnation
	if !same && c.running && s.cur.last() != "" && !tsTime(s.cur.last()).Before(c.startedAt) {
		// The last request already reached this instance (the cache saw the
		// restart after we did): same file, resume.
		same = true
	}
	if tracked {
		s.incarnation = c.id
	}
	if same && s.cur.last() != "" {
		thr, expect, covered := s.cur.replay(s.q.SinceTime)
		req.SinceTime = thr
		return req, &skipper{expect: expect, mismatch: !covered}
	}
	if same {
		req.TailLines = tailOf(s.q) // nothing delivered yet: the same window again
		return req, nil
	}
	s.cur.reset()
	req.TailLines = startupTail
	return req, nil
}

// gap tells the page that lines around a reconnect may be missing or
// repeated; the next state is sent again after it.
func (s *podSource) gap() error {
	s.sent = provider.LogState{}
	return s.sink.State(s.id, provider.LogState{State: provider.LogGap, Message: "lines around the reconnect may be missing or repeated (the log file was rotated?)"})
}

// pump reads one response into the sink. It returns the number of lines
// delivered, how the response ended (readErr: io.EOF when clean) and an
// error only when the sink or ctx failed.
func (s *podSource) pump(ctx context.Context, rc io.Reader, sk *skipper) (n int, readErr, err error) {
	lr := newLineReader(rc)
	var batch []provider.LogLine
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.sink.Lines(s.id, batch)
		batch, size = nil, 0
		return err
	}
	for {
		line, complete, rerr := lr.next()
		// A last fragment without a newline is a line when the response
		// ended cleanly (the file ends so); after a broken read it is not
		// (the next request replays it).
		if complete || (errors.Is(rerr, io.EOF) && (line.Text != "" || line.TS != "")) {
			dropped := sk.drop(line)
			if sk != nil && sk.mismatch && !sk.reported {
				sk.reported = true
				if err := flush(); err != nil {
					return n, nil, err
				}
				if err := s.gap(); err != nil {
					return n, nil, err
				}
				if err := s.state(provider.LogStreaming, "", ""); err != nil {
					return n, nil, err
				}
			}
			if !dropped {
				if line.TS != "" && !s.q.Archive {
					s.cur.add(line.TS)
				}
				batch = append(batch, line)
				size += len(line.Text)
				n++
			}
		}
		if rerr != nil {
			if err := flush(); err != nil {
				return n, rerr, err
			}
			if ctx.Err() != nil {
				return n, rerr, ctx.Err()
			}
			return n, rerr, nil
		}
		if len(batch) >= batchLines || size >= batchBytes || lr.buffered() == 0 {
			if err := flush(); err != nil {
				return n, nil, err
			}
		}
	}
}

// wait blocks until ctx ends, changed fires (if not nil) or d passes (if > 0).
func wait(ctx context.Context, changed <-chan struct{}, d time.Duration) error {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-changed:
	case <-timer:
	}
	return nil
}

func jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1) //nolint:gosec // backoff jitter
}
