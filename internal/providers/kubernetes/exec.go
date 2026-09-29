package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"
	streamhttp "k8s.io/streaming/pkg/httpstream"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var _ provider.Execer = (*session)(nil)

// Commands run in pods and, through one of their pods, in workloads.
func init() {
	podsKind.desc.Exec = true
	for def := range logWorkloads {
		def.desc.Exec = true
	}
}

// defaultShell finds a shell without relying on /bin/sh's path: bash, then
// ash, then sh (exec replaces the probing sh, so nothing extra stays).
var defaultShell = []string{"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; elif command -v ash >/dev/null 2>&1; then exec ash; else exec sh; fi"}

// maxInstances bounds the pods offered for a workload.
const maxInstances = 50

// conn is a session's connection snapshot: what live resources (terminals,
// tunnels) keep after the session itself is closed. It holds no session
// context or cache.
type conn struct {
	cfg         *rest.Config // a private copy
	dyn         dynamic.Interface
	target      string
	targetTitle string
	hash        string
	endpoint    string // the API server's host (never credentials)
	// newExecutor builds the exec transport (tests replace it).
	newExecutor func(u *url.URL) (remotecommand.Executor, error)
}

func newConn(cfg *rest.Config, dyn dynamic.Interface, target, title, hash string) *conn {
	c := &conn{cfg: rest.CopyConfig(cfg), dyn: dyn, target: target, targetTitle: title, hash: hash}
	if u, err := serverURL(cfg.Host); err == nil {
		c.endpoint = u.Host
	}
	c.newExecutor = c.fallbackExecutor
	return c
}

// serverURL parses a kubeconfig server ("host:port" without a scheme is
// https, as client-go reads it).
func serverURL(host string) (*url.URL, error) {
	host = strings.TrimRight(host, "/")
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err == nil && u.Path == "" {
		u.Path = "/"
	}
	return u, err
}

// fallbackExecutor: WebSocket (GET) first, SPDY (POST) when the upgrade
// fails before anything ran — kubectl's predicate, so a command never runs
// twice.
func (c *conn) fallbackExecutor(u *url.URL) (remotecommand.Executor, error) {
	ws, err := c.wsExecutor(u)
	if err != nil {
		return nil, err
	}
	spdy, err := c.spdyExecutor(u)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(ws, spdy, shouldFallback)
}

func (c *conn) wsExecutor(u *url.URL) (remotecommand.Executor, error) {
	return remotecommand.NewWebSocketExecutor(c.cfg, "GET", u.String())
}

func (c *conn) spdyExecutor(u *url.URL) (remotecommand.Executor, error) {
	rt, up, err := spdyTransports(c.cfg)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewSPDYExecutorForTransports(rt, up, "POST", u)
}

// shouldFallback: the WebSocket upgrade was refused (or an HTTPS proxy
// cannot carry it) before anything ran. client-go v0.37 reports it with
// k8s.io/streaming's error type — apimachinery's deprecated same-named
// predicates do not recognise it (the fallback would never happen).
func shouldFallback(err error) bool {
	return streamhttp.IsUpgradeFailure(err) || streamhttp.IsHTTPSProxyError(err)
}

// podURL is <server>/api/v1/namespaces/<ns>/pods/<name>/<sub>.
func (c *conn) podURL(ns, name, sub string, q url.Values) (*url.URL, error) {
	base, err := serverURL(c.cfg.Host)
	if err != nil {
		return nil, err
	}
	u := base.JoinPath("api", "v1", "namespaces", ns, "pods", name, sub)
	u.RawQuery = q.Encode()
	return u, nil
}

func (c *conn) getPod(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
	return c.get(ctx, podsKind, ns, name)
}

// get GETs an object with the snapshot's client.
func (c *conn) get(ctx context.Context, def *kindDef, ns, name string) (*unstructured.Unstructured, error) {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	u, err := c.dyn.Resource(def.gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		class, msg := classify(err)
		return nil, &provider.Error{Class: class, Message: msg}
	}
	return u, nil
}

// ExecInfo: a pod is its own single instance; a workload offers its
// running pods (by controller UID), ready and newest first.
func (s *session) ExecInfo(ctx context.Context, ref core.Ref) (core.ExecInfo, error) {
	pods, err := s.execPods(ctx, ref)
	if err != nil {
		return core.ExecInfo{}, err
	}
	info := core.ExecInfo{}
	for _, p := range pods {
		info.Instances = append(info.Instances, execInstance(p))
	}
	if len(info.Instances) > 0 {
		info.DefaultInstance = info.Instances[0].ID
	}
	return info, nil
}

// execPods: ref itself for a pod (UID-checked); a workload's running,
// not deleting pods in preference order.
func (s *session) execPods(ctx context.Context, ref core.Ref) ([]*unstructured.Unstructured, error) {
	def := s.kinds.byID[ref.Kind]
	if def == podsKind {
		u, err := s.getObject(ctx, ref)
		if err != nil {
			return nil, err
		}
		return []*unstructured.Unstructured{u}, nil
	}
	if !logWorkloads[def] {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("commands cannot run in %s", ref.Kind)}
	}
	owner, err := s.getObject(ctx, ref)
	if err != nil {
		return nil, err
	}
	pods, err := workloadPods(ctx, s.conn.dyn, def, owner)
	if err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	for _, p := range pods {
		if runnable(p) {
			out = append(out, p)
		}
	}
	rankPods(out)
	if len(out) == 0 {
		return nil, &provider.Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("%s has no running pods", ref.Name)}
	}
	return out[:min(len(out), maxInstances)], nil
}

// workloadPods lists every pod controlled by owner (a Deployment through
// its ReplicaSets), following all pages: the choice must not depend on a
// capped relation list.
func workloadPods(ctx context.Context, dyn dynamic.Interface, def *kindDef, owner *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	ns := owner.GetNamespace()
	controllers := map[types.UID]bool{owner.GetUID(): true}
	if def == deploymentsKind {
		controllers = map[types.UID]bool{}
		rss, err := listAll(ctx, dyn, replicaSetsKind, ns, selectorOf(owner))
		if err != nil {
			return nil, err
		}
		for _, rs := range rss {
			if c := metav1.GetControllerOfNoCopy(rs); c != nil && c.UID == owner.GetUID() {
				controllers[rs.GetUID()] = true
			}
		}
	}
	pods, err := listAll(ctx, dyn, podsKind, ns, selectorOf(owner))
	if err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	for _, p := range pods {
		if c := metav1.GetControllerOfNoCopy(p); c != nil && controllers[c.UID] {
			out = append(out, p)
		}
	}
	return out, nil
}

// selectorOf is a workload's spec.selector as a label selector ("" = none).
func selectorOf(owner *unstructured.Unstructured) string {
	m, ok, _ := unstructured.NestedMap(owner.Object, "spec", "selector")
	if !ok {
		return ""
	}
	var ls metav1.LabelSelector
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &ls); err != nil {
		return ""
	}
	sel, err := metav1.LabelSelectorAsSelector(&ls)
	if err != nil || sel.Empty() {
		return ""
	}
	return sel.String()
}

// maxListed bounds a full listing (pages of listPage).
const maxListed = 20000

func listAll(ctx context.Context, dyn dynamic.Interface, def *kindDef, ns, selector string) ([]*unstructured.Unstructured, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out []*unstructured.Unstructured
	opts := metav1.ListOptions{LabelSelector: selector, Limit: listPage}
	for {
		l, err := dyn.Resource(def.gvr).Namespace(ns).List(ctx, opts)
		if err != nil {
			class, msg := classify(err)
			return nil, &provider.Error{Class: class, Message: msg}
		}
		for i := range l.Items {
			out = append(out, &l.Items[i])
		}
		if l.GetContinue() == "" || len(out) >= maxListed {
			return out, nil
		}
		opts.Continue = l.GetContinue()
	}
}

// runnable: a pod a command can run in — running and not being deleted.
func runnable(p *unstructured.Unstructured) bool {
	return p.GetDeletionTimestamp() == nil && str(p.Object, "status", "phase") == "Running"
}

func podReady(p *unstructured.Unstructured) bool {
	return podConditions(p.Object)["Ready"].status == "True"
}

// rankPods: ready first, then newest.
func rankPods(pods []*unstructured.Unstructured) {
	sort.SliceStable(pods, func(i, j int) bool {
		ri, rj := podReady(pods[i]), podReady(pods[j])
		if ri != rj {
			return ri
		}
		return pods[i].GetCreationTimestamp().After(pods[j].GetCreationTimestamp().Time)
	})
}

// instanceID encodes the pod's name and UID (opaque to the UI).
func instanceID(p *unstructured.Unstructured) string { return p.GetName() + "/" + string(p.GetUID()) }

func execInstance(p *unstructured.Unstructured) core.ExecInstance {
	chs := execChannels(p)
	logChs := make([]core.LogChannel, len(chs))
	for i, c := range chs {
		logChs[i] = core.LogChannel{ID: c.ID, Note: c.Note}
	}
	return core.ExecInstance{
		ID: instanceID(p), Title: p.GetName(), Ready: podReady(p),
		Channels: chs, DefaultChannel: defaultChannel(p.GetAnnotations(), logChs),
	}
}

// execChannels: regular containers (with their state), then init and
// ephemeral containers that are running now.
func execChannels(p *unstructured.Unstructured) []core.ExecChannel {
	states := map[string]map[string]any{}
	for _, key := range []string{"containerStatuses", "initContainerStatuses", "ephemeralContainerStatuses"} {
		for _, cs := range slice(p.Object, "status", key) {
			st, _ := cs["state"].(map[string]any)
			states[key+"/"+strOf(cs, "name")] = st
		}
	}
	var out []core.ExecChannel
	for _, c := range podChannels(p.Object, "spec") {
		key := "containerStatuses/"
		switch c.Note {
		case ctrInit, ctrSidecar:
			key = "initContainerStatuses/"
		case ctrEphemeral:
			key = "ephemeralContainerStatuses/"
		}
		st := states[key+c.ID]
		running := st != nil && st["running"] != nil && runnable(p)
		if c.Note != "" && !running {
			continue
		}
		out = append(out, core.ExecChannel{ID: c.ID, Title: c.ID, Note: c.Note, Running: running, State: stateText(st)})
	}
	return out
}

func stateText(st map[string]any) string {
	switch {
	case st == nil:
		return "not started"
	case st["running"] != nil:
		return "running"
	case st["waiting"] != nil:
		w, _ := st["waiting"].(map[string]any)
		return nonEmpty("waiting: "+strOf(w, "reason"), "waiting")
	case st["terminated"] != nil:
		t, _ := st["terminated"].(map[string]any)
		return nonEmpty("terminated: "+strOf(t, "reason"), "terminated")
	}
	return ""
}

func invalid(format string, a ...any) error {
	return &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf(format, a...)}
}

// PrepareExec pins the pod (name + UID), the container and argv.
func (s *session) PrepareExec(ctx context.Context, ref core.Ref, req provider.ExecRequest) (provider.ExecHandle, error) {
	var p *unstructured.Unstructured
	if req.Instance == "" {
		pods, err := s.execPods(ctx, ref)
		if err != nil {
			return nil, err
		}
		p = pods[0]
	} else {
		name, uid, ok := strings.Cut(req.Instance, "/")
		if !ok {
			return nil, invalid("bad instance %q", req.Instance)
		}
		if s.kinds.byID[ref.Kind] == podsKind && (name != ref.Name || (ref.UID != "" && uid != ref.UID)) {
			return nil, invalid("instance %q is not %s", req.Instance, ref.Name)
		}
		var err error
		if p, err = s.getObject(ctx, core.Ref{Kind: podsKind.desc.ID, Scope: ref.Scope, Name: name, UID: uid}); err != nil {
			return nil, err
		}
	}
	inst := execInstance(p)
	ch := req.Channel
	if ch == "" {
		ch = inst.DefaultChannel
	}
	if err := channelRunnable(inst, ch); err != nil {
		return nil, err
	}
	argv := req.Command
	if len(argv) == 0 {
		argv = defaultShell
	}
	return &execHandle{
		conn: s.conn, ns: p.GetNamespace(), pod: p.GetName(), uid: p.GetUID(), container: ch, argv: argv,
		shell: len(req.Command) == 0, ref: ref,
	}, nil
}

func channelRunnable(inst core.ExecInstance, ch string) error {
	for _, c := range inst.Channels {
		if c.ID != ch {
			continue
		}
		if !c.Running {
			return invalid("container %s of %s is not running (%s)", ch, inst.Title, c.State)
		}
		return nil
	}
	return invalid("%s has no running container %q", inst.Title, ch)
}

// execHandle runs one command in a pinned pod and container. It needs only
// the connection snapshot: the session may be long gone.
type execHandle struct {
	conn      *conn
	ref       core.Ref
	ns, pod   string
	uid       types.UID
	container string
	argv      []string
	shell     bool // argv is defaultShell
}

func (h *execHandle) Describe() core.LiveTarget {
	t := core.LiveTarget{
		Provider: ProviderID, Target: h.conn.target, TargetTitle: h.conn.targetTitle, Endpoint: h.conn.endpoint,
		ConfigHash: h.conn.hash, Ref: h.ref, Instance: h.pod, Channel: h.container,
	}
	if !h.shell {
		t.Command = h.argv
	}
	return t
}

func (h *execHandle) Again() (provider.ExecHandle, error) {
	c := *h
	return &c, nil
}

// Close: the handle holds no connection between runs.
func (h *execHandle) Close() {}

func (h *execHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	// The pod is addressed by name: check it is still the pinned one (a
	// same-named replacement between this check and the exec request is
	// a race the API cannot close).
	p, err := h.conn.getPod(ctx, h.ns, h.pod)
	var pe *provider.Error
	switch {
	case errors.As(err, &pe) && pe.Class == provider.ClassNotFound:
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("pod %s no longer exists", h.pod)}
	case err != nil:
		return provider.ExitStatus{}, err
	case p.GetUID() != h.uid:
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("pod %s was replaced by a new one with the same name", h.pod)}
	}
	if err := channelRunnable(execInstance(p), h.container); err != nil {
		return provider.ExitStatus{}, err
	}
	q := url.Values{"container": {h.container}, "command": h.argv, "stdin": {"true"}, "stdout": {"true"}, "tty": {"true"}}
	u, err := h.conn.podURL(h.ns, h.pod, "exec", q)
	if err != nil {
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	ex, err := h.conn.newExecutor(u)
	if err != nil {
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	// closeOnCancel: the WebSocket handshake would not notice ctx ending
	// (upgrade.go); the SPDY path's upgrader does by itself.
	err = ex.StreamWithContext(closeOnCancel(ctx), remotecommand.StreamOptions{
		Stdin: t.Stdin, Stdout: t.Stdout, Tty: true, TerminalSizeQueue: sizeQueue{t.Sizes},
	})
	var ce exec.CodeExitError
	switch {
	case err == nil:
		return provider.ExitStatus{Code: 0, Known: true}, nil
	case errors.As(err, &ce):
		return provider.ExitStatus{Code: ce.Code, Known: true}, nil
	case ctx.Err() != nil:
		return provider.ExitStatus{}, ctx.Err()
	}
	return provider.ExitStatus{}, h.execError(err)
}

// execError explains the usual failures.
func (h *execHandle) execError(err error) error {
	msg := err.Error()
	switch {
	case apierrors.IsForbidden(err):
		return &provider.Error{Class: provider.ClassForbidden, Message: statusMessage(err) + " (commands need the pods/exec permission)"}
	case strings.Contains(msg, "executable file not found") || strings.Contains(msg, "no such file or directory"):
		if h.shell {
			return invalid("%q was not found in the container's PATH: the image has no shell; run a command instead", h.argv[0])
		}
		return invalid("%q was not found in the container: %s", h.argv[0], msg)
	}
	class, m := classify(err)
	return &provider.Error{Class: class, Message: m}
}

// sizeQueue adapts provider sizes to remotecommand's.
type sizeQueue struct{ s provider.TermSizes }

func (q sizeQueue) Next() *remotecommand.TerminalSize {
	if q.s == nil {
		return nil
	}
	v := q.s.Next()
	if v == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: v.Cols, Height: v.Rows}
}
