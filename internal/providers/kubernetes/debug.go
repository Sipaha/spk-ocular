package kubernetes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Debug containers (P16): an ephemeral container added to a running pod,
// as kubectl debug -it --image --target does, and a terminal attached to
// it. The container's name is chosen at the review and carried in a signed
// grant (the plan's Expect), spent before the write: one review adds at
// most one container. No stdinOnce (as kubectl debug): with a tty the end
// of an attach never reaches the shell (checked on kind) — a closed tab
// ends it by the terminal's hang-up keys (streams), and after a lost
// connection to the cluster the debugger keeps running for "Reconnect".

const (
	defaultDebugImage = "busybox:1.36"
	maxDebugImage     = 512
	// noTarget: the choice of no target container (a pod sharing its
	// process namespace); not a container name (DNS labels have no '*').
	noTarget = "*none"
	// debugGrantTag separates debug grants from the key's other uses.
	debugGrantTag = "spk-ocular/p16/debug-grant/v1\x00"
	mirrorKey     = "kubernetes.io/config.mirror"
	enforceKey    = "pod-security.kubernetes.io/enforce"
)

var actDebug = core.ActionDescriptor{
	ID: "debug", Title: "Debug", Param: &core.ActionParam{Kind: core.ParamChoice, Title: msgp("debug.targets")}, NoAgents: true,
	Text: &core.ActionText{Title: msg("debug.image"), Default: defaultDebugImage, Max: maxDebugImage},
}

var (
	debuggerRe  = regexp.MustCompile(`^debugger-[a-z0-9]{5}$`)
	namespaceGV = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
)

// debugState: what a debug review read of the pod — its incarnation,
// deletion, phase, containers (all kinds) and process namespace sharing.
// The target's own state is not part of it: a crash-looping target is the
// usual reason to debug.
func debugState(u *unstructured.Unstructured) string {
	o := u.Object
	names := func(key string) []string {
		var out []string
		for _, c := range slice(o, "spec", key) {
			out = append(out, str(c, "name"))
		}
		sort.Strings(out)
		return out
	}
	b, _ := json.Marshal(map[string]any{
		"uid": string(u.GetUID()), "deleting": u.GetDeletionTimestamp() != nil, "phase": str(o, "status", "phase"),
		"containers": names("containers"), "init": names("initContainers"), "ephemeral": names("ephemeralContainers"),
		"share": boolAt(o, "spec", "shareProcessNamespace"), "mirror": u.GetAnnotations()[mirrorKey] != "",
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// debugGrant is what a debug review promised, signed.
type debugGrant struct {
	Action    string `json:"act"`
	Route     string `json:"route"`
	NS        string `json:"ns"`
	Pod       string `json:"pod"`
	UID       string `json:"uid"`
	State     string `json:"state"`
	Container string `json:"container"`
	Image     string `json:"image"`
	Target    string `json:"target"`
	Exp       int64  `json:"exp"` // unix ms
	Inc       string `json:"inc"` // the session incarnation
	Nonce     string `json:"nonce"`
}

func debugGrantSig(payload []byte) string {
	m := hmac.New(sha256.New, printKey)
	m.Write([]byte(debugGrantTag))
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

func (g debugGrant) expect() string {
	b, _ := json.Marshal(g)
	return g.Route + "-" + base64.RawURLEncoding.EncodeToString(b) + "." + debugGrantSig(b)
}

// readDebugGrant checks a run's Expect: its signature, and that it names
// this action, route, pod, the run's image and target and a debugger name.
func readDebugGrant(def *kindDef, run provider.ActionRun) (debugGrant, error) {
	var g debugGrant
	bad := provider.Said(provider.ClassInvalid, msg("run.invalid"))
	if len(run.Expect) > maxGrantLen || run.Params.Text == nil || run.Params.Choice == nil {
		return g, bad
	}
	route, rest, ok := strings.Cut(run.Expect, "-")
	if !ok {
		return g, bad
	}
	enc, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return g, bad
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || !hmac.Equal([]byte(sig), []byte(debugGrantSig(payload))) {
		return g, bad
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&g) != nil {
		return g, bad
	}
	ref := run.Ref
	if route != g.Route || g.Action != actDebug.ID || g.Route != routeOf(def) || g.NS != ref.Scope || g.Pod != ref.Name || g.UID != ref.UID ||
		!debuggerRe.MatchString(g.Container) || g.Image != *run.Params.Text || g.Target != *run.Params.Choice || g.Nonce == "" {
		return g, bad
	}
	return g, nil
}

// debugTargets: the pod's regular and sidecar containers (their image and
// state in the details), and no target when the pod shares its process
// namespace. The default: the default-container annotation, else the first.
func debugTargets(u *unstructured.Unstructured) ([]core.ActionChoice, string) {
	o := u.Object
	var out []core.ActionChoice
	images := map[string]string{}
	for _, key := range []string{"containers", "initContainers"} {
		for _, c := range slice(o, "spec", key) {
			images[str(c, "name")] = str(c, "image")
		}
	}
	// From the spec, whatever the state (a crash-looping sidecar is the
	// usual reason to debug it): the state is only a detail.
	states := map[string]map[string]any{}
	for _, key := range []string{"containerStatuses", "initContainerStatuses"} {
		for _, cs := range slice(o, "status", key) {
			st, _ := cs["state"].(map[string]any)
			states[key+"/"+strOf(cs, "name")] = st
		}
	}
	for _, ch := range podChannels(o, "spec") {
		key := "containerStatuses/"
		switch ch.Note {
		case ctrEphemeral, ctrInit:
			continue
		case ctrSidecar:
			key = "initContainerStatuses/"
		}
		c := core.ActionChoice{Value: ch.ID, Title: msg("debug.container", "name", ch.ID),
			Details: []core.Message{msg("debug.targetImage", "image", images[ch.ID]), msg("debug.targetState", "state", nonEmpty(stateText(states[key+ch.ID]), "not started"))}}
		out = append(out, c)
	}
	def := ""
	if len(out) > 0 {
		def = out[0].Value
		if a := u.GetAnnotations()[defaultContainerAnnotation]; a != "" {
			for _, c := range out {
				if c.Value == a {
					def = a
				}
			}
		}
	}
	if boolAt(o, "spec", "shareProcessNamespace") {
		out = append(out, core.ActionChoice{Value: noTarget, Title: msg("debug.noTarget"), Details: []core.Message{msg("debug.noTargetShared")}})
	}
	return out, def
}

// debugUnavailable: why no debugger can be added to u (nil: it can).
func debugUnavailable(u *unstructured.Unstructured) *core.Message {
	switch phase := str(u.Object, "status", "phase"); {
	case u.GetAnnotations()[mirrorKey] != "":
		m := msg("debug.mirror", "name", u.GetName())
		return &m
	case phase != "Running":
		m := msg("debug.notRunning", "name", u.GetName(), "phase", nonEmpty(phase, "Unknown"))
		return &m
	}
	return nil
}

func validImage(image string) bool {
	return image != "" && len(image) <= maxDebugImage && !strings.ContainsFunc(image, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

// prepareDebug offers the targets (with the default chosen when none was),
// names the debugger, signs the grant and says what happens.
func (s *session) prepareDebug(ctx context.Context, def *kindDef, plan core.ActionPlan, u *unstructured.Unstructured) core.ActionPlan {
	targets, dflt := debugTargets(u)
	plan.Choices = targets
	image := defaultDebugImage
	if plan.Params.Text != nil {
		image = *plan.Params.Text
	}
	target := dflt
	if plan.Params.Choice != nil {
		target = *plan.Params.Choice
	}
	plan.Params.Text, plan.Params.Choice = &image, &target
	if plan.Unavailable != nil {
		return plan
	}
	if why := debugUnavailable(u); why != nil {
		plan.Unavailable = why
		return plan
	}
	if !validImage(image) {
		m := msg("debug.badImage")
		plan.Unavailable = &m
		return plan
	}
	known := false
	for _, c := range targets {
		known = known || c.Value == target
	}
	if !known {
		m := msg("debug.noSuchTarget", "name", target)
		plan.Unavailable = &m
		return plan
	}
	name := "debugger-" + randomSuffix()
	plan.Expect = debugGrant{
		Action: actDebug.ID, Route: routeOf(def), NS: u.GetNamespace(), Pod: u.GetName(), UID: string(u.GetUID()), State: debugState(u),
		Container: name, Image: image, Target: target, Exp: s.now().Add(runGrantTTL).UnixMilli(), Inc: s.incarnation, Nonce: randomHex(16),
	}.expect()
	plan.Effects = []core.Message{msg("debug.effect", "container", name, "image", image, "name", u.GetName())}
	if target == noTarget {
		plan.Effects = append(plan.Effects, msg("debug.seesAll"))
	} else {
		plan.Effects = append(plan.Effects, msg("debug.sees", "target", target))
	}
	plan.Effects = append(plan.Effects, msg("debug.stays"), msg("debug.terminal"))
	plan.Warnings = append(plan.Warnings, msg("debug.registry"))
	if s.enforcesRestricted(ctx, u.GetNamespace()) {
		plan.Warnings = append(plan.Warnings, msg("debug.restricted", "namespace", u.GetNamespace()))
	}
	return plan
}

// enforcesRestricted: the namespace enforces the restricted Pod Security
// level (a debugger without a securityContext is rejected); not readable —
// false: the plan does not depend on it.
func (s *session) enforcesRestricted(ctx context.Context, ns string) bool {
	ctx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	n, err := s.dyn.Resource(namespaceGV).Get(ctx, ns, metav1.GetOptions{})
	return err == nil && n.GetLabels()[enforceKey] == "restricted"
}

// ephemeral: u's ephemeral container of that name (nil: none).
func ephemeral(u *unstructured.Unstructured, name string) map[string]any {
	for _, c := range slice(u.Object, "spec", "ephemeralContainers") {
		if str(c, "name") == name {
			return c
		}
	}
	return nil
}

// ours: an ephemeral container is the one g writes.
func (g debugGrant) ours(c map[string]any) bool {
	target := g.Target
	if target == noTarget {
		target = ""
	}
	stdin, _ := c["stdin"].(bool)
	tty, _ := c["tty"].(bool)
	return str(c, "image") == g.Image && str(c, "targetContainerName") == target && stdin && tty
}

// runDebug: the grant checked, the pod read by UID; a container of the
// grant's name already there is done when it is ours (an earlier attempt
// landed), else a conflict — checked before the state (the names are part
// of it); the grant is spent before the first write; one PATCH per
// attempt, retried only after a failed version precondition.
func (s *session) runDebug(ctx context.Context, def *kindDef, run provider.ActionRun) (core.ActionResult, error) {
	g, err := readDebugGrant(def, run)
	if err != nil {
		return core.ActionResult{}, err
	}
	if g.Inc != s.incarnation {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("run.earlier"))
	}
	if !s.now().Before(time.UnixMilli(g.Exp)) {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("run.expired"))
	}
	if s.spent.has(g.Nonce) { // a replay: its container was asked for already
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("run.spent"))
	}
	done := func(u *unstructured.Unstructured) core.ActionResult {
		ref := run.Ref
		ref.UID = string(u.GetUID())
		return core.ActionResult{Message: msg("done.debug", "kind", singular(def), "name", u.GetName(), "container", g.Container),
			Terminal: &core.TerminalOpen{Ref: ref, Channel: g.Container, Attach: true}}
	}
	spent := false
	for attempt := 0; ; attempt++ {
		u, err := s.getConfirmed(ctx, def, run.Ref)
		if err != nil {
			return core.ActionResult{}, err
		}
		if c := ephemeral(u, g.Container); c != nil {
			if g.ours(c) {
				return done(u), nil
			}
			return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("debug.nameTaken", "container", g.Container))
		}
		if why := debugUnavailable(u); why != nil {
			return core.ActionResult{}, provider.Said(provider.ClassConflict, *why)
		}
		if actionUnavailable(def, run.Action, u) != nil || debugState(u) != g.State {
			return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.changed", "kind", singular(def), "name", u.GetName()))
		}
		if s.beforeWrite != nil {
			s.beforeWrite(run.Action, u)
		}
		if !spent {
			if err := ctx.Err(); err != nil {
				return core.ActionResult{}, provider.Said(provider.ClassUnavailable, msg("error.nothingWritten", "detail", err.Error()))
			}
			if err := s.spent.spend(runGrant{Nonce: g.Nonce, Exp: g.Exp}, s.now); err != nil {
				return core.ActionResult{}, err
			}
			spent = true
		}
		err = s.writeDebug(ctx, def, u, g)
		if err == nil {
			return done(u), nil
		}
		// Re-read as the plan's state; our container landed meanwhile
		// counts as unchanged, so the next attempt finds it.
		byState := provider.ActionRun{Ref: run.Ref, Action: run.Action, Params: run.Params, Expect: g.State}
		retry, err := s.failedWrite(ctx, def, byState, u, err, func(_ context.Context, now *unstructured.Unstructured) (string, error) {
			if ephemeral(now, g.Container) != nil {
				return g.State, nil
			}
			return debugState(now), nil
		})
		if !retry || attempt == maxVersionRetries {
			var pe *provider.Error
			if errors.As(err, &pe) && pe.Class == provider.ClassUnknown {
				return core.ActionResult{}, provider.Said(provider.ClassUnknown, msg("debug.unknown", "container", g.Container, "name", u.GetName()))
			}
			return core.ActionResult{}, err
		}
	}
}

// writeDebug: one strategic merge PATCH of pods/<name>/ephemeralcontainers
// (the list merges by name: the others stay), with u's UID and version as
// preconditions (a stale version is a 409).
func (s *session) writeDebug(ctx context.Context, def *kindDef, u *unstructured.Unstructured, g debugGrant) error {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	c := map[string]any{"name": g.Container, "image": g.Image, "stdin": true, "tty": true, "imagePullPolicy": "IfNotPresent"}
	if g.Target != noTarget {
		c["targetContainerName"] = g.Target
	}
	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()},
		"spec":     map[string]any{"ephemeralContainers": []any{c}},
	})
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	return wr.patch(ctx, def.gvr, u.GetNamespace(), u.GetName(), types.StrategicMergePatchType, body, "ephemeralcontainers")
}
