package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/api/transport"
	"github.com/spk/spk-ocular/internal/streams"
)

// shutdownTimeout bounds graceful shutdown: an open SSE tab never closes its
// stream by itself, so without a bound Ctrl+C would hang forever.
const shutdownTimeout = 3 * time.Second

func runBrowser(ctx context.Context, o browserOpts) error {
	c, err := newCore(ctx, "browser")
	if err != nil {
		return err
	}
	defer c.Close()
	h, _ := newBrowserHandler(c, frontendFS(), o.TestAPI)

	// Request contexts derive from baseCtx so cancelBase ends long-lived
	// requests (SSE) directly — Shutdown alone only waits for them.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", o.Port),
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	slog.Info("spk-ocular browser mode", "url", "http://"+srv.Addr, "data", c.Paths.DataDir)
	return serveWithGracefulShutdown(ctx, srv, cancelBase)
}

// newBrowserHandler: the API under /api/, the SPA elsewhere, every request
// restricted to a loopback Host (DNS-rebinding defense: / hands out the token).
func newBrowserHandler(c *appCore, dist fs.FS, testAPI bool) (http.Handler, string) {
	apiH := transport.NewHTTP(c.Service, c.Emitter)
	mux := http.NewServeMux()
	mux.Handle("/api/", apiH)
	// Streams (logs) on the same origin; the token is in the path.
	sh := streams.NewHandler(c.Service.Streams(), streams.HandlerOptions{Classify: api.StreamErrorClass})
	mux.Handle("/streams/", http.StripPrefix("/streams", sh))
	c.Service.SetStreamBase(func() (string, error) { return "/streams/" + sh.Token(), nil })
	if testAPI {
		mux.Handle("/api/_test/", transport.AuthGuard(apiH.AuthToken(), testRoutes(c)))
	}
	mux.Handle("/", frontendHandler(apiH.AuthToken(), dist))
	return transport.LoopbackHostGuard(mux), apiH.AuthToken()
}

func serveWithGracefulShutdown(ctx context.Context, srv *http.Server, cancelBase context.CancelFunc) error {
	go func() {
		<-ctx.Done()
		cancelBase()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
