package kubernetes

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport/spdy"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"
)

// Streaming upgrades (exec, port-forward) whose handshake a context can
// end. client-go's own ones read the upgrade answer without looking at the
// context: gorilla/websocket (the WebSocket path, built without a handshake
// timeout) and the SPDY round tripper both block in http.ReadResponse, so a
// server that accepts and never answers would hold a cancelled terminal or
// tunnel forever.

// spdyPingPeriod keeps an idle SPDY connection observably alive (as
// client-go's own SPDY round tripper does).
const spdyPingPeriod = 5 * time.Second

// closeOnCancel returns ctx with an HTTP trace hook: the connection a
// WebSocket handshake (gorilla) dials is closed once ctx ends, which
// unblocks a handshake waiting for an answer. Only for requests whose
// connection is theirs alone — never a pooled one.
func closeOnCancel(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			c := info.Conn
			context.AfterFunc(ctx, func() { _ = c.Close() })
		},
	})
}

// ctxSpdyUpgrader is a SPDY upgrade round tripper for one request: it
// dials like client-go's (TLS, HTTP/SOCKS proxies), but while it waits for
// the upgrade answer the request's context can close the connection.
type ctxSpdyUpgrader struct {
	rt   *streamspdy.SpdyRoundTripper
	conn net.Conn
}

// spdyTransports returns the round tripper for a SPDY upgrade request
// (auth wrappers included) and its upgrader. One pair per request.
func spdyTransports(cfg *rest.Config) (http.RoundTripper, spdy.Upgrader, error) {
	rt, up, err := spdyUpgrade(cfg)
	if err != nil {
		return nil, nil, err
	}
	return rt, spdy.NewUpgraderForStreaming(up), nil
}

func spdyUpgrade(cfg *rest.Config) (http.RoundTripper, *ctxSpdyUpgrader, error) {
	tlsConfig, err := rest.TLSConfigFor(cfg)
	if err != nil {
		return nil, nil, err
	}
	proxy := http.ProxyFromEnvironment
	if cfg.Proxy != nil {
		proxy = cfg.Proxy
	}
	rt, err := streamspdy.NewRoundTripperWithConfig(streamspdy.RoundTripperConfig{TLS: tlsConfig, Proxier: proxy, PingPeriod: spdyPingPeriod})
	if err != nil {
		return nil, nil, err
	}
	up := &ctxSpdyUpgrader{rt: rt}
	wrapped, err := rest.HTTPWrappersForConfig(cfg, up)
	if err != nil {
		return nil, nil, err
	}
	return wrapped, up, nil
}

func (u *ctxSpdyUpgrader) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header = req.Header.Clone()
	req.Header.Add(streamhttp.HeaderConnection, streamhttp.HeaderUpgrade)
	req.Header.Add(streamhttp.HeaderUpgrade, streamspdy.HeaderSpdy31)
	conn, err := u.rt.Dial(req) // the dial itself honours the context
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(req.Context(), func() { _ = conn.Close() })
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if !stop() { // the context ended during the handshake: the conn is closed
		if err == nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("upgrade cancelled: %w", context.Cause(req.Context()))
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	u.conn = conn
	return resp, nil
}

// NewConnection validates the upgrade answer (as client-go's does) and
// starts SPDY on the connection.
func (u *ctxSpdyUpgrader) NewConnection(resp *http.Response) (streamhttp.Connection, error) {
	connection := strings.ToLower(resp.Header.Get(streamhttp.HeaderConnection))
	upgrade := strings.ToLower(resp.Header.Get(streamhttp.HeaderUpgrade))
	if resp.StatusCode != http.StatusSwitchingProtocols || !strings.Contains(connection, strings.ToLower(streamhttp.HeaderUpgrade)) ||
		!strings.Contains(upgrade, strings.ToLower(streamspdy.HeaderSpdy31)) {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if u.conn != nil {
			_ = u.conn.Close()
		}
		return nil, fmt.Errorf("unable to upgrade connection: %w", upgradeError(resp.StatusCode, body))
	}
	return streamspdy.NewClientConnectionWithPings(u.conn, spdyPingPeriod)
}

// upgradeError is a refused upgrade as an API status error when the body is
// a metav1.Status (so 403 is "forbidden"), else plain text.
func upgradeError(code int, body []byte) error {
	var st metav1.Status
	if json.Unmarshal(body, &st) == nil && st.Kind == "Status" {
		return apierrors.FromObject(&st)
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		s = http.StatusText(code)
	}
	return apierrors.NewGenericServerResponse(code, "", schema.GroupResource{}, "", s, 0, false)
}
