package kubernetes

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/spk/spk-ocular/internal/provider"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// One budget belongs to one admitted session and all its HTTP clients, including
// raw Table/discovery clients. Retries pay again. Streams pay for establishment
// but do not occupy a finite-response slot for their entire lifetime.
const (
	sessionReadQPS         = 10
	sessionReadBurst       = 20
	sessionReadConcurrency = 4
)

type readBudget struct {
	next  http.RoundTripper
	rate  flowcontrol.RateLimiter
	slots chan struct{}
}

// An opt-in observation budget charges actual HTTP attempts, including client-go
// retries. Logical Get calls alone do not bound retry traffic.
type finiteRequestLimitKey struct{}

// budgetConfig copies, rather than mutates, the admitted caller configuration.
// The shared transport budget also covers raw HTTP clients. Existing stricter
// client-go throttles remain in place, including those for mutation requests.
func budgetConfig(original *rest.Config) *rest.Config {
	cfg := rest.CopyConfig(original)
	rate := flowcontrol.NewTokenBucketRateLimiter(sessionReadQPS, sessionReadBurst)
	slots := make(chan struct{}, sessionReadConcurrency)
	previous := cfg.WrapTransport
	cfg.WrapTransport = func(next http.RoundTripper) http.RoundTripper {
		if previous != nil {
			next = previous(next)
		}
		return &readBudget{next: next, rate: rate, slots: slots}
	}
	return cfg
}

func (b *readBudget) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return b.next.RoundTrip(req)
	}
	if remaining, ok := req.Context().Value(finiteRequestLimitKey{}).(*atomic.Int32); ok && remaining.Add(-1) < 0 {
		return nil, &provider.Error{Class: provider.ClassScopeLimit, Message: "finite observation request budget exhausted"}
	}
	if p, ok := req.Context().Value(loadTransferKey{}).(*loadTransfer); ok {
		p.known.Store(true)
	}
	streaming := req.URL.Query().Get("watch") == "true" || req.URL.Query().Get("follow") == "true" || req.Header.Get("Upgrade") != ""
	if streaming {
		if err := b.rate.Wait(req.Context()); err != nil {
			return nil, err
		}
		resp, err := b.next.RoundTrip(req)
		if resp != nil {
			resp.Body = countTransfer(req.Context(), resp.Body)
		}
		return resp, err
	}
	select {
	case b.slots <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	var once sync.Once
	release := func() { once.Do(func() { <-b.slots }) }
	if err := b.rate.Wait(req.Context()); err != nil {
		release()
		return nil, err
	}
	resp, err := b.next.RoundTrip(req)
	if err != nil {
		release()
		return nil, err
	}
	if resp.Body == nil {
		release()
		return resp, nil
	}
	resp.Body = countTransfer(req.Context(), resp.Body)
	if guard, ok := req.Context().Value(scopeBudgetKey{}).(*scopeBudget); ok {
		resp.Body = &scopeBody{ReadCloser: resp.Body, guard: guard}
	}
	resp.Body = &budgetBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}

type budgetBody struct {
	io.ReadCloser
	release func()
}

func (b *budgetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.release()
	}
	return n, err
}
func (b *budgetBody) Close() error { defer b.release(); return b.ReadCloser.Close() }
