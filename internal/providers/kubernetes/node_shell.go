package kubernetes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const nodeShellLabel = "spk-ocular.dev/node-shell"
const nodeShellImage = "alpine:3.24.2"

func nodeShellHelper(p *unstructured.Unstructured) bool {
	// The generated name remains reserved even if somebody edits its labels.
	return p.GetLabels()[nodeShellLabel] != "" || strings.HasPrefix(p.GetName(), "ocular-node-shell-")
}

var actNodeShell = core.ActionDescriptor{
	ID: "node-shell", Title: "Node Shell", NoAgents: true, Single: true, Terminal: true,
	Param: &core.ActionParam{Kind: core.ParamChoice, Title: msgp("nodeShell.namespace"), Custom: true},
	Text:  &core.ActionText{Title: msg("debug.image"), Default: nodeShellImage, Max: maxDebugImage},
}

type nodeShellGrant struct {
	Shared       string    `json:"shared,omitempty"`
	Action       string    `json:"action"`
	Ref          core.Ref  `json:"ref"`
	Namespace    string    `json:"ns"`
	NamespaceUID types.UID `json:"nsUID"`
	Policy       string    `json:"policy"`
	Image        string    `json:"image"`
	Name         string    `json:"name"`
	Nonce        string    `json:"nonce"`
	Inc          string    `json:"inc"`
	Exp          int64     `json:"exp"`
}

func nodeShellSignature(b []byte) string {
	m := hmac.New(sha256.New, printKey)
	m.Write([]byte("spk-ocular/node-shell/v1\x00"))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}
func (g nodeShellGrant) token() string {
	b, _ := json.Marshal(g)
	return base64.RawURLEncoding.EncodeToString(b) + "." + nodeShellSignature(b)
}
func readNodeShellGrant(run provider.ActionRun) (nodeShellGrant, error) {
	var g nodeShellGrant
	bad := provider.Said(provider.ClassInvalid, msg("run.invalid"))
	if len(run.Expect) > maxGrantLen || (run.Action != actNodeShell.ID && run.Action != actNodeFiles.ID) || run.Params.Text == nil || run.Params.Choice == nil {
		return g, bad
	}
	enc, sig, ok := strings.Cut(run.Expect, ".")
	if !ok {
		return g, bad
	}
	b, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || !hmac.Equal([]byte(sig), []byte(nodeShellSignature(b))) || json.Unmarshal(b, &g) != nil {
		return g, bad
	}
	if g.Action != run.Action || g.Ref != run.Ref || g.Image != *run.Params.Text || g.Namespace != *run.Params.Choice || g.Nonce == "" {
		return g, bad
	}
	return g, nil
}
func nodeShellUnavailable(u *unstructured.Unstructured) *core.Message {
	os := str(u.Object, "status", "nodeInfo", "operatingSystem")
	if os == "" {
		os = u.GetLabels()["kubernetes.io/os"]
	}
	if os != "linux" {
		m := msg("nodeShell.linux")
		return &m
	}
	return actionUnavailable(nodesKind, actNodeShell.ID, u)
}

func (s *session) prepareNodeShell(ctx context.Context, plan core.ActionPlan, node *unstructured.Unstructured) core.ActionPlan {
	plan.Expect = ""
	ctx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	if why := nodeShellUnavailable(node); why != nil {
		plan.Unavailable = why
		return plan
	}
	if reused, err := s.conn.helpers.reusable(ctx, plan.Where.Ref); err != nil {
		why := core.Message{Text: err.Error()}
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Why != nil {
			why = *pe.Why
		}
		plan.Unavailable = &why
		return plan
	} else if reused != nil {
		return s.prepareWarmNodeHelper(ctx, plan, reused)
	}
	image, ns := nodeShellImage, "kube-system"
	if plan.Params.Text != nil {
		image = *plan.Params.Text
	}
	if plan.Params.Choice != nil {
		ns = *plan.Params.Choice
	}
	plan.Params = core.ActionParams{Text: &image, Choice: &ns}
	plan.Unavailable = nodeShellUnavailable(node)
	if !validImage(image) || len(validation.IsDNS1123Label(ns)) > 0 {
		m := msg("nodeShell.invalid")
		plan.Unavailable = &m
		plan.Expect = ""
		return plan
	}
	n, err := s.dyn.Resource(namespaceGV).Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		m := msg("nodeShell.namespaceError", "namespace", ns)
		plan.Unavailable = &m
	}
	if n != nil && (n.GetDeletionTimestamp() != nil || n.GetUID() == "") {
		m := msg("nodeShell.namespaceError", "namespace", ns)
		plan.Unavailable = &m
	}
	plan.Effects = []core.Message{msg("nodeShell.effect", "name", node.GetName(), "namespace", ns, "image", image), msg("nodeShell.cleanup")}
	if plan.Action.Files {
		plan.Effects = []core.Message{msg("nodeFiles.effect", "name", node.GetName(), "namespace", ns, "image", image), msg("nodeFiles.cleanup")}
	}
	plan.Warnings = []core.Message{msg("nodeShell.privileged"), msg("debug.registry"), msg("nodeShell.requirements")}
	if n != nil && (n.GetLabels()[enforceKey] == "baseline" || n.GetLabels()[enforceKey] == "restricted") {
		plan.Warnings = append(plan.Warnings, msg("nodeShell.policy", "namespace", ns))
	}
	if plan.Unavailable != nil {
		plan.Expect = ""
		return plan
	}
	g := nodeShellGrant{Action: plan.Action.ID, Ref: plan.Where.Ref, Namespace: ns, NamespaceUID: n.GetUID(), Policy: n.GetLabels()[enforceKey], Image: image, Name: "ocular-node-shell-" + randomHex(8), Nonce: randomHex(16), Inc: s.incarnation, Exp: s.now().Add(runGrantTTL).UnixMilli()}
	plan.Expect = g.token()
	plan.Rights = s.nodeShellRights(ctx, ns, g.Name)
	return plan
}

func (s *session) prepareWarmNodeHelper(ctx context.Context, plan core.ActionPlan, e *nodeHelperEntry) core.ActionPlan {
	g := e.owner.g
	if (plan.Params.Text != nil && *plan.Params.Text != g.Image) || (plan.Params.Choice != nil && *plan.Params.Choice != g.Namespace) {
		plan.Unavailable = msgp("nodeShell.sharedConfig", "namespace", g.Namespace, "image", g.Image)
		return plan
	}
	g.Shared, g.Action, g.Nonce, g.Inc, g.Exp = e.owner.g.Nonce, plan.Action.ID, randomHex(16), s.incarnation, s.now().Add(runGrantTTL).UnixMilli()
	image, namespace := g.Image, g.Namespace
	plan.Params = core.ActionParams{Text: &image, Choice: &namespace}
	plan.Reuse = true
	plan.Expect = g.token()
	plan.Effects = []core.Message{msg("nodeShell.reuse", "namespace", g.Namespace, "name", g.Name)}
	plan.Warnings = []core.Message{msg("nodeShell.privileged")}
	plan.Choices = []core.ActionChoice{{Value: g.Namespace, Title: core.Message{Text: g.Namespace}}}
	plan.Rights = s.nodeHelperRights(ctx, g.Namespace, g.Name, true)
	return plan
}

func (s *session) nodeShellRights(ctx context.Context, ns, name string) core.Rights {
	return s.nodeHelperRights(ctx, ns, name, false)
}
func (s *session) nodeHelperRights(ctx context.Context, ns, name string, reuse bool) core.Rights {
	u := &unstructured.Unstructured{}
	u.SetNamespace(ns)
	rights := []struct{ verb, sub string }{{"get", ""}, {"create", "exec"}, {"get", "exec"}}
	if !reuse {
		rights = append(rights, struct{ verb, sub string }{"create", ""}, struct{ verb, sub string }{"watch", ""}, struct{ verb, sub string }{"delete", ""})
	}
	for _, q := range rights {
		attrs := map[string]any{"verb": q.verb, "group": "", "resource": "pods", "namespace": ns}
		if q.verb != "create" || q.sub != "" {
			attrs["name"] = name
		}
		if q.sub != "" {
			attrs["subresource"] = q.sub
		}
		r := s.rightsTo(ctx, q.verb, attrs, q.sub, u)
		if r.State != core.RightsAllowed {
			return r
		}
	}
	return core.Rights{State: core.RightsAllowed}
}

// PrepareTerminalAction admits a signed review, without creating a Pod.
// A handle retains the reviewed connection even after target switching.
func (s *session) PrepareTerminalAction(ctx context.Context, run provider.ActionRun) (provider.ExecHandle, error) {
	if run.Action != actNodeShell.ID {
		return nil, invalid("Node Shell requires a shell review")
	}
	h, err := s.prepareNodeHelper(ctx, run)
	if err != nil {
		return nil, err
	}
	lease, err := s.conn.helpers.acquire(ctx, h)
	if err != nil {
		return nil, err
	}
	return &sharedNodeShellHandle{lease: lease, started: &atomic.Bool{}}, nil
}
func (s *session) prepareNodeHelper(ctx context.Context, run provider.ActionRun) (*nodeShellHandle, error) {
	if s.kind(run.Ref.Kind) != nodesKind {
		return nil, invalid("Node Shell requires a node")
	}
	g, err := readNodeShellGrant(run)
	if err != nil {
		return nil, err
	}
	if g.Inc != s.incarnation {
		return nil, provider.Said(provider.ClassConflict, msg("run.earlier"))
	}
	if !s.now().Before(time.UnixMilli(g.Exp)) {
		return nil, provider.Said(provider.ClassConflict, msg("run.expired"))
	}
	if err := validateNodeShell(ctx, s.conn, g); err != nil {
		return nil, err
	}
	if err := s.spent.spend(runGrant{Nonce: g.Nonce, Exp: g.Exp}, s.now); err != nil {
		return nil, err
	}
	w := s.writer
	if w == nil {
		w = dynWriter{s.dyn}
	}
	return &nodeShellHandle{conn: s.conn, writer: w, g: g, state: &nodeShellState{done: make(chan struct{})}, now: s.now}, nil
}

func validateNodeShell(ctx context.Context, c *conn, g nodeShellGrant) error {
	n, err := c.get(ctx, nodesKind, "", g.Ref.Name)
	if err != nil {
		return err
	}
	if string(n.GetUID()) != g.Ref.UID {
		return provider.Said(provider.ClassGone, msg("error.replacedMeanwhile", "kind", "node", "name", g.Ref.Name))
	}
	if why := nodeShellUnavailable(n); why != nil {
		return provider.Said(provider.ClassConflict, *why)
	}
	ns, err := c.dyn.Resource(namespaceGV).Get(ctx, g.Namespace, metav1.GetOptions{})
	if err != nil {
		cl, m := classify(err)
		return &provider.Error{Class: cl, Message: m}
	}
	if ns.GetUID() != g.NamespaceUID || ns.GetDeletionTimestamp() != nil {
		return provider.Said(provider.ClassConflict, msg("nodeShell.namespaceError", "namespace", g.Namespace))
	}
	if ns.GetLabels()[enforceKey] != g.Policy {
		return provider.Said(provider.ClassConflict, msg("nodeShell.namespaceError", "namespace", g.Namespace))
	}
	return nil
}

type nodeShellHandle struct {
	journal provider.HelperJournal
	conn    *conn
	writer  actionWriter
	g       nodeShellGrant
	state   *nodeShellState
	now     func() time.Time
}

type nodeShellState struct {
	started atomic.Bool
	done    chan struct{}
	// Published by closing done; read only after receiving from done.
	cleanupError error
	helperUID    types.UID
}

type resultCreator interface {
	createResult(context.Context, schema.GroupVersionResource, string, *unstructured.Unstructured) (*unstructured.Unstructured, error)
}

func (h *nodeShellHandle) Describe() core.LiveTarget {
	return core.LiveTarget{Provider: ProviderID, Target: h.conn.target, TargetTitle: h.conn.targetTitle, Endpoint: h.conn.endpoint, ConfigHash: h.conn.hash, Ref: h.g.Ref, Instance: h.g.Name, Channel: "shell"}
}
func (h *nodeShellHandle) Again() (provider.ExecHandle, error) {
	if h.state.started.Load() {
		return nil, provider.Said(provider.ClassGone, msg("nodeShell.ended"))
	}
	c := *h
	return &c, nil
}
func (h *nodeShellHandle) Close()                                    {}
func (h *nodeShellHandle) SetHelperJournal(j provider.HelperJournal) { h.journal = j }

func (h *nodeShellHandle) WaitCleanup(ctx context.Context) error {
	if !h.state.started.Load() {
		return nil // No CREATE can begin after a revoked stream's context ends.
	}
	select {
	case <-h.state.done:
		return h.state.cleanupError
	case <-ctx.Done():
		return provider.Said(provider.ClassUnknown, msg("nodeShell.cleanupFailed", "namespace", h.g.Namespace, "name", h.g.Name, "detail", ctx.Err().Error()))
	}
}

func (g nodeShellGrant) pod() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": g.Name, "namespace": g.Namespace, "labels": map[string]any{nodeShellLabel: g.Nonce}, "ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Node", "name": g.Ref.Name, "uid": g.Ref.UID}}},
		"spec": map[string]any{
			"nodeName": g.Ref.Name, "hostPID": true, "hostNetwork": true, "hostIPC": true, "restartPolicy": "Never", "automountServiceAccountToken": false, "activeDeadlineSeconds": int64(3600), "terminationGracePeriodSeconds": int64(0), "dnsPolicy": "ClusterFirstWithHostNet",
			"tolerations": []any{map[string]any{"operator": "Exists"}},
			"containers":  []any{map[string]any{"name": "shell", "image": g.Image, "imagePullPolicy": "IfNotPresent", "command": []any{"sleep", "3600"}, "securityContext": map[string]any{"privileged": true, "runAsUser": int64(0)}, "resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}}}},
		},
	}}
}

func (h *nodeShellHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	return h.run(ctx, t, func(ctx context.Context, e *execHandle) (provider.ExitStatus, error) { return e.Run(ctx, t) })
}
func (h *nodeShellHandle) run(ctx context.Context, t provider.Terminal, command func(context.Context, *execHandle) (provider.ExitStatus, error)) (status provider.ExitStatus, err error) {
	if !h.state.started.CompareAndSwap(false, true) {
		return status, provider.Said(provider.ClassGone, msg("nodeShell.ended"))
	}
	defer close(h.state.done)
	ctx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	if !h.now().Before(time.UnixMilli(h.g.Exp)) {
		return status, provider.Said(provider.ClassConflict, msg("run.expired"))
	}
	if err = validateNodeShell(ctx, h.conn, h.g); err != nil {
		return status, err
	}
	if err = ctx.Err(); err != nil {
		return status, err
	}
	creator, ok := h.writer.(resultCreator)
	if !ok {
		return status, invalid("Node Shell requires a create transport that retains resource identity")
	}
	var uid types.UID
	needsCleanup := false
	var lease provider.HelperLease
	// Cleanup uses an independent deadline even after tab cancellation. The
	// random ownership marker and UID guard also cover an ambiguous CREATE.
	defer func() {
		clean := true
		if needsCleanup {
			if cleanErr := h.cleanup(uid); cleanErr != nil {
				clean = false
				m := msg("nodeShell.cleanupFailed", "namespace", h.g.Namespace, "name", h.g.Name, "detail", cleanErr.Error())
				if t.Notice != nil {
					t.Notice(m)
				}
				h.state.cleanupError = provider.Said(provider.ClassUnknown, m)
				slog.Warn("Node Shell helper cleanup not confirmed", "target", h.conn.target, "namespace", h.g.Namespace, "pod", h.g.Name)
				err = errors.Join(err, h.state.cleanupError)
			}
		}
		if lease != nil {
			finish, cancel := context.WithTimeout(context.Background(), time.Second)
			finishErr := lease.Finish(finish, clean)
			cancel()
			if finishErr != nil {
				err = errors.Join(err, fmt.Errorf("cannot finalize helper recovery record: %w", finishErr))
			}
		}
	}()
	if h.journal != nil {
		lease, err = h.journal.Begin(ctx, core.OwnedHelper{ID: h.g.Nonce, Provider: ProviderID, Target: h.conn.target, Namespace: h.g.Namespace, NamespaceUID: string(h.g.NamespaceUID), Name: h.g.Name, NodeName: h.g.Ref.Name, NodeUID: h.g.Ref.UID}, stopRun)
		if err != nil {
			return status, err
		}
	}
	cctx, cancel := context.WithTimeout(ctx, getTimeout)
	created, err := creator.createResult(cctx, podsKind.gvr, h.g.Namespace, h.g.pod())
	cancel()
	if err != nil {
		if ambiguous(err) || !isAPIStatus(err) {
			needsCleanup = true
			return status, provider.Said(provider.ClassUnknown, msg("nodeShell.createUnknown", "namespace", h.g.Namespace, "name", h.g.Name))
		}
		cl, m := classify(err)
		return status, &provider.Error{Class: cl, Message: m}
	}
	needsCleanup = true
	if created == nil || !h.owns(created) {
		return status, provider.Said(provider.ClassConflict, msg("nodeShell.changed"))
	}
	uid = created.GetUID()
	h.state.helperUID = uid
	if lease != nil {
		if err = lease.PinUID(ctx, string(uid)); err != nil {
			return status, err
		}
	}
	p, err := h.conn.getPod(ctx, h.g.Namespace, h.g.Name)
	if err != nil {
		return status, err
	}
	if !h.owns(p) || p.GetUID() != uid {
		return status, provider.Said(provider.ClassConflict, msg("nodeShell.changed"))
	}
	e := &execHandle{conn: h.conn, ref: h.g.Ref, ns: h.g.Namespace, pod: h.g.Name, uid: p.GetUID(), container: "shell",
		argv: []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "-r", "-w", "--", "/bin/sh", "-l"},
	}
	wctx, stop := context.WithTimeout(ctx, h.conn.startWait)
	defer stop()
	if t.Notice != nil {
		t.Notice(msg("nodeShell.waiting"))
	}
	_, err = e.watchPod(wctx, func(p *unstructured.Unstructured) (bool, error) {
		if !h.owns(p) {
			return false, provider.Said(provider.ClassConflict, msg("nodeShell.changed"))
		}
		for _, cs := range slice(p.Object, "status", "containerStatuses") {
			if str(cs, "name") != "shell" {
				continue
			}
			st, _ := cs["state"].(map[string]any)
			if st["running"] != nil {
				return true, nil
			}
			waiting, _ := st["waiting"].(map[string]any)
			if reason := str(waiting, "reason"); startFailures[reason] {
				return false, provider.Said(provider.ClassUnavailable, msg("nodeShell.startFailed", "reason", reason))
			}
		}
		if !execEligible(p) {
			return false, provider.Said(provider.ClassUnavailable, msg("nodeShell.startFailed", "reason", str(p.Object, "status", "phase")))
		}
		return false, nil
	})
	if err != nil {
		if ctx.Err() == nil && wctx.Err() != nil {
			return status, provider.Said(provider.ClassUnavailable, msg("nodeShell.startFailed", "reason", "timeout"))
		}
		return status, err
	}
	if t.Notice != nil {
		t.Notice(core.Message{}) // Startup is complete; clear its transient notice.
	}
	// Recheck the node immediately before exec; the helper may have spent
	// time pulling its image. Reject an observed same-named replacement.
	if err = validateNodeShell(ctx, h.conn, h.g); err != nil {
		return status, err
	}
	return command(ctx, e)
}
func isAPIStatus(err error) bool { var se apierrors.APIStatus; return errors.As(err, &se) }
func (h *nodeShellHandle) owns(p *unstructured.Unstructured) bool {
	if p.GetUID() == "" || p.GetLabels()[nodeShellLabel] != h.g.Nonce || str(p.Object, "spec", "nodeName") != h.g.Ref.Name {
		return false
	}
	if h.g.Ref.UID != "" {
		for _, owner := range p.GetOwnerReferences() {
			if owner.APIVersion == "v1" && owner.Kind == "Node" && owner.Name == h.g.Ref.Name && string(owner.UID) == h.g.Ref.UID {
				return true
			}
		}
		return false
	}
	return true
}
func (h *nodeShellHandle) cleanup(expected types.UID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return h.cleanupWith(ctx, expected)
}
func (h *nodeShellHandle) cleanupWith(ctx context.Context, expected types.UID) error {
	p, err := h.conn.dyn.Resource(podsKind.gvr).Namespace(h.g.Namespace).Get(ctx, h.g.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if expected != "" && p.GetUID() != expected {
		// The original is gone. Even a copied ownership label cannot grant
		// permission to delete a replacement with a different UID.
		return nil
	}
	if !h.owns(p) {
		return fmt.Errorf("helper identity no longer matches; nothing deleted")
	}
	uid, zero := p.GetUID(), int64(0)
	err = h.writer.delete(ctx, podsKind.gvr, h.g.Namespace, h.g.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero, Preconditions: &metav1.Preconditions{UID: &uid}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Admission may have installed a finalizer. A successful DELETE is
	// only a request; confirm disappearance without repeating the write.
	for {
		p, err := h.conn.dyn.Resource(podsKind.gvr).Namespace(h.g.Namespace).Get(ctx, h.g.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) || (err == nil && p.GetUID() != uid) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("helper still exists after deletion: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
