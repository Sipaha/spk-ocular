package provider

import (
	"context"
	"io"

	"github.com/spk/spk-ocular/internal/core"
)

// PortForwarder is implemented by sessions that can forward a local port to
// a port of an object (k8s: a pod, the pods of a Service or a workload).
type PortForwarder interface {
	// ForwardInfo lists the ports of ref that can be forwarded.
	ForwardInfo(ctx context.Context, ref core.Ref) (core.ForwardInfo, error)
	// PrepareForward pins the logical target (its identity, not one
	// instance) and captures the connection. Like an ExecHandle, the handle
	// does not depend on the session.
	PrepareForward(ctx context.Context, ref core.Ref, req ForwardRequest) (ForwardHandle, error)
}

type ForwardRequest struct {
	// Port is ForwardInfo's port number (k8s: the Service port, or a
	// container port; any port of a Pod when ForwardInfo.AnyPort).
	Port int `json:"port"`
}

// ForwardHandle is a prepared tunnel target, owned by the app.
type ForwardHandle interface {
	Describe() core.LiveTarget
	// Connect chooses an instance of the pinned target, resolves the port
	// and establishes a multiplexed connection to it. It is cancelled by
	// ctx (including the network handshake); a replaced pinned target is an
	// error, never silently another object.
	Connect(ctx context.Context) (Upstream, error)
	Close()
}

// Upstream is one established connection to an instance; it carries many
// local connections as independent streams.
type Upstream interface {
	// Label says where it goes (the chosen pod and resolved port).
	Label() string
	// Open starts one forwarded connection. A failure is this connection's
	// error: the upstream and its other streams stay usable.
	Open(ctx context.Context) (Stream, error)
	// Done is closed when the upstream is dead (its streams fail); Err is
	// why (nil after Close).
	Done() <-chan struct{}
	Err() error
	// Close is idempotent.
	Close()
}

// Stream is one forwarded connection.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite half-closes: the request is sent, the response is still
	// read.
	CloseWrite() error
	// Close resets the stream and forgets it; idempotent.
	Close() error
	// Result is this connection's error reported by the remote side (k8s:
	// the error stream, "connection refused" for a port nobody listens on),
	// nil if none. Call it after reading ended: it waits a bounded time for
	// the report and returns promptly once the stream or the upstream is
	// closed.
	Result() error
}
