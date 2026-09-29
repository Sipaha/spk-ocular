package kubernetes

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gwebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	pfconst "k8s.io/apimachinery/pkg/util/portforward"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"
)

// pfServer is an API server + kubelet stand-in for pods/portforward: it
// speaks SPDY directly (POST) and tunnelled in a WebSocket (GET), pairs
// the error and data streams of each request id, and connects the data to
// a local backend per remote port. A port without a backend gets
// kubelet's error on the error stream.
type pfServer struct {
	srv *httptest.Server

	mu       sync.Mutex
	backends map[int]string  // remote port → address
	noWS     bool            // refuse the WebSocket upgrade (older server)
	dead     map[string]bool // pods whose sandbox is gone: every stream fails
	pods     []string        // pods forwarded to, in order
	methods  []string
	ids      map[string]bool // request ids seen per connection
	dupIDs   int
	conns    []streamhttp.Connection
	// holdErrs, when set, keeps each error stream open (no report, no
	// end) until it is closed.
	holdErrs chan struct{}
}

func newPFServer(t *testing.T) *pfServer {
	t.Helper()
	s := &pfServer{backends: map[int]string{}, ids: map[string]bool{}, dead: map[string]bool{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(func() {
		s.srv.CloseClientConnections()
		s.srv.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
	})
	return s
}

func (s *pfServer) backend(port int, addr string) {
	s.mu.Lock()
	s.backends[port] = addr
	s.mu.Unlock()
}

func (s *pfServer) forwarded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.pods...)
}

func (s *pfServer) serve(w http.ResponseWriter, r *http.Request) {
	// /api/v1/namespaces/<ns>/pods/<pod>/portforward
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 7 || parts[6] != "portforward" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	noWS := s.noWS
	s.mu.Unlock()
	pod := parts[5]
	switch r.Method {
	case http.MethodGet:
		if noWS {
			http.Error(w, "websockets are not supported here", http.StatusBadRequest)
			return
		}
		up := gwebsocket.Upgrader{Subprotocols: []string{pfconst.WebsocketsSPDYTunnelingPortForwardV1}}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.record(pod, "ws")
		tc := portforward.NewTunnelingConnection("server", ws)
		c, err := streamspdy.NewServerConnection(tc, s.pairs(pod))
		if err != nil {
			_ = tc.Close()
			return
		}
		s.keep(c)
	case http.MethodPost:
		if _, err := streamhttp.Handshake(r, w, []string{pfProtocol}); err != nil {
			return
		}
		s.record(pod, "spdy")
		c := streamspdy.NewResponseUpgrader().UpgradeResponse(w, r, s.pairs(pod))
		if c != nil {
			s.keep(c)
		}
	}
}

func (s *pfServer) record(pod, method string) {
	s.mu.Lock()
	s.pods = append(s.pods, pod)
	s.methods = append(s.methods, method)
	s.mu.Unlock()
}

func (s *pfServer) keep(c streamhttp.Connection) {
	s.mu.Lock()
	s.conns = append(s.conns, c)
	s.mu.Unlock()
}

// pairs returns a stream handler that serves each (error, data) pair.
func (s *pfServer) pairs(pod string) streamhttp.NewStreamHandler {
	var mu sync.Mutex
	pending := map[string]streamhttp.Stream{}
	return func(st streamhttp.Stream, replySent <-chan struct{}) error {
		h := st.Headers()
		id := h.Get(corev1.PortForwardRequestIDHeader)
		typ := h.Get(corev1.StreamType)
		mu.Lock()
		other, ok := pending[id]
		if !ok {
			pending[id] = st
			mu.Unlock()
			s.mu.Lock()
			if typ == corev1.StreamTypeError {
				if s.ids[fmt.Sprintf("%p/%s", pending, id)] {
					s.dupIDs++
				}
				s.ids[fmt.Sprintf("%p/%s", pending, id)] = true
			}
			s.mu.Unlock()
			return nil
		}
		delete(pending, id)
		mu.Unlock()
		errs, data := other, st
		if typ == corev1.StreamTypeError {
			errs, data = st, other
		}
		port, _ := strconv.Atoi(h.Get(corev1.PortHeader))
		go func() {
			<-replySent
			s.forward(pod, port, errs, data)
		}()
		return nil
	}
}

// forward is kubelet's side of one connection.
func (s *pfServer) forward(pod string, port int, errs, data streamhttp.Stream) {
	s.mu.Lock()
	addr, dead, hold := s.backends[port], s.dead[pod], s.holdErrs
	s.mu.Unlock()
	defer func() {
		if hold != nil {
			<-hold
		}
		_ = errs.Close()
	}()
	defer func() { _ = data.Close() }()
	if dead { // kubelet keeps the connection and fails the streams
		_, _ = fmt.Fprintf(errs, "failed to find sandbox for pod %q", pod)
		return
	}
	var c net.Conn
	var err error
	if addr != "" {
		c, err = net.DialTimeout("tcp4", addr, 5*time.Second)
	}
	if addr == "" || err != nil {
		_, _ = fmt.Fprintf(errs, "error forwarding port %d to pod: connection refused", port)
		return
	}
	defer func() { _ = c.Close() }()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(c, data)
		_ = c.(*net.TCPConn).CloseWrite()
		close(done)
	}()
	_, _ = io.Copy(data, c)
	<-done
}

// echoBackend echoes each connection until it half-closes.
func echoBackend(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

// A refused upgrade whose body never completes ends by cancellation, and
// without it by the refusal's own deadline — as the refusal it is.
func TestARefusalWithAStalledBodyEndsByCancelOrDeadline(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	d := &forwardDialer{c: &conn{cfg: &rest.Config{Host: srv.URL}}, deadAfter: time.Second}
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := d.dialSPDY(ctx, u); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not end a refusal with a stalled body")
	}

	old := refusalBodyTimeout
	refusalBodyTimeout = 200 * time.Millisecond
	defer func() { refusalBodyTimeout = old }()
	go func() { _, err := d.dialSPDY(context.Background(), u); done <- err }()
	<-entered
	select {
	case err := <-done:
		assert.True(t, apierrors.IsForbidden(err), "the refusal, not a hang: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("the refusal's body had no deadline")
	}
}
