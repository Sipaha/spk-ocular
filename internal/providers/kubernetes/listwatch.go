package kubernetes

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/provider"
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
	watchList bool // the client supports WatchList semantics (real clients do)
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
	obj, err := lw.res.List(ctx, lw.opts(o))
	lw.report(err)
	return obj, err
}

func (lw *statusListWatch) WatchWithContext(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
	w, err := lw.res.Watch(ctx, lw.opts(o))
	if err != nil {
		lw.report(err)
		return nil, err
	}
	lw.report(nil)
	return newReportingWatch(w, lw.report), nil
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
}

func newReportingWatch(inner watch.Interface, report transportReport) *reportingWatch {
	w := &reportingWatch{inner: inner, out: make(chan watch.Event), stop: make(chan struct{}), report: report}
	go w.run()
	return w
}

func (w *reportingWatch) run() {
	defer close(w.out)
	in := w.inner.ResultChan()
	for {
		select {
		case <-w.stop:
			return
		case ev, ok := <-in:
			if !ok {
				return // normal end (timeout renewal) or server close: the reflector re-watches
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
