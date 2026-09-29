package streams

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
)

type fixture struct {
	reg *Registry
	h   *Handler
	srv *httptest.Server
}

func newFixture(t *testing.T, o HandlerOptions) *fixture {
	t.Helper()
	reg := NewRegistry()
	h := NewHandler(reg, o)
	h.nudge, h.beat = time.Hour, time.Hour // no pings unless a test asks
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(reg.Close)
	return &fixture{reg: reg, h: h, srv: srv}
}

func (f *fixture) get(t *testing.T, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+path, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func frames(t *testing.T, r io.Reader) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), sc.Text())
		out = append(out, m)
	}
	return out
}

func lines(n int) func(ctx context.Context, out *Writer) error {
	return func(_ context.Context, out *Writer) error {
		s := LogSink{W: out}
		if err := s.Source(1, "k", "pod/c", "c"); err != nil {
			return err
		}
		for i := range n {
			if err := s.Lines(1, []provider.LogLine{{TS: "t", Text: strings.Repeat("x", i%7) + "<&>"}}); err != nil {
				return err
			}
		}
		return s.Ready()
	}
}

func TestStreamDeliversFramesAndEndsDone(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	id, err := f.reg.Add("o", lines(5000))
	require.NoError(t, err)
	resp := f.get(t, "/"+f.h.Token()+"/logs/"+id, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	fr := frames(t, resp.Body)
	require.Len(t, fr, 1+5000+2)
	assert.Equal(t, "source", fr[0]["k"])
	assert.Equal(t, []any{"t", "<&>"}, fr[1]["l"].([]any)[0], "no HTML escaping, frames intact")
	assert.Equal(t, "ready", fr[5001]["k"])
	assert.Equal(t, map[string]any{"k": "end", "reason": "done"}, fr[5002])
	assert.Equal(t, 0, f.reg.Len(), "a finished stream is forgotten")
}

func TestStreamIDIsSingleUse(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	id, err := f.reg.Add("o", lines(1))
	require.NoError(t, err)
	resp := f.get(t, "/"+f.h.Token()+"/logs/"+id, nil)
	_, _ = io.Copy(io.Discard, resp.Body)
	assert.Equal(t, http.StatusGone, f.get(t, "/"+f.h.Token()+"/logs/"+id, nil).StatusCode)
}

func TestRegisteredStreamExpiresWithoutWork(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	now := time.Now()
	f.reg.now = func() time.Time { return now }
	ran := false
	id, err := f.reg.Add("o", func(context.Context, *Writer) error { ran = true; return nil })
	require.NoError(t, err)
	assert.Equal(t, 1, f.reg.Len())
	now = now.Add(connectTTL + time.Second)
	assert.Equal(t, 0, f.reg.Len())
	assert.Equal(t, http.StatusGone, f.get(t, "/"+f.h.Token()+"/logs/"+id, nil).StatusCode)
	assert.False(t, ran, "no work for a stream nobody connected to")
}

func TestRejectionsDoNotConsumeTheStream(t *testing.T) {
	f := newFixture(t, HandlerOptions{AllowOrigin: "wails://localhost"})
	id, err := f.reg.Add("o", lines(1))
	require.NoError(t, err)
	tok := f.h.Token()
	for name, c := range map[string]struct {
		path   string
		hdr    map[string]string
		status int
	}{
		"wrong token":                   {"/" + strings.Repeat("A", len(tok)) + "/logs/" + id, nil, http.StatusNotFound},
		"no token":                      {"/logs/" + id, nil, http.StatusNotFound},
		"other origin":                  {"/" + tok + "/logs/" + id, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		"wails://wails is not our page": {"/" + tok + "/logs/" + id, map[string]string{"Origin": "wails://wails"}, http.StatusForbidden},
		"unknown route":                 {"/" + tok + "/exec/" + id, nil, http.StatusNotFound},
	} {
		assert.Equal(t, c.status, f.get(t, c.path, c.hdr).StatusCode, name)
	}
	resp := f.get(t, "/"+tok+"/logs/"+id, map[string]string{"Origin": "wails://localhost"})
	require.Equal(t, http.StatusOK, resp.StatusCode, "still connectable after the rejections")
	assert.Equal(t, "wails://localhost", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestBrowserModeAllowsItsOwnOriginOnly(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	id, err := f.reg.Add("o", lines(1))
	require.NoError(t, err)
	host := strings.TrimPrefix(f.srv.URL, "http://")
	assert.Equal(t, http.StatusForbidden, f.get(t, "/"+f.h.Token()+"/logs/"+id, map[string]string{"Origin": "wails://localhost"}).StatusCode)
	resp := f.get(t, "/"+f.h.Token()+"/logs/"+id, map[string]string{"Origin": "http://" + host})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"), "same origin: no CORS")
}

func TestCloseOwnerEndsConnectedStreamsGoneAndDropsRegistered(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	started := make(chan struct{})
	live, err := f.reg.Add("o", func(ctx context.Context, _ *Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	require.NoError(t, err)
	pending, err := f.reg.Add("o", lines(1))
	require.NoError(t, err)
	other, err := f.reg.Add("other", lines(1))
	require.NoError(t, err)

	resp := f.get(t, "/"+f.h.Token()+"/logs/"+live, nil)
	<-started
	f.reg.CloseOwner("o")
	fr := frames(t, resp.Body)
	assert.Equal(t, map[string]any{"k": "end", "reason": "gone"}, fr[len(fr)-1])
	assert.Equal(t, http.StatusGone, f.get(t, "/"+f.h.Token()+"/logs/"+pending, nil).StatusCode)
	assert.Equal(t, http.StatusOK, f.get(t, "/"+f.h.Token()+"/logs/"+other, nil).StatusCode, "other owners untouched")
}

func TestConnectRacingCloseOwnerNeverRunsAfterIt(t *testing.T) {
	for range 200 {
		reg := NewRegistry()
		var ran sync.WaitGroup
		ran.Add(1)
		id, err := reg.Add("o", nil)
		require.NoError(t, err)
		go func() { defer ran.Done(); reg.CloseOwner("o") }()
		s, ctx, done, err := reg.connect(context.Background(), id)
		ran.Wait()
		if err == nil {
			assert.Error(t, ctx.Err(), "connected before the close: canceled by it")
			assert.True(t, reg.wasRevoked(s))
			done()
		} else {
			assert.ErrorIs(t, err, ErrGone)
		}
	}
}

func TestStreamErrorEndsWithClass(t *testing.T) {
	f := newFixture(t, HandlerOptions{Classify: func(err error) (string, string) { return "forbidden", err.Error() }})
	id, err := f.reg.Add("o", func(context.Context, *Writer) error { return errors.New("pods/log is forbidden") })
	require.NoError(t, err)
	fr := frames(t, f.get(t, "/"+f.h.Token()+"/logs/"+id, nil).Body)
	assert.Equal(t, map[string]any{"k": "end", "reason": "error", "class": "forbidden", "message": "pods/log is forbidden"}, fr[len(fr)-1])
}

func TestLimitCountsRegisteredAndConnected(t *testing.T) {
	reg := NewRegistry()
	for range MaxStreams {
		_, err := reg.Add("o", nil)
		require.NoError(t, err)
	}
	_, err := reg.Add("o", nil)
	assert.ErrorIs(t, err, ErrLimit)
	assert.Equal(t, map[string]int{"o": MaxStreams}, reg.Owners())
}

func TestPageClosingCancelsTheStream(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	started, ended := make(chan struct{}), make(chan struct{})
	id, err := f.reg.Add("o", func(ctx context.Context, out *Writer) error {
		_ = out.Frame(map[string]string{"k": "ping"})
		_ = out.Flush()
		close(started)
		<-ctx.Done()
		close(ended)
		return ctx.Err()
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+"/"+f.h.Token()+"/logs/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	<-started
	cancel() // the tab is closed: fetch aborted
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not see the page go away")
	}
}

// A page that stops reading must not keep a stream (and its upstream
// requests) forever: writes have a deadline.
func TestStalledReaderIsDisconnected(t *testing.T) {
	f := newFixture(t, HandlerOptions{})
	f.h.writeTimeout = 300 * time.Millisecond
	ended := make(chan error, 1)
	id, err := f.reg.Add("o", func(_ context.Context, out *Writer) error {
		big := strings.Repeat("y", 60<<10)
		for {
			if err := out.Frame(map[string]string{"k": "lines", "x": big}); err != nil {
				ended <- err
				return err
			}
		}
	})
	require.NoError(t, err)
	conn, err := net.Dial("tcp", strings.TrimPrefix(f.srv.URL, "http://"))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET /"+f.h.Token()+"/logs/"+id+" HTTP/1.1\r\nHost: x\r\n\r\n")
	require.NoError(t, err)
	// never read
	select {
	case err := <-ended:
		assert.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled reader blocked the producer forever")
	}
	require.Eventually(t, func() bool { return f.reg.Len() == 0 }, 5*time.Second, 20*time.Millisecond)
}

// WebKitGTK withholds a burst's tail until more bytes arrive: a flush that
// goes quiet is followed by a small ping, then heartbeats.
func TestQuietStreamIsNudgedThenHeartbeats(t *testing.T) {
	var buf safeBuffer
	w := newWriter(&buf, func() error { return nil }, nil)
	w.nudge, w.beat = 30*time.Millisecond, 80*time.Millisecond
	defer w.close()
	require.NoError(t, w.Frame(map[string]int{"i": 1}))
	require.NoError(t, w.Flush())
	assert.Equal(t, `{"i":1}`+"\n", buf.String(), "nothing but the frame right after the flush")
	require.Eventually(t, func() bool { return strings.Count(buf.String(), `"ping"`) >= 3 }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, w.Frame(map[string]int{"i": 2}))
	require.NoError(t, w.Flush())
	assert.Contains(t, buf.String(), `{"i":2}`)
}

func TestFramesCoalesceAndNeverSplit(t *testing.T) {
	var buf safeBuffer
	var flushes int
	var mu sync.Mutex
	w := newWriter(&buf, func() error { mu.Lock(); flushes++; mu.Unlock(); return nil }, nil)
	w.nudge = time.Hour
	for i := range 100 {
		require.NoError(t, w.Frame(map[string]int{"i": i}))
	}
	assert.Empty(t, buf.String(), "small frames wait for the coalescing timer")
	require.Eventually(t, func() bool { return strings.Count(buf.String(), "\n") == 100 }, time.Second, 5*time.Millisecond)
	mu.Lock()
	assert.Equal(t, 1, flushes, "one burst, one network flush")
	mu.Unlock()
	big := strings.Repeat("z", flushSize)
	require.NoError(t, w.Frame(map[string]string{"b": big}))
	assert.True(t, strings.HasSuffix(buf.String(), "}\n"), "an oversized burst is flushed whole at once")
	w.close()
	assert.ErrorIs(t, w.Frame(1), errWriterClosed)
}

func TestLoopbackAnswersOnlyItsExactHost(t *testing.T) {
	reg := NewRegistry()
	lb := NewLoopback(NewHandler(reg, HandlerOptions{AllowOrigin: "wails://localhost"}))
	base, err := lb.Base()
	require.NoError(t, err)
	again, _ := lb.Base()
	assert.Equal(t, base, again, "one server per run")
	id, err := reg.Add("o", lines(1))
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, base+"/logs/"+id, nil)
	req.Host = "localhost" + strings.TrimPrefix(strings.Split(strings.TrimPrefix(base, "http://"), "/")[0], "127.0.0.1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "DNS rebinding defense: only 127.0.0.1:<port>")

	resp, err = http.Get(base + "/logs/" + id)
	require.NoError(t, err)
	fr := frames(t, resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "end", fr[len(fr)-1]["k"])

	require.NoError(t, lb.Close())
	_, err = lb.Base()
	assert.Error(t, err)
	_, err = http.Get(base + "/logs/x")
	assert.Error(t, err, "closed")
}

func TestSaveNeedsAnAllowedOriginAndWritesUnique(t *testing.T) {
	dir := t.TempDir()
	f := newFixture(t, HandlerOptions{AllowOrigin: "wails://localhost", SaveDir: func() (string, error) { return dir, nil }})
	post := func(origin, name, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/"+f.h.Token()+"/save?name="+name, strings.NewReader(body))
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	assert.Equal(t, http.StatusForbidden, post("", "a.log", "x").StatusCode, "a simple cross-site POST without Origin")
	assert.Equal(t, http.StatusForbidden, post("https://evil.example", "a.log", "x").StatusCode)
	resp := post("wails://localhost", "..%2F..%2Fa.log", "hello")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, dir+"/a.log", out["path"])
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
