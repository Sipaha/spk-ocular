package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// Actions (P7): a container is restarted, stopped, started or removed; a
// service's containers are restarted, stopped or started one after
// another. The Engine takes no preconditions, so the plan's Expect is a
// local check: the run reads the containers afresh and refuses (conflict)
// before its first write when anything the plan showed changed. The race
// between that check and the write stays (said in the plan). A service
// acts only on the containers of its plan; the first refused or unknown
// write stops the rest (skipped), and the run returns its parts rather
// than an error once a write was sent. Nothing is retried.

var (
	actRestart = core.ActionDescriptor{ID: "restart", Title: "Restart"}
	actStop    = core.ActionDescriptor{ID: "stop", Title: "Stop"}
	actStart   = core.ActionDescriptor{ID: "start", Title: "Start"}
	// actDelete removes a container (the UI's "Delete" key and menu item).
	actDelete = core.ActionDescriptor{ID: "delete", Title: "Remove", Destructive: true}
)

var (
	containerKindActions = []core.ActionDescriptor{actRestart, actStop, actStart, actDelete}
	serviceKindActions   = []core.ActionDescriptor{actRestart, actStop, actStart}
)

var _ provider.Actioner = (*session)(nil)

// rightsUnknown: the Engine has no permissions to ask about.
var rightsUnknown = core.Rights{State: core.RightsUnknown, Reason: "access to the Docker socket allows everything; there are no rights to check"}

func actionOf(kind, id string) (core.ActionDescriptor, error) {
	var list []core.ActionDescriptor
	switch kind {
	case KindContainers:
		list = containerKindActions
	case KindServices:
		list = serviceKindActions
	}
	for _, a := range list {
		if a.ID == id {
			return a, nil
		}
	}
	return core.ActionDescriptor{}, provider.Said(provider.ClassUnsupported, msg("act.noAction", "kind", kind, "action", id))
}

// stopTimeout is the container's stop timeout in seconds (the daemon's
// default without its own): 0 kills at once, −1 never kills.
func stopTimeout(c *engine.ContainerInspect) int {
	if c.Config.StopTimeout != nil {
		return *c.Config.StopTimeout
	}
	return engine.DefaultStopTimeout
}

func stopSignal(c *engine.ContainerInspect) string {
	if c.Config.StopSignal != "" {
		return c.Config.StopSignal
	}
	return "SIGTERM"
}

// actionUnavailable: why action cannot run on c in its state (nil: it can).
func actionUnavailable(action string, c *engine.ContainerInspect) *core.Message {
	st := c.State
	switch {
	case st.Status == "removing":
		m := msg("act.removing")
		return &m
	case st.Dead && action != actDelete.ID:
		m := msg("act.dead")
		return &m
	}
	switch action {
	case actStart.ID:
		if st.Paused {
			m := msg("act.startPaused")
			return &m
		}
		if st.Running {
			m := msg("act.running")
			return &m
		}
	case actStop.ID:
		if !st.Running {
			m := msg("act.notRunning", "state", execState(c))
			return &m
		}
	case actDelete.ID:
		if st.Running {
			m := msg("act.stopFirst", "state", execState(c))
			return &m
		}
	}
	return nil
}

// actionExpect fingerprints the action and, per container (by id), the
// state its effects and availability depend on (health churn is not).
func actionExpect(action string, cs []*engine.ContainerInspect) string {
	type state struct {
		ID, StartedAt, Status, Signal, Policy string
		Running, Paused, Restarting, Dead     bool
		Timeout                               int
		AutoRemove                            bool
	}
	sts := make([]state, 0, len(cs))
	for _, c := range cs {
		sts = append(sts, state{
			ID: c.ID, StartedAt: c.State.StartedAt.Raw, Status: c.State.Status, Signal: stopSignal(c), Policy: c.HostConfig.RestartPolicy.Name,
			Running: c.State.Running, Paused: c.State.Paused, Restarting: c.State.Restarting, Dead: c.State.Dead,
			Timeout: stopTimeout(c), AutoRemove: c.HostConfig.AutoRemove,
		})
	}
	sort.Slice(sts, func(i, j int) bool { return sts[i].ID < sts[j].ID })
	b, _ := json.Marshal(map[string]any{"action": action, "containers": sts})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// removesOnStop: a stop of an AutoRemove container removes it (and its
// anonymous volumes) — the plan is destructive. A restart does not (the
// daemon keeps a manually restarted container).
func removesOnStop(action string, c *engine.ContainerInspect) bool {
	return action == actStop.ID && c.HostConfig.AutoRemove
}

// actionEffects: what action does to c (it can run).
func actionEffects(action string, c *engine.ContainerInspect) []core.Message {
	name, sig, t := containerName(c), stopSignal(c), stopTimeout(c)
	secs := strconv.Itoa(t)
	var out []core.Message
	switch action {
	case actStart.ID:
		out = append(out, msg("act.start", "name", name))
	case actStop.ID:
		switch {
		case t == 0:
			out = append(out, msg("act.stopKill", "name", name))
		case t < 0:
			out = append(out, msg("act.stopWait", "name", name, "signal", sig))
		default:
			out = append(out, msg("act.stop", "name", name, "signal", sig, "timeout", secs))
		}
		if c.HostConfig.AutoRemove {
			out = append(out, msg("act.autoRemove", "name", name))
		}
		if c.HostConfig.RestartPolicy.Name == "always" {
			out = append(out, msg("act.policyAlways", "name", name))
		}
	case actRestart.ID:
		switch {
		case !c.State.Running:
			out = append(out, msg("act.restartStopped", "name", name))
		case t == 0:
			out = append(out, msg("act.restartKill", "name", name))
		case t < 0:
			out = append(out, msg("act.restartWait", "name", name, "signal", sig))
		default:
			out = append(out, msg("act.restart", "name", name, "signal", sig, "timeout", secs))
		}
	case actDelete.ID:
		out = append(out, msg("act.remove", "name", name))
	}
	return out
}

// actionObjects are the containers ref names, read afresh: the container,
// or the service's — listed now by its labels (not from the feed, which
// may not have seen a container created a moment ago) and each inspected
// now; one removed meanwhile is left out (the fingerprint tells).
func (s *session) actionObjects(ctx context.Context, ref core.Ref) ([]*engine.ContainerInspect, error) {
	if ref.Kind == KindContainers {
		c, err := s.container(ctx, ref)
		if err != nil {
			return nil, err
		}
		return []*engine.ContainerInspect{c}, nil
	}
	project, service, ok := strings.Cut(ref.Name, "/")
	if !ok {
		return nil, provider.Said(provider.ClassInvalid, msg("act.notServiceKey", "key", strconv.Quote(ref.Name)))
	}
	listed, err := s.cl.ListContainers(ctx, engine.Filters{"label": {LabelProject + "=" + project, LabelService + "=" + service}})
	if err != nil {
		return nil, providerError(err)
	}
	objs := map[string]any{}
	for _, m := range listed {
		c, err := s.cl.InspectContainer(ctx, m.ID)
		switch {
		case engine.IsNotFound(err):
			continue
		case err != nil:
			return nil, providerError(err)
		}
		objs[c.ID] = &c
	}
	out := membersOf(objs, project, service) // not one-off, in replica order
	if len(out) == 0 {
		return nil, provider.Said(provider.ClassNotFound, msg("act.noContainers"))
	}
	return out, nil
}

// touched: the containers of cs the action runs on (in order), and why
// each of the others is left alone.
func touched(action string, cs []*engine.ContainerInspect) (on []*engine.ContainerInspect, left []core.Message) {
	for _, c := range cs {
		if why := actionUnavailable(action, c); why != nil {
			left = append(left, msg("act.notTouched", "name", containerName(c), "reason", why.Text))
			continue
		}
		on = append(on, c)
	}
	return on, left
}

func (s *session) PrepareAction(ctx context.Context, ref core.Ref, action string, p core.ActionParams) (core.ActionPlan, error) {
	d, err := actionOf(ref.Kind, action)
	if err != nil {
		return core.ActionPlan{}, err
	}
	if err := d.CheckParams(p, false); err != nil {
		return core.ActionPlan{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	cs, err := s.actionObjects(ctx, ref)
	if err != nil {
		return core.ActionPlan{}, err
	}
	plan := core.ActionPlan{
		Where:  core.LiveTarget{Provider: ProviderID, Target: s.target, TargetTitle: s.title, Endpoint: s.cl.Endpoint(), ConfigHash: s.hash, Ref: ref},
		Action: d, Params: p, Destructive: d.Destructive, Rights: rightsUnknown,
		Warnings: []core.Message{msg("act.noPrecondition")},
		Expect:   actionExpect(action, cs),
	}
	if ref.Kind == KindContainers {
		c := cs[0]
		plan.Where.Ref = containerRef(c)
		plan.Where.Ref.Target = ref.Target
		if why := actionUnavailable(action, c); why != nil {
			plan.Unavailable = why
			return plan, nil
		}
		plan.Effects = actionEffects(action, c)
		plan.Destructive = plan.Destructive || removesOnStop(action, c)
		return plan, nil
	}
	project, service, _ := strings.Cut(ref.Name, "/")
	plan.Where.Ref = serviceRef(project, service)
	plan.Where.Ref.Target = ref.Target
	plan.Warnings = append(plan.Warnings, msg("act.newMembers"))
	on, left := touched(action, cs)
	if len(on) == 0 {
		m := msg("act.serviceNone."+action, "service", service)
		plan.Unavailable = &m
		return plan, nil
	}
	for _, c := range on {
		plan.Effects = append(plan.Effects, actionEffects(action, c)...)
		plan.Destructive = plan.Destructive || removesOnStop(action, c)
	}
	plan.Effects = append(plan.Effects, left...)
	return plan, nil
}

func (s *session) RunAction(ctx context.Context, run provider.ActionRun) (core.ActionResult, error) {
	d, err := actionOf(run.Ref.Kind, run.Action)
	if err != nil {
		return core.ActionResult{}, err
	}
	if err := d.CheckParams(run.Params, true); err != nil {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	cs, err := s.actionObjects(ctx, run.Ref)
	if err != nil {
		return core.ActionResult{}, err
	}
	if actionExpect(run.Action, cs) != run.Expect {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, changedSince(run.Ref))
	}
	if run.Ref.Kind == KindContainers {
		c := cs[0]
		if why := actionUnavailable(run.Action, c); why != nil {
			return core.ActionResult{}, provider.Said(provider.ClassConflict, *why)
		}
		m, err := s.writeAction(ctx, run.Action, c)
		if err != nil {
			return core.ActionResult{}, err
		}
		return core.ActionResult{Message: m, Outcome: core.OutcomeDone}, nil
	}
	on, _ := touched(run.Action, cs)
	if len(on) == 0 { // the fingerprint held: the plan said so too
		_, service, _ := strings.Cut(run.Ref.Name, "/")
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("act.serviceNone."+run.Action, "service", service))
	}
	res := core.ActionResult{Outcome: core.OutcomeDone, Parts: make([]core.ActionPart, 0, len(on))}
	for _, c := range on {
		part := core.ActionPart{ID: c.ID, Title: containerName(c)}
		if res.Outcome != core.OutcomeDone {
			part.Outcome = core.OutcomeSkipped
			res.Parts = append(res.Parts, part)
			continue
		}
		_, err := s.writeAction(ctx, run.Action, c)
		switch {
		case err == nil:
			part.Outcome = core.OutcomeDone
		case isUnknown(err):
			part.Outcome, part.Why = core.OutcomeUnknown, whyOf(err)
			res.Outcome = core.OutcomeUnknown
		default:
			part.Outcome, part.Why = core.OutcomeRefused, whyOf(err)
			res.Outcome = core.OutcomeRefused
		}
		res.Parts = append(res.Parts, part)
	}
	res.Message = core.Message{Text: partsSummary(run.Action, res.Parts)}
	return res, nil
}

// whyOf is a failed write's reason for its part: the provider's sentence,
// else its text (a server's words).
func whyOf(err error) *core.Message {
	var pe *provider.Error
	switch {
	case errors.As(err, &pe) && pe.Why != nil:
		why := *pe.Why
		return &why
	case errors.As(err, &pe):
		return &core.Message{Text: pe.Message}
	}
	return &core.Message{Text: err.Error()}
}

func isUnknown(err error) bool {
	var pe *provider.Error
	return errors.As(err, &pe) && pe.Class == provider.ClassUnknown
}

// changedSince: ref ("container web-1", "service web") changed since the
// action was reviewed.
func changedSince(ref core.Ref) core.Message {
	if ref.Kind == KindServices {
		_, service, _ := strings.Cut(ref.Name, "/")
		return msg("act.changedService", "name", service)
	}
	return msg("act.changedContainer", "name", shown(ref))
}

// actionPast: "stopped", … for messages.
var actionPast = map[string]string{"start": "started", "stop": "stopped", "restart": "restarted", "delete": "removed"}

// partsSummary: "2 of 3 containers stopped; 1 outcome unknown; 1 not run".
func partsSummary(action string, parts []core.ActionPart) string {
	n := map[core.ActionOutcome]int{}
	for _, p := range parts {
		n[p.Outcome]++
	}
	out := fmt.Sprintf("%d of %d containers %s", n[core.OutcomeDone], len(parts), actionPast[action])
	if k := n[core.OutcomeRefused]; k > 0 {
		out += fmt.Sprintf("; %d refused", k)
	}
	if k := n[core.OutcomeUnknown]; k > 0 {
		out += fmt.Sprintf("; %d outcome unknown", k)
	}
	if k := n[core.OutcomeSkipped]; k > 0 {
		out += fmt.Sprintf("; %d not run", k)
	}
	return out
}

// writeAction sends the action for c (once). Its errors: gone (removed
// meanwhile), invalid (the daemon refused it — 409, with its words),
// unknown (sent, no usable answer), the transport's classes.
func (s *session) writeAction(ctx context.Context, action string, c *engine.ContainerInspect) (core.Message, error) {
	name := containerName(c)
	var (
		already bool
		err     error
	)
	switch action {
	case actStart.ID:
		already, err = s.cl.StartContainer(ctx, c.ID)
	case actStop.ID:
		already, err = s.cl.StopContainer(ctx, c.ID, stopTimeout(c))
	case actRestart.ID:
		err = s.cl.RestartContainer(ctx, c.ID, stopTimeout(c))
	case actDelete.ID:
		err = s.cl.RemoveContainer(ctx, c.ID)
	}
	switch {
	case engine.IsNotFound(err):
		return core.Message{}, provider.Said(provider.ClassGone, msg("act.removedMeanwhile", "name", name))
	case engine.ClassOf(err) == provider.ClassConflict:
		var pe *provider.Error
		_ = errors.As(providerError(err), &pe)
		return core.Message{}, provider.Said(provider.ClassInvalid, msg("act.engineRefused", "detail", pe.Message))
	case err != nil:
		return core.Message{}, providerError(err)
	case already && action == actStart.ID:
		return msg("done.alreadyRunning", "name", name), nil
	case already:
		return msg("done.alreadyStopped", "name", name), nil
	}
	return msg("done."+action, "name", name), nil
}
