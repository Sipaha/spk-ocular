package engine

import (
	"context"
	"net"
	"net/url"
	"strings"

	"github.com/Microsoft/go-winio"
	"github.com/spk/spk-ocular/internal/provider"
)

func pipeDialer(u *url.URL) (func(context.Context, string, string) (net.Conn, error), error) {
	path := u.Path
	if u.Host != "" {
		path = "//" + u.Host + path
	}
	if !strings.HasPrefix(path, "//./pipe/") || strings.TrimPrefix(path, "//./pipe/") == "" ||
		strings.ContainsAny(strings.TrimPrefix(path, "//./pipe/"), `/\`) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, &Error{Class: provider.ClassInvalid, Message: "Docker named pipe must be a local npipe:////./pipe/NAME endpoint"}
	}
	path = strings.ReplaceAll(path, "/", `\`)
	return func(ctx context.Context, _, _ string) (net.Conn, error) { return winio.DialPipeContext(ctx, path) }, nil
}
