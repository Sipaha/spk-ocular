package engine

import (
	"context"
	"encoding/json"
	"github.com/spk/spk-ocular/internal/provider"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The stream's context ending right at the switch — before its lifetime
// callback is registered — closes the stream (the callback runs at once,
// possibly before StartExec returns) without a race or a panic.
func TestExecContextEndsAtTheSwitch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("Api-Version", MaxAPIVersion)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n$ ")
		_ = brw.Flush()
		_, _ = io.Copy(io.Discard, brw) // until the client closes
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{Host: "tcp://" + strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	t.Cleanup(func() { testHookSwitched = nil })
	for range 30 {
		ctx, cancel := context.WithCancel(t.Context())
		testHookSwitched = cancel
		x, err := c.StartExec(ctx, "e1", true, ConsoleSize{})
		testHookSwitched = nil
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _, _ = io.ReadAll(x); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("the stream outlived its ended context")
		}
		_ = x.Close()
	}
}

// A transport without a write-side close (e.g. a byte-mode named pipe)
// may stage exec metadata, but must never start a finite-input command.
func TestExecFiniteStdinRefusesTransportWithoutHalfClose(t *testing.T) {
	var attached bool
	var starts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("Api-Version", MaxAPIVersion)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/start") {
			starts++
			t.Error("command must not start")
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		attached, _ = body["AttachStdin"].(bool)
		_, _ = io.WriteString(w, `{"Id":"staged-only"}`)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{Host: "tcp://" + strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	original := c.tr.DialContext
	c.tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := original(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return withoutHalfClose{Conn: conn}, nil
	}
	_, err = c.CreateExec(t.Context(), "c", ExecConfig{Cmd: []string{"cat"}, RequireStdinEOF: true})
	if err == nil || ClassOf(err) != provider.ClassUnsupported {
		t.Fatalf("wanted refusal before start, got %v", err)
	}
	if !attached || starts != 0 {
		t.Fatalf("attached=%v starts=%d", attached, starts)
	}
	_, err = c.CreateExec(t.Context(), "c", ExecConfig{Cmd: []string{"true"}, NoStdin: true})
	if err != nil {
		t.Fatal(err)
	}
	if attached {
		t.Fatal("no-input commands must leave stdin detached")
	}
}

type withoutHalfClose struct{ net.Conn }
