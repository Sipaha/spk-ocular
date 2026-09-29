package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/provider"
)

// Client-side bounds for the reflector's requests (package vars for tests).
// A server-side TimeoutSeconds is not a client deadline, and a Watch that
// never gets response headers would otherwise hang silently.
var (
	listTimeout = 90 * time.Second // one initial LIST, including big ones
	// watchEstablishTimeout bounds getting a watch stream's headers only;
	// a healthy stream then lives as long as the server keeps it.
	watchEstablishTimeout = 30 * time.Second
)

// transportReport receives the outcome of every list/watch request the
// reflector makes: nil = the request (or stream) works, else why not.
type transportReport func(err error)

// statusListWatch is the informer's ListerWatcher: plain dynamic-client
// list/watch that reports request outcomes (the reflector retries some
// failures internally, so its error handler alone would miss them) and
// wraps each watch stream to see watch.Error events. ListOptions pass
// through untouched (resourceVersion, timeouts, bookmarks, sendInitialEvents).
type statusListWatch struct {
	res       dynamic.ResourceInterface
	selector  string // field selector, "" = none
	report    transportReport
	ended     func() // a watch stream ended (renewal or a dropped connection)
	watchList bool   // the client supports WatchList semantics (real clients do)
}

var (
	_ cache.ListerWatcher            = (*statusListWatch)(nil)
	_ cache.ListerWatcherWithContext = (*statusListWatch)(nil)
)

func (lw *statusListWatch) opts(o metav1.ListOptions) metav1.ListOptions {
	if lw.selector != "" {
		o.FieldSelector = lw.selector
	}
	return o
}

func (lw *statusListWatch) ListWithContext(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	obj, err := lw.res.List(ctx, lw.opts(o))
	lw.report(err)
	return obj, err
}

func (lw *statusListWatch) WatchWithContext(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
	// Cancel only if the stream does not start in time; once it has, the
	// context lives until the stream ends or is stopped.
	ctx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(watchEstablishTimeout, cancel)
	w, err := lw.res.Watch(ctx, lw.opts(o))
	if !timer.Stop() && err == nil {
		// Established exactly as the timer fired: the stream is cancelled.
		w.Stop()
		err = context.DeadlineExceeded
	}
	if err != nil {
		cancel()
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			err = fmt.Errorf("watch did not start within %s: %w", watchEstablishTimeout, context.DeadlineExceeded)
		}
		lw.report(err)
		return nil, err
	}
	lw.report(nil)
	return newReportingWatch(w, lw.report, lw.ended, cancel), nil
}

func (lw *statusListWatch) List(o metav1.ListOptions) (runtime.Object, error) {
	return lw.ListWithContext(context.Background(), o)
}

func (lw *statusListWatch) Watch(o metav1.ListOptions) (watch.Interface, error) {
	return lw.WatchWithContext(context.Background(), o)
}

// IsWatchListSemanticsUnSupported tells the reflector whether it may use
// WatchList (the initial state as a watch stream); fake clients cannot.
func (lw *statusListWatch) IsWatchListSemanticsUnSupported() bool { return !lw.watchList }

// reportingWatch forwards a watch stream (being its only consumer) and
// reports watch.Error events. Stop is idempotent; forwarding never blocks
// after Stop.
type reportingWatch struct {
	inner  watch.Interface
	out    chan watch.Event
	stop   chan struct{}
	once   sync.Once
	report transportReport
	ended  func()
	cancel context.CancelFunc
}

func newReportingWatch(inner watch.Interface, report transportReport, ended func(), cancel context.CancelFunc) *reportingWatch {
	w := &reportingWatch{inner: inner, out: make(chan watch.Event), stop: make(chan struct{}), report: report, ended: ended, cancel: cancel}
	go w.run()
	return w
}

func (w *reportingWatch) run() {
	defer close(w.out)
	defer w.cancel()
	in := w.inner.ResultChan()
	for {
		select {
		case <-w.stop:
			return
		case ev, ok := <-in:
			if !ok {
				// Renewal by the server's timeout, or a dropped connection:
				// the reflector re-watches; if that does not succeed soon the
				// cache reports it (informerCache.streamEnded).
				if w.ended != nil {
					w.ended()
				}
				return
			}
			if ev.Type == watch.Error {
				w.report(apierrors.FromObject(ev.Object))
			}
			select {
			case w.out <- ev:
			case <-w.stop:
				return
			}
		}
	}
}

func (w *reportingWatch) ResultChan() <-chan watch.Event { return w.out }

func (w *reportingWatch) Stop() {
	w.once.Do(func() {
		close(w.stop)
		w.inner.Stop()
		w.cancel()
	})
}

// classify maps a client error to a UI error class and a short message.
func classify(err error) (provider.ErrorClass, string) {
	var se apierrors.APIStatus
	switch {
	case err == nil:
		return "", ""
	case apierrors.IsForbidden(err):
		return provider.ClassForbidden, statusMessage(err)
	case apierrors.IsUnauthorized(err):
		return provider.ClassUnauthorized, statusMessage(err)
	case strings.Contains(err.Error(), "getting credentials"):
		// exec credential plugin failed or timed out (internal/execshim);
		// its own stderr is in the app log.
		return provider.ClassUnauthorized, "the kubeconfig credential plugin failed or did not answer"
	case apierrors.IsNotFound(err):
		return provider.ClassNotFound, statusMessage(err)
	case apierrors.IsMethodNotSupported(err):
		return provider.ClassUnsupported, statusMessage(err)
	case apierrors.IsGone(err) || apierrors.IsResourceExpired(err):
		return provider.ClassGone, statusMessage(err) // relist follows; transient
	case errors.As(err, &se):
		return provider.ClassUnavailable, statusMessage(err)
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
		return provider.ClassUnavailable, err.Error()
	}
	return provider.ClassUnavailable, err.Error()
}

func statusMessage(err error) string {
	var se apierrors.APIStatus
	if errors.As(err, &se) && se.Status().Message != "" {
		return se.Status().Message
	}
	return err.Error()
}
