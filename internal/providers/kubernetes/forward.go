package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var _ provider.PortForwarder = (*session)(nil)

// Ports of pods, Services and workloads can be forwarded.
func init() {
	podsKind.desc.Forward = true
	servicesKind.desc.Forward = true
	for def := range logWorkloads {
		def.desc.Forward = true
	}
}

// ForwardInfo: a pod's container ports (and any other port); a Service's
// ports; a workload's ports from its pod template.
func (s *session) ForwardInfo(ctx context.Context, ref core.Ref) (core.ForwardInfo, error) {
	info, _, err := s.forwardInfo(ctx, ref)
	return info, err
}

// forwardInfo reads the object once: its ports and the object itself.
func (s *session) forwardInfo(ctx context.Context, ref core.Ref) (core.ForwardInfo, *unstructured.Unstructured, error) {
	def := s.kinds.byID[ref.Kind]
	if def != podsKind && def != servicesKind && !logWorkloads[def] {
		return core.ForwardInfo{}, nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("ports of %s cannot be forwarded", ref.Kind)}
	}
	u, err := s.getObject(ctx, ref)
	if err != nil {
		return core.ForwardInfo{}, nil, err
	}
	return forwardInfoOf(def, u), u, nil
}

func forwardInfoOf(def *kindDef, u *unstructured.Unstructured) core.ForwardInfo {
	switch def {
	case podsKind:
		return core.ForwardInfo{Ports: containerPorts(u.Object, "spec"), AnyPort: true}
	case servicesKind:
		return core.ForwardInfo{Ports: serviceForwardPorts(u.Object), Unsupported: serviceUnsupported(u.Object)}
	}
	return core.ForwardInfo{Ports: containerPorts(u.Object, "spec", "template", "spec")}
}

// containerPorts lists the declared ports of a pod spec (at path).
func containerPorts(o map[string]any, path ...string) []core.ForwardPort {
	out := []core.ForwardPort{}
	seen := map[string]bool{}
	for _, key := range []string{"containers", "initContainers"} {
		for _, c := range slice(o, append(path, key)...) {
			for _, p := range slice(c, "ports") {
				n := int(i64(p, "containerPort"))
				proto := nonEmpty(strOf(p, "protocol"), "TCP")
				k := proto + "/" + strconv.Itoa(n)
				if n <= 0 || seen[k] {
					continue
				}
				seen[k] = true
				fp := core.ForwardPort{Port: n, Name: strOf(p, "name"), Protocol: proto, Note: "container " + strOf(c, "name"),
					Scheme: schemeOf(strOf(p, "name"), "")}
				supported(&fp)
				out = append(out, fp)
			}
		}
	}
	return out
}

func serviceForwardPorts(o map[string]any) []core.ForwardPort {
	out := []core.ForwardPort{}
	for _, p := range slice(o, "spec", "ports") {
		fp := core.ForwardPort{Port: int(i64(p, "port")), Name: strOf(p, "name"), Protocol: nonEmpty(strOf(p, "protocol"), "TCP"),
			Scheme: schemeOf(strOf(p, "name"), strOf(p, "appProtocol"))}
		if tp := targetPortText(p); tp != "" && tp != strconv.Itoa(fp.Port) {
			fp.Note = "→ " + tp
		}
		supported(&fp)
		out = append(out, fp)
	}
	return out
}

func supported(fp *core.ForwardPort) {
	fp.Supported = fp.Protocol == "TCP"
	if !fp.Supported {
		fp.Reason = fp.Protocol + " cannot be forwarded (Kubernetes forwards TCP only)"
	}
}

// schemeOf: a port named or declared as HTTP(S) may be opened in a browser.
func schemeOf(name, appProtocol string) string {
	for _, v := range []string{strings.ToLower(appProtocol), strings.ToLower(name)} {
		switch {
		case v == "https" || strings.HasPrefix(v, "https-") || strings.HasSuffix(v, "-https"):
			return "https"
		case v == "http" || strings.HasPrefix(v, "http-") || strings.HasSuffix(v, "-http") || v == "kubernetes.io/http" || v == "web":
			return "http"
		}
	}
	return ""
}

// serviceUnsupported says why a Service has no pods to forward to.
func serviceUnsupported(o map[string]any) string {
	if str(o, "spec", "type") == "ExternalName" {
		return "an ExternalName Service points outside the cluster: it has no pods to forward to"
	}
	if len(stringMap(o, "spec", "selector")) == 0 {
		return "a Service without a selector has no pods to forward to (its endpoints are managed by hand)"
	}
	return ""
}

func stringMap(o map[string]any, path ...string) map[string]string {
	m, _, _ := unstructured.NestedStringMap(o, path...)
	return m
}

// targetPort of a Service port: "" when empty/0, the number, or the name.
func targetPortText(p map[string]any) string {
	switch v := p["targetPort"].(type) {
	case string:
		return v
	case int64:
		if v > 0 {
			return strconv.FormatInt(v, 10)
		}
	case float64:
		if v > 0 {
			return strconv.FormatInt(int64(v), 10)
		}
	case int:
		if v > 0 {
			return strconv.Itoa(v)
		}
	}
	return ""
}

// PrepareForward pins the logical target (its UID, from the snapshot its
// ports were checked on) and the port.
func (s *session) PrepareForward(ctx context.Context, ref core.Ref, req provider.ForwardRequest) (provider.ForwardHandle, error) {
	info, u, err := s.forwardInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	if info.Unsupported != "" {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: info.Unsupported}
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, invalid("the port must be 1..65535")
	}
	// A number may be declared for several protocols (DNS: TCP and UDP
	// 53): one supported entry is enough.
	var refused *core.ForwardPort
	found := false
	for i, p := range info.Ports {
		if p.Port != req.Port {
			continue
		}
		if p.Supported {
			found = true
			break
		}
		if refused == nil {
			refused = &info.Ports[i]
		}
	}
	switch {
	case !found && refused != nil:
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: refused.Reason}
	case !found && !info.AnyPort:
		return nil, invalid("%s has no port %d", ref.Name, req.Port)
	}
	def := s.kinds.byID[ref.Kind]
	return &forwardHandle{conn: s.conn, def: def, ref: ref, ns: u.GetNamespace(), name: u.GetName(), uid: u.GetUID(), port: req.Port,
		dialer: &forwardDialer{c: s.conn, deadAfter: pfDeadAfter, ws: true}}, nil
}

// forwardHandle is a tunnel's pinned target: the object (by UID), not one
// pod — a Service or workload chooses a pod on every connect.
type forwardHandle struct {
	conn   *conn
	def    *kindDef
	ref    core.Ref
	ns     string
	name   string
	uid    types.UID
	port   int
	dialer *forwardDialer
}

func (h *forwardHandle) Describe() core.LiveTarget {
	return core.LiveTarget{
		Provider: ProviderID, Target: h.conn.target, TargetTitle: h.conn.targetTitle, Endpoint: h.conn.endpoint,
		ConfigHash: h.conn.hash, Ref: h.ref, Port: h.port,
	}
}

// Close: the handle holds no connection (upstreams are closed by the
// tunnel).
func (h *forwardHandle) Close() {}

// Connect chooses a pod (checking the pinned object is still the same
// one), resolves the port on it and negotiates.
func (h *forwardHandle) Connect(ctx context.Context) (provider.Upstream, error) {
	pod, port, err := h.choose(ctx)
	if err != nil {
		return nil, err
	}
	c, via, err := h.dialer.dial(ctx, h.ns, pod.GetName())
	if err != nil {
		return nil, forwardError(err)
	}
	u := newPFUpstream(c, fmt.Sprintf("%s:%d", pod.GetName(), port), port)
	u.via = via
	u.alive = func(ctx context.Context) error { return h.podStillServes(ctx, pod.GetName(), pod.GetUID()) }
	return u, nil
}

// podStillServes: the pod an upstream was opened to still exists as that
// incarnation and runs.
func (h *forwardHandle) podStillServes(ctx context.Context, name string, uid types.UID) error {
	p, err := h.conn.getPod(ctx, h.ns, name)
	var pe *provider.Error
	switch {
	case errors.As(err, &pe) && pe.Class == provider.ClassNotFound:
		return gone("pod %s no longer exists", name)
	case err != nil:
		return err
	case p.GetUID() != uid:
		return gone("pod %s was replaced", name)
	case !runnable(p): // Running only moves on to deletion or an end
		return gone("pod %s is no longer running (it is %s)", name, podState(p))
	}
	return nil
}

func podState(p *unstructured.Unstructured) string {
	if p.GetDeletionTimestamp() != nil {
		return "being deleted"
	}
	return nonEmpty(str(p.Object, "status", "phase"), "unknown")
}

// forwardError explains a refused port-forward.
func forwardError(err error) error {
	var pe *provider.Error
	if errors.As(err, &pe) || errors.Is(err, context.Canceled) {
		return err
	}
	class, msg := classify(err)
	if class == provider.ClassForbidden {
		msg += " (tunnels need the pods/portforward permission)"
	}
	return &provider.Error{Class: class, Message: msg}
}

func gone(format string, a ...any) error {
	return &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf(format, a...)}
}

func unavailable(format string, a ...any) error {
	return &provider.Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf(format, a...)}
}

// pinned GETs the target and checks it is the object the tunnel was
// started for.
func (h *forwardHandle) pinned(ctx context.Context) (*unstructured.Unstructured, error) {
	u, err := h.conn.get(ctx, h.def, h.ns, h.name)
	var pe *provider.Error
	switch {
	case errors.As(err, &pe) && pe.Class == provider.ClassNotFound:
		return nil, gone("%s %s no longer exists", noun(h.def), h.name)
	case err != nil:
		return nil, err
	case u.GetUID() != h.uid:
		return nil, gone("%s %s was replaced by a new one with the same name; start a new tunnel", noun(h.def), h.name)
	}
	return u, nil
}

// noun: "Service" for the Services kind.
func noun(def *kindDef) string { return strings.TrimSuffix(def.desc.Title, "s") }

func (h *forwardHandle) choose(ctx context.Context) (*unstructured.Unstructured, int, error) {
	u, err := h.pinned(ctx)
	if err != nil {
		return nil, 0, err
	}
	switch h.def {
	case podsKind:
		if !runnable(u) {
			return nil, 0, unavailable("pod %s is not running (it is %s)", h.name, podState(u))
		}
		return u, h.port, nil
	case servicesKind:
		return h.chooseForService(ctx, u)
	}
	pods, err := workloadPods(ctx, h.conn.dyn, h.def, u)
	if err != nil {
		return nil, 0, err
	}
	live := runningPods(pods)
	if len(live) == 0 {
		return nil, 0, unavailable("%s has no running pods", h.name)
	}
	return live[0], h.port, nil
}

func runningPods(pods []*unstructured.Unstructured) []*unstructured.Unstructured {
	var out []*unstructured.Unstructured
	for _, p := range pods {
		if runnable(p) {
			out = append(out, p)
		}
	}
	rankPods(out)
	return out
}

// chooseForService re-reads the Service on every connect (selector and
// targetPort edits apply from the next connection) and resolves a named
// targetPort on the chosen pod.
func (h *forwardHandle) chooseForService(ctx context.Context, svc *unstructured.Unstructured) (*unstructured.Unstructured, int, error) {
	if why := serviceUnsupported(svc.Object); why != "" {
		return nil, 0, &provider.Error{Class: provider.ClassUnsupported, Message: why}
	}
	var sp map[string]any
	for _, p := range slice(svc.Object, "spec", "ports") {
		if int(i64(p, "port")) == h.port && nonEmpty(strOf(p, "protocol"), "TCP") == "TCP" {
			sp = p
		}
	}
	if sp == nil {
		return nil, 0, gone("Service %s no longer has TCP port %d", h.name, h.port)
	}
	sel := labels.SelectorFromSet(stringMap(svc.Object, "spec", "selector")).String()
	pods, err := listAll(ctx, h.conn.dyn, podsKind, h.ns, sel)
	if err != nil {
		return nil, 0, err
	}
	live := runningPods(pods)
	if len(live) == 0 {
		return nil, 0, unavailable("Service %s selects no running pods", h.name)
	}
	target := targetPortText(sp)
	if target == "" {
		return live[0], h.port, nil
	}
	if n, err := strconv.Atoi(target); err == nil {
		return live[0], n, nil
	}
	var why error
	for _, p := range live {
		n, err := namedPort(p, target)
		if err == nil {
			return p, n, nil
		}
		why = err
	}
	return nil, 0, why
}

// namedPort resolves a Service's named targetPort on one pod: exactly one
// TCP container port with that name, no guessing.
func namedPort(p *unstructured.Unstructured, name string) (int, error) {
	var found []int
	for _, key := range []string{"containers", "initContainers"} {
		for _, c := range slice(p.Object, "spec", key) {
			for _, cp := range slice(c, "ports") {
				if strOf(cp, "name") == name && nonEmpty(strOf(cp, "protocol"), "TCP") == "TCP" {
					found = append(found, int(i64(cp, "containerPort")))
				}
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return 0, unavailable("pod %s has no TCP port named %q (the Service's targetPort)", p.GetName(), name)
	}
	return 0, unavailable("pod %s has %d ports named %q; the Service's targetPort is ambiguous", p.GetName(), len(found), name)
}
