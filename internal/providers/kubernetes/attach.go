package kubernetes

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kfields "k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// A debugger's terminal attaches to its own process (its shell), like
// kubectl debug -it: the container may still be starting (its image
// pulled), so Run waits for it by watching the pod.
const (
	debugStartWait = 2 * time.Minute
	// debugExitWait bounds waiting for the exit code after the attach
	// ended (the kubelet reports it shortly after).
	debugExitWait = 10 * time.Second
	// rewatchPause: a watch the server ended is started again after it
	// (never a tight loop).
	rewatchPause = time.Second
)

// startFailures are waiting reasons after which a debugger does not start
// by itself soon (kubectl debug gives up on the pull failures too).
var startFailures = map[string]bool{"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true, "ErrImageNeverPull": true}

// prepareAttach pins the pod and its ephemeral container ch; the container
// need not run yet (Run waits for it).
func (s *session) prepareAttach(p *unstructured.Unstructured, ref core.Ref, ch string, req provider.ExecRequest) (provider.ExecHandle, error) {
	if len(req.Command) > 0 {
		return nil, invalid("an attach runs no command")
	}
	found := false
	for _, c := range slice(p.Object, "spec", "ephemeralContainers") {
		found = found || strOf(c, "name") == ch
	}
	if !found {
		return nil, invalid("%s has no debug container %q", p.GetName(), ch)
	}
	return &execHandle{conn: s.conn, ns: p.GetNamespace(), pod: p.GetName(), uid: p.GetUID(), container: ch, ref: ref, attach: true}, nil
}

// debuggerState is the ephemeral container's state (nil: no status yet).
func debuggerState(p *unstructured.Unstructured, name string) map[string]any {
	for _, cs := range slice(p.Object, "status", "ephemeralContainerStatuses") {
		if strOf(cs, "name") == name {
			st, _ := cs["state"].(map[string]any)
			return st
		}
	}
	return nil
}

func (h *execHandle) runAttach(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	if err := h.awaitStart(ctx, t.Notice); err != nil {
		return provider.ExitStatus{}, err
	}
	q := url.Values{"container": {h.container}, "stdin": {"true"}, "stdout": {"true"}, "tty": {"true"}}
	u, err := h.conn.podURL(h.ns, h.pod, "attach", q)
	if err != nil {
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	ex, err := h.conn.newExecutor(u)
	if err != nil {
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	// One newline first: the shell printed its prompt before the attach,
	// the tab would stay empty until the first key. Stdin is first read
	// once the streams are up.
	err = ex.StreamWithContext(closeOnCancel(ctx), remotecommand.StreamOptions{
		Stdin: io.MultiReader(strings.NewReader("\n"), t.Stdin), Stdout: t.Stdout, Tty: true, TerminalSizeQueue: sizeQueue{t.Sizes},
	})
	var ce exec.CodeExitError
	switch {
	case errors.As(err, &ce):
		return provider.ExitStatus{Code: ce.Code, Known: true}, nil
	case ctx.Err() != nil:
		return provider.ExitStatus{}, ctx.Err()
	case err != nil:
		return provider.ExitStatus{}, attachError(err)
	}
	return h.exitStatus(ctx)
}

// awaitStart returns once the debugger runs, telling the page why it
// waits meanwhile; an error when it will not run (or not in time).
func (h *execHandle) awaitStart(ctx context.Context, notice func(core.Message)) error {
	said, first := "", true
	check := func(p *unstructured.Unstructured) (bool, error) {
		st := debuggerState(p, h.container)
		switch {
		case st["running"] != nil:
			return true, nil
		case st["terminated"] != nil:
			return false, provider.Said(provider.ClassGone, msg("debug.ended", "container", h.container))
		case !execEligible(p):
			return false, provider.Said(provider.ClassGone, msg("debug.podEnded", "name", h.pod))
		}
		w, _ := st["waiting"].(map[string]any)
		reason := strOf(w, "reason")
		if startFailures[reason] {
			detail := reason
			if m := strOf(w, "message"); m != "" {
				detail += ": " + m
			}
			return false, provider.Said(provider.ClassUnavailable, msg("debug.cannotStart", "container", h.container, "detail", detail))
		}
		if notice != nil && (first || reason != said) {
			if reason == "" {
				notice(msg("debug.waitingStart"))
			} else {
				notice(msg("debug.starting", "reason", reason))
			}
		}
		said, first = reason, false
		return false, nil
	}
	wctx, cancel := context.WithTimeout(ctx, h.conn.startWait)
	defer cancel()
	_, err := h.watchPod(wctx, check)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil && wctx.Err() != nil:
		return provider.Said(provider.ClassUnavailable, msg("debug.slowStart", "container", h.container))
	}
	return err
}

// exitStatus is the debugger's exit code from its status (attach does not
// report it); unknown when it does not end soon (a lost connection) or the
// pod is gone.
func (h *execHandle) exitStatus(ctx context.Context) (provider.ExitStatus, error) {
	var st provider.ExitStatus
	wctx, cancel := context.WithTimeout(ctx, h.conn.exitWait)
	defer cancel()
	_, _ = h.watchPod(wctx, func(p *unstructured.Unstructured) (bool, error) {
		t, _ := debuggerState(p, h.container)["terminated"].(map[string]any)
		if t == nil {
			return false, nil
		}
		if code, ok := t["exitCode"].(int64); ok {
			st = provider.ExitStatus{Code: int(code), Known: true}
		}
		return true, nil
	})
	if ctx.Err() != nil {
		return provider.ExitStatus{}, ctx.Err()
	}
	return st, nil
}

// watchPod calls check with the pinned pod now and on each change until it
// is done or fails, the pod is gone, or ctx ends (then ctx's error).
func (h *execHandle) watchPod(ctx context.Context, check func(*unstructured.Unstructured) (bool, error)) (bool, error) {
	for {
		p, err := h.pinnedPod(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, err
		}
		if done, err := check(p); done || err != nil {
			return done, err
		}
		w, err := h.conn.dyn.Resource(podsKind.gvr).Namespace(h.ns).Watch(ctx, metav1.ListOptions{
			FieldSelector: kfields.OneTermEqualSelector("metadata.name", h.pod).String(), ResourceVersion: p.GetResourceVersion(),
		})
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			class, m := classify(err)
			return false, &provider.Error{Class: class, Message: m}
		}
		done, again, err := h.follow(ctx, w, check)
		w.Stop()
		if !again {
			return done, err
		}
		// The server ended the watch (a timeout, an expired version): read
		// the pod again.
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(rewatchPause):
		}
	}
}

// follow feeds w's changes of the pod to check; again: w ended first.
func (h *execHandle) follow(ctx context.Context, w watch.Interface, check func(*unstructured.Unstructured) (bool, error)) (done, again bool, err error) {
	for {
		select {
		case <-ctx.Done():
			return false, false, ctx.Err()
		case ev, ok := <-w.ResultChan():
			if !ok {
				return false, true, nil
			}
			u, _ := ev.Object.(*unstructured.Unstructured)
			switch ev.Type {
			case watch.Error:
				return false, true, nil
			case watch.Deleted:
				if u != nil && u.GetName() == h.pod {
					return false, false, h.podGone()
				}
			case watch.Added, watch.Modified:
				if u == nil || u.GetName() != h.pod {
					continue
				}
				if err := h.samePod(u); err != nil {
					return false, false, err
				}
				if done, err := check(u); done || err != nil {
					return done, false, err
				}
			}
		}
	}
}

func attachError(err error) error {
	if apierrors.IsForbidden(err) {
		return &provider.Error{Class: provider.ClassForbidden, Message: statusMessage(err) + " (the terminal needs the pods/attach permission)"}
	}
	class, m := classify(err)
	return &provider.Error{Class: class, Message: m}
}
