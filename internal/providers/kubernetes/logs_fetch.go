package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// errorBodyTimeout bounds reading an error response's body (a var for tests).
var errorBodyTimeout = 10 * time.Second

// logHeaderTimeout bounds getting a pods/log response's headers; the body
// then streams without a client deadline (a follow is long-lived).
const logHeaderTimeout = 30 * time.Second

// podLogRequest is PodLogOptions as we use them (timestamps always on).
type podLogRequest struct {
	Namespace, Pod, Container string
	Follow, Previous          bool
	TailLines                 int64     // 0: not set (all)
	SinceTime                 time.Time // zero: not set; sent with second precision
	LimitBytes                int64     // 0: not set
}

func (r podLogRequest) query() url.Values {
	v := url.Values{"timestamps": {"true"}}
	if r.Container != "" {
		v.Set("container", r.Container)
	}
	if r.Follow {
		v.Set("follow", "true")
	}
	if r.Previous {
		v.Set("previous", "true")
	}
	if r.TailLines > 0 {
		v.Set("tailLines", strconv.FormatInt(r.TailLines, 10))
	}
	if !r.SinceTime.IsZero() {
		// metav1.Time's wire form: RFC 3339 in seconds (the apiserver would
		// drop fractions anyway).
		v.Set("sinceTime", r.SinceTime.UTC().Truncate(time.Second).Format(time.RFC3339))
	}
	if r.LimitBytes > 0 {
		v.Set("limitBytes", strconv.FormatInt(r.LimitBytes, 10))
	}
	return v
}

// logFetcher opens a pods/log stream. Errors are apierrors (classify).
type logFetcher func(ctx context.Context, r podLogRequest) (io.ReadCloser, error)

// httpLogFetcher talks to the apiserver through client-go's transport
// (auth, exec plugins via the shim, proxies, TLS) without a typed clientset.
func httpLogFetcher(cfg *rest.Config) (logFetcher, error) {
	hc, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimRight(cfg.Host, "/"))
	if err != nil {
		return nil, err
	}
	if base.Scheme == "" { // "host:port" as kubeconfig allows
		if base, err = url.Parse("https://" + strings.TrimRight(cfg.Host, "/")); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context, r podLogRequest) (io.ReadCloser, error) {
		u := *base
		u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log", url.PathEscape(r.Namespace), url.PathEscape(r.Pod))
		u.RawQuery = r.query().Encode()
		return fetchLog(ctx, hc, u.String(), cfg.UserAgent)
	}, nil
}

func fetchLog(ctx context.Context, hc *http.Client, u, ua string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	hdr := time.AfterFunc(logHeaderTimeout, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/plain, */*")
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := hc.Do(req)
	if !hdr.Stop() {
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		return nil, fmt.Errorf("no response from the cluster within %s: %w", logHeaderTimeout, context.DeadlineExceeded)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		// the error body is small; it must not stall the caller forever
		body := time.AfterFunc(errorBodyTimeout, cancel)
		defer body.Stop()
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		return nil, statusError(resp)
	}
	return &cancelBody{ReadCloser: resp.Body, cancel: cancel}, nil
}

// statusError turns an error response into an apierrors.StatusError (the
// body is a metav1.Status, or plain text from a proxy).
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var st metav1.Status
	if json.Unmarshal(body, &st) == nil && st.Kind == "Status" {
		if st.Code == 0 {
			st.Code = int32(resp.StatusCode) //nolint:gosec // an HTTP status fits
		}
		return &apierrors.StatusError{ErrStatus: st}
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return apierrors.NewGenericServerResponse(resp.StatusCode, http.MethodGet, podsResource, "", msg, 0, false)
}

var podsResource = podsKind.gvr.GroupResource()

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

// logErrorKind sorts a failed pods/log request.
type logErrorKind int

const (
	logErrOther     logErrorKind = iota
	logErrWaiting                // the container has not started (yet)
	logErrNoPrev                 // previous requested, there is none
	logErrBadCtr                 // no such container in the pod
	logErrForbidden              // no pods/log permission
	logErrNotFound               // the pod is gone
)

func kindOfLogError(err error) logErrorKind {
	switch {
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
		return logErrForbidden
	case apierrors.IsNotFound(err):
		return logErrNotFound
	}
	var se apierrors.APIStatus
	if !errors.As(err, &se) || se.Status().Code != http.StatusBadRequest {
		return logErrOther
	}
	msg := se.Status().Message
	switch {
	case strings.Contains(msg, "previous terminated container"):
		return logErrNoPrev
	case strings.Contains(msg, "is not valid for pod"):
		return logErrBadCtr
	case strings.Contains(msg, "is waiting to start"), strings.Contains(msg, "ContainerCreating"),
		strings.Contains(msg, "PodInitializing"), strings.Contains(msg, "is not available"):
		return logErrWaiting
	}
	return logErrOther
}
