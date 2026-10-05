//go:build !windows

package engine

import (
	"context"
	"net"
	"net/url"
)

func pipeDialer(*url.URL) (func(context.Context, string, string) (net.Conn, error), error) {
	return nil, unsupported("npipe:// Docker endpoints exist only on Windows")
}
