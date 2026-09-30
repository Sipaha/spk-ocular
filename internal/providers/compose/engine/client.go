// Package engine is a thin Docker Engine API client: the transport of an
// endpoint (unix, tcp, tcp+TLS), a lazily negotiated API version, bounded
// request/response calls, streams (events, logs) with a deadline only until
// their headers, and classified errors. Every request goes on the wire at
// most once: nothing here retries.
package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/spk/spk-ocular/internal/provider"
)

// API versions the client speaks: the path prefix is /v<min(MaxAPIVersion,
// daemon)>; a daemon older than MinAPIVersion is unsupported.
const (
	MaxAPIVersion = "1.54"
	MinAPIVersion = "1.41"
)

// Defaults of Config and Limits.
const (
	DefaultRequestTimeout = 15 * time.Second
	DefaultListBytes      = 64 << 20
	DefaultInspectBytes   = 4 << 20
	DefaultErrorBytes     = 64 << 10
	DefaultEventLineBytes = 1 << 20
	DefaultLogLineBytes   = 256 << 10
	DefaultLogFrameBytes  = 8 << 20
)

// Config says how to reach one Engine endpoint.
type Config struct {
	// Host: unix:///path or tcp://host[:port][/base]. Other schemes
	// (ssh://, npipe://) are unsupported.
	Host string
	// TLS: nil — plaintext (tcp) / a plain socket (unix).
	TLS *TLSConfig
	// RequestTimeout bounds a request/response call as a whole (headers
	// and body) and the version ping. Default DefaultRequestTimeout.
	RequestTimeout time.Duration
	// HeaderTimeout bounds getting a stream's (events, logs) response
	// headers; the body then has no deadline. Default RequestTimeout.
	HeaderTimeout time.Duration
	Limits        Limits
	UserAgent     string
}

// Limits bound what the client reads; exceeding one is an error, never a
// silently truncated answer (log lines are the exception: a longer line
// keeps its beginning and says so). Zero fields take the defaults.
type Limits struct {
	ListBytes      int64 // a list answer
	InspectBytes   int64 // an inspect or /info answer
	ErrorBytes     int64 // an error answer's body
	EventLineBytes int   // one event of the events stream
	LogLineBytes   int   // one log line (longer: Truncated)
	LogFrameBytes  int   // one stdcopy frame (longer: the stream is broken)
}

func (l Limits) withDefaults() Limits {
	if l.ListBytes <= 0 {
		l.ListBytes = DefaultListBytes
	}
	if l.InspectBytes <= 0 {
		l.InspectBytes = DefaultInspectBytes
	}
	if l.ErrorBytes <= 0 {
		l.ErrorBytes = DefaultErrorBytes
	}
	if l.EventLineBytes <= 0 {
		l.EventLineBytes = DefaultEventLineBytes
	}
	if l.LogLineBytes <= 0 {
		l.LogLineBytes = DefaultLogLineBytes
	}
	if l.LogFrameBytes <= 0 {
		l.LogFrameBytes = DefaultLogFrameBytes
	}
	return l
}

// TLSConfig of an endpoint. Each of the CA, the certificate and the key is
// given as PEM bytes or as a file path (not both). The certificate and the
// key come together or not at all: half a pair is an error, never a
// fallback to no client certificate. With a CA only that CA is trusted;
// without one, the system roots.
type TLSConfig struct {
	CAFile, CertFile, KeyFile string
	CA, Cert, Key             []byte
	SkipVerify                bool
}

// Client talks to one Engine endpoint. Safe for concurrent use.
type Client struct {
	cfg      Config
	lim      Limits
	hc       *http.Client
	tr       *http.Transport
	scheme   string // http or https
	host     string // the URL host (a fixed name for unix sockets)
	basePath string

	base   context.Context // ends with Close (the shared ping)
	cancel context.CancelFunc

	mu      sync.Mutex
	version string    // negotiated, "" until the first successful ping
	pinging *pingCall // the ping in flight, shared by concurrent first requests
}

type pingCall struct {
	done    chan struct{}
	version string
	err     error
}

// unixHost is the URL host of unix-socket requests. Not "localhost": a
// proxy would skip that name anyway, and the unix transport has no proxy.
const unixHost = "docker"

var errRedirect = errors.New("redirects are not followed")

// New builds a client; it does not touch the network (the API version is
// negotiated by the first request). Configuration errors — an unsupported
// scheme, an incomplete or unreadable TLS pair — are *Error.
func New(cfg Config) (*Client, error) {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.HeaderTimeout <= 0 {
		cfg.HeaderTimeout = cfg.RequestTimeout
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "spk-ocular"
	}
	c := &Client{cfg: cfg, lim: cfg.Limits.withDefaults()}
	u, err := url.Parse(cfg.Host)
	if err != nil || u.Scheme == "" {
		return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("invalid Docker endpoint %q", cfg.Host)}
	}
	var tlsCfg *tls.Config
	if cfg.TLS != nil {
		if tlsCfg, err = cfg.TLS.build(); err != nil {
			return nil, err
		}
	}
	tr := &http.Transport{
		TLSClientConfig:       tlsCfg,
		MaxIdleConnsPerHost:   16, // the inspect pool (8) and streams
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	c.scheme = "http"
	if tlsCfg != nil {
		c.scheme = "https"
	}
	switch u.Scheme {
	case "unix":
		path := u.Path
		if u.Host != "" { // unix://relative/path
			path = u.Host + u.Path
		}
		if path == "" {
			return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("invalid Docker endpoint %q: no socket path", cfg.Host)}
		}
		d := &net.Dialer{}
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", path)
		}
		tr.Proxy = nil // like the docker CLI: a socket is never proxied
		c.host = unixHost
	case "tcp":
		if u.Hostname() == "" {
			return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("invalid Docker endpoint %q: no host", cfg.Host)}
		}
		c.host = u.Host
		if u.Port() == "" {
			port := "2375"
			if tlsCfg != nil {
				port = "2376"
			}
			c.host = net.JoinHostPort(u.Hostname(), port)
		}
		c.basePath = strings.TrimRight(u.Path, "/")
		d := &net.Dialer{KeepAlive: 30 * time.Second}
		tr.DialContext = d.DialContext
		// Like the docker CLI (http.ProxyFromEnvironment: HTTPS_PROXY,
		// HTTP_PROXY, NO_PROXY), read when the client is built — the
		// stdlib caches the environment for the life of the process.
		pf := httpproxy.FromEnvironment().ProxyFunc()
		tr.Proxy = func(r *http.Request) (*url.URL, error) { return pf(r.URL) }
	case "ssh":
		return nil, unsupported("ssh:// Docker endpoints are not supported yet (they need `docker system dial-stdio`)")
	case "npipe":
		return nil, unsupported("npipe:// Docker endpoints exist only on Windows")
	default:
		return nil, unsupported("the %s:// Docker endpoint scheme is not supported", u.Scheme)
	}
	c.tr = tr
	c.hc = &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}
	c.base, c.cancel = context.WithCancel(context.Background())
	return c, nil
}

// Close ends a ping in flight and drops idle connections. Streams opened
// earlier end with their own Close or context.
func (c *Client) Close() {
	c.cancel()
	c.tr.CloseIdleConnections()
}

// Config is the configuration the client was built with (defaults
// applied): New(c.Config()) is an independent client of the same endpoint.
func (c *Client) Config() Config { return c.cfg }

// Endpoint is where the client connects, without credentials: the socket
// path of a unix endpoint, host:port of a tcp one.
func (c *Client) Endpoint() string {
	if c.host == unixHost {
		u, err := url.Parse(c.cfg.Host)
		if err == nil {
			return u.Host + u.Path
		}
	}
	return c.host
}

// APIVersion is the negotiated version ("" before the first successful ping).
func (c *Client) APIVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (t *TLSConfig) build() (*tls.Config, error) {
	load := func(what, file string, pem []byte) ([]byte, error) {
		switch {
		case file != "" && len(pem) > 0:
			return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("TLS %s is given both as a file and as data", what)}
		case file != "":
			b, err := os.ReadFile(file)
			if err != nil {
				return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("cannot read the TLS %s: %v", what, err), Err: err}
			}
			return b, nil
		}
		return pem, nil
	}
	ca, err := load("CA", t.CAFile, t.CA)
	if err != nil {
		return nil, err
	}
	cert, err := load("certificate", t.CertFile, t.Cert)
	if err != nil {
		return nil, err
	}
	key, err := load("key", t.KeyFile, t.Key)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: t.SkipVerify} //nolint:gosec // the context's own choice
	if len(ca) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, &Error{Class: provider.ClassInvalid, Message: "the TLS CA has no PEM certificate"}
		}
		cfg.RootCAs = pool
	}
	switch {
	case len(cert) > 0 && len(key) > 0:
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("invalid TLS certificate/key pair: %v", err), Err: err}
		}
		cfg.Certificates = []tls.Certificate{pair}
	case len(cert) > 0:
		return nil, &Error{Class: provider.ClassInvalid, Message: "a TLS certificate without its key"}
	case len(key) > 0:
		return nil, &Error{Class: provider.ClassInvalid, Message: "a TLS key without its certificate"}
	case t.CertFile != "" || t.KeyFile != "":
		return nil, &Error{Class: provider.ClassInvalid, Message: "empty TLS certificate/key files"}
	}
	return cfg, nil
}

// apiVersion returns the negotiated version, pinging once if needed.
// Concurrent first requests share one ping; a failed ping is not cached
// (the next request pings again), and the waiting requests fail with it —
// they are not sent.
func (c *Client) apiVersion(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.version != "" {
		v := c.version
		c.mu.Unlock()
		return v, nil
	}
	call := c.pinging
	if call == nil {
		call = &pingCall{done: make(chan struct{})}
		c.pinging = call
		go c.sharedPing(call)
	}
	c.mu.Unlock()
	select {
	case <-call.done:
		return call.version, call.err
	case <-ctx.Done():
		return "", ctxError(ctx)
	}
}

// sharedPing runs on the client's own context: one caller giving up does
// not fail the others waiting for the same ping.
func (c *Client) sharedPing(call *pingCall) {
	p, err := c.ping(c.base)
	if err == nil {
		call.version = p.Version
	}
	call.err = err
	c.mu.Lock()
	if err == nil {
		c.version = p.Version
	}
	c.pinging = nil
	c.mu.Unlock()
	close(call.done)
}

// negotiate picks the version to speak with a daemon reporting daemon.
func negotiate(daemon string) (string, error) {
	d, ok := parseVersion(daemon)
	if !ok {
		return "", unsupported("the Docker Engine reported an unrecognized API version %q", daemon)
	}
	lo, _ := parseVersion(MinAPIVersion)
	hi, _ := parseVersion(MaxAPIVersion)
	if d.less(lo) {
		return "", unsupported("the Docker Engine speaks API %s; the oldest supported is %s", daemon, MinAPIVersion)
	}
	if hi.less(d) {
		return MaxAPIVersion, nil
	}
	return fmt.Sprintf("%d.%d", d.major, d.minor), nil
}

type version struct{ major, minor int }

func (v version) less(o version) bool {
	return v.major < o.major || v.major == o.major && v.minor < o.minor
}

func parseVersion(s string) (version, bool) {
	maj, minor, ok := strings.Cut(s, ".")
	if !ok {
		return version{}, false
	}
	a, ok1 := number(maj)
	b, ok2 := number(minor)
	return version{a, b}, ok1 && ok2
}

func number(s string) (int, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// endpoint builds the URL of an Engine path; versioned unless path is
// unversioned (/_ping).
func (c *Client) endpoint(ver, path string, q url.Values) string {
	u := url.URL{Scheme: c.scheme, Host: c.host, Path: c.basePath + path}
	if ver != "" {
		u.Path = c.basePath + "/v" + ver + path
	}
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// newRequest builds a GET that the transport can never replay. net/http
// resends an idempotent request whose reused connection died before the
// answer (it may have reached the daemon already); a request with a body
// and no GetBody is not "replayable", and an empty in-memory body is sent
// as no body at all. Every call goes on the wire at most once.
func (c *Client) newRequest(ctx context.Context, method, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, &Error{Class: provider.ClassInternal, Message: err.Error(), Err: err}
	}
	req.Body = io.NopCloser(bytes.NewReader(nil))
	req.GetBody = nil
	req.ContentLength = 0
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	return req, nil
}

// transportError classifies a failed round trip or body read of a request
// made on a context derived from parent: parent ending is the caller's
// decision; the derived one ending alone is the client's own deadline.
func transportError(parent, derived context.Context, err error, limit time.Duration) error {
	if errors.Is(err, errRedirect) {
		return &Error{Class: provider.ClassUnsupported, Message: "the endpoint answered with a redirect; redirects are not followed (is it a Docker Engine?)", Err: err}
	}
	if parent.Err() != nil {
		return ctxError(parent)
	}
	if derived.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return deadlineError(limit)
	}
	return &Error{Class: provider.ClassUnavailable, Message: "cannot reach the Docker Engine: " + unwrapURLError(err).Error(), Err: err}
}

func deadlineError(limit time.Duration) error {
	return &Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("the Docker Engine did not answer within %s", limit), Err: context.DeadlineExceeded}
}

// ctxError: the caller's context ended the call.
func ctxError(ctx context.Context) error {
	cause := ctx.Err()
	msg := "the request was canceled"
	if errors.Is(cause, context.DeadlineExceeded) {
		msg = "the request's deadline passed"
	}
	return &Error{Class: provider.ClassUnavailable, Message: msg, Err: cause}
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// get does a bounded request/response call: one deadline over the whole
// exchange, the body capped at limit (more is an error, not a cut answer).
// ver "" is an unversioned path.
func (c *Client) get(parent context.Context, ver, path string, q url.Values, limit int64) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(parent, c.cfg.RequestTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, c.endpoint(ver, path, q))
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, nil, transportError(parent, ctx, err, c.cfg.RequestTimeout)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, c.lim.ErrorBytes))
		return nil, resp.Header, statusError(resp.StatusCode, resp.Status, body)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, transportError(parent, ctx, err, c.cfg.RequestTimeout)
	}
	if int64(len(body)) > limit {
		return nil, nil, &Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("the Docker Engine's answer to %s is larger than %d bytes", path, limit)}
	}
	return body, resp.Header, nil
}

// getVersioned negotiates the version (once per client) and does get.
func (c *Client) getVersioned(ctx context.Context, path string, q url.Values, limit int64) ([]byte, error) {
	ver, err := c.apiVersion(ctx)
	if err != nil {
		return nil, err
	}
	body, _, err := c.get(ctx, ver, path, q, limit)
	return body, err
}

// openStream starts a streaming GET: its deadline covers only the response
// headers (and an error answer's bounded body); a 2xx body then streams
// until the caller's ctx ends or the returned closer is called.
func (c *Client) openStream(parent context.Context, path string, q url.Values) (*http.Response, context.CancelFunc, error) {
	ver, err := c.apiVersion(parent)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	hdr := time.AfterFunc(c.cfg.HeaderTimeout, cancel)
	req, err := c.newRequest(ctx, http.MethodGet, c.endpoint(ver, path, q))
	if err != nil {
		hdr.Stop()
		cancel()
		return nil, nil, err
	}
	resp, err := c.hc.Do(req)
	if !hdr.Stop() {
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		if parent.Err() != nil {
			return nil, nil, ctxError(parent)
		}
		return nil, nil, deadlineError(c.cfg.HeaderTimeout)
	}
	if err != nil {
		cancel()
		return nil, nil, transportError(parent, ctx, err, c.cfg.HeaderTimeout)
	}
	if resp.StatusCode/100 != 2 {
		// the error body is small; it must not stall the caller forever
		t := time.AfterFunc(c.cfg.HeaderTimeout, cancel)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, c.lim.ErrorBytes))
		t.Stop()
		_ = resp.Body.Close()
		cancel()
		return nil, nil, statusError(resp.StatusCode, resp.Status, body)
	}
	return resp, cancel, nil
}
