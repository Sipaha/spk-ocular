package agentapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/store"
)

// runTimeout bounds a write (a var for tests).
var runTimeout = 2 * time.Minute

// plan is a plan an agent was shown, kept for its run: what the service
// needs to run exactly it, and what the grants are judged by.
type plan struct {
	agent       string
	method      string // RunAction | RunEdit
	provider    string
	target      string
	title       string
	ref         core.Ref
	kind        core.KindDescriptor
	verb        string
	destructive bool
	lists       []core.ActionList
	action      *api.ActionRunRequest
	actionPlan  *core.ActionPlan
	edit        *api.EditRunRequest
	editPlan    *core.EditPlan
}

// editSource is an object's text an agent got for editing.
type editSource struct {
	ref      core.Ref
	base     string
	original string
}

func (s *Server) writeMethods() {
	register(s, "PrepareAction", "action:<id>", false, "What an action would do to an object (nothing changes): its effects, warnings, and whether it is destructive; a plan that can run has a planId for RunAction (valid 10 minutes).", s.prepareAction)
	register(s, "RunAction", "action:<id>", true, "Runs a prepared plan (its planId, once). A destructive plan waits for the user's confirmation unless its grant has noConfirm: then state is awaiting_confirmation and GetRun tells the outcome.", s.runAction)
	register(s, "GetEditSource", agentgrant.VerbEdit, false, "An object's YAML to edit (what cannot change is left out), with a sourceId for PrepareEdit.", s.getEditSource)
	register(s, "PrepareEdit", agentgrant.VerbEdit, false, "What an edit of that YAML would do (nothing changes): the object before and after (a server dry run when possible), collisions, whether it is destructive; a plan that can be written has a planId for RunEdit.", s.prepareEdit)
	register(s, "RunEdit", agentgrant.VerbEdit, true, "Writes a prepared edit (its planId, once); a destructive one waits for the user's confirmation like RunAction.", s.runEdit)
	register(s, "GetRun", "none", false, "The outcome of a run that waited for the user's confirmation: waits up to 25 s for a change, then answers its state (awaiting_confirmation, running, done, failed, rejected, expired; gone: unknown here).", s.getRun)
}

type PrepareActionRequest struct {
	Ref    core.Ref          `json:"ref" jsonschema:"required" jsonschema_description:"The object as ListObjects gave it."`
	Action string            `json:"action" jsonschema:"required" jsonschema_description:"An action id from ListKinds (restart, scale, delete, ...)."`
	Params core.ActionParams `json:"params,omitzero" jsonschema_description:"count for scale."`
}

// PlanView is a plan as an agent reads it (the provider's English).
type PlanView struct {
	Where       core.Ref          `json:"where"`
	TargetTitle string            `json:"targetTitle"`
	Action      string            `json:"action,omitempty"`
	Params      core.ActionParams `json:"params,omitzero"`
	Current     *int              `json:"current,omitempty"`
	Destructive bool              `json:"destructive"`
	Effects     []string          `json:"effects,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Rights      core.Rights       `json:"rights"`
	Unavailable string            `json:"unavailable,omitempty" jsonschema_description:"Why it cannot run now (no planId then)."`
	Lists       []ListView        `json:"lists,omitempty"`
	// Edit plans.
	Before     string   `json:"before,omitempty"`
	After      string   `json:"after,omitempty"`
	Checked    bool     `json:"checked,omitempty" jsonschema_description:"After is the server's dry run (else computed locally)."`
	Changed    bool     `json:"changed,omitempty"`
	Rebased    bool     `json:"rebased,omitempty"`
	Collisions []string `json:"collisions,omitempty"`
}

type ListView struct {
	Title       string     `json:"title"`
	Destructive bool       `json:"destructive,omitempty"`
	Items       []ItemView `json:"items"`
}

type ItemView struct {
	Name string    `json:"name"`
	Note string    `json:"note,omitempty"`
	Ref  *core.Ref `json:"ref,omitempty"`
}

type PrepareView struct {
	PlanID string   `json:"planId,omitempty"`
	Plan   PlanView `json:"plan"`
	// Confirmation: none, or user (RunAction/RunEdit waits for the user).
	Confirmation string `json:"confirmation"`
}

func texts(ms []core.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

func actionView(p core.ActionPlan) PlanView {
	v := PlanView{Where: p.Where.Ref, TargetTitle: p.Where.TargetTitle, Action: p.Action.ID, Params: p.Params, Current: p.Current, Destructive: p.Destructive,
		Effects: texts(p.Effects), Warnings: texts(p.Warnings), Rights: p.Rights}
	if p.Unavailable != nil {
		v.Unavailable = p.Unavailable.Text
	}
	for _, l := range p.Lists {
		lv := ListView{Title: l.Title.Text, Destructive: l.Destructive, Items: []ItemView{}}
		for _, it := range l.Items {
			iv := ItemView{Name: it.Name, Ref: it.Ref}
			if it.Note != nil {
				iv.Note = it.Note.Text
			}
			lv.Items = append(lv.Items, iv)
		}
		v.Lists = append(v.Lists, lv)
	}
	return v
}

// listsOutside names the first item of lists outside the scopes verb is
// granted in (an item without a ref counts as outside).
func listsOutside(gs []agentgrant.Grant, verb string, lists []core.ActionList) string {
	for _, l := range lists {
		for _, it := range l.Items {
			if it.Ref == nil || it.Ref.Scope == "" {
				return it.Name
			}
			ok := false
			for _, g := range gs {
				if g.Verb == verb && (g.Scope.Mode == agentgrant.ScopeAll || g.Scope.Mode == agentgrant.ScopeOne && g.Scope.Name == it.Ref.Scope) {
					ok = true
				}
			}
			if !ok {
				return it.Name
			}
		}
	}
	return ""
}

// judge checks a plan against the grants: the verb for its kind and
// scope, destructive only by name, lists within the scopes; it returns
// whether the user must confirm.
func (s *Server) judge(c caller, x *session, method string, p *plan) (bool, error) {
	if !p.kind.Scoped {
		err := forbidden("%s objects are outside namespaces: agents do not change them", p.kind.ID)
		s.refused(c, method, p.provider, p.target, p.ref.Scope, objectOf(p.ref), err)
		return false, err
	}
	d, err := s.check(c, x, method, p.kind, p.ref, p.verb, p.destructive)
	if err != nil {
		return false, err
	}
	if name := listsOutside(x.grants.Grants, p.verb, p.lists); name != "" {
		err := forbidden("the plan concerns %s, outside the namespaces %s is granted in", name, p.verb)
		s.refused(c, method, p.provider, p.target, p.ref.Scope, objectOf(p.ref), err)
		return false, err
	}
	return p.destructive && !d.NoConfirm, nil
}

func (s *Server) prepareAction(ctx context.Context, c caller, req *PrepareActionRequest) (*PrepareView, error) {
	ref := req.Ref
	x, err := s.open(ctx, c, "PrepareAction", ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(ref.Kind)
	if err != nil {
		return nil, err
	}
	d, err := core.FindAction([]core.KindDescriptor{k}, k.ID, req.Action)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	p := &plan{agent: c.agent, method: "RunAction", provider: ref.Provider, target: ref.Target, title: x.call.Title(), ref: ref, kind: k, verb: agentgrant.ActionVerb(d.ID), destructive: d.Destructive}
	if _, err := s.judge(c, x, "PrepareAction", p); err != nil {
		return nil, err
	}
	ap, err := s.o.Service.PrepareAction(x.call.Context(), api.ActionRequest{Ref: ref, Action: req.Action, Params: req.Params})
	if err != nil {
		return nil, err
	}
	p.ref, p.destructive, p.lists = ap.Where.Ref, ap.Destructive, ap.Lists
	confirm, err := s.judge(c, x, "PrepareAction", p)
	if err != nil {
		return nil, err
	}
	out := &PrepareView{Plan: actionView(ap), Confirmation: "none"}
	if confirm {
		out.Confirmation = "user"
	}
	if ap.Unavailable == nil {
		p.action = &api.ActionRunRequest{Ref: ap.Where.Ref, Action: ap.Action.ID, Params: ap.Params, Expect: ap.Expect, ConfigRev: ap.Where.ConfigRev}
		p.actionPlan = &ap
		out.PlanID = s.plans.put("plan-", p)
	}
	s.read(c, "PrepareAction", ref.Provider, ref.Target, ref.Scope, objectOf(ref))
	return out, nil
}

type RunRequest struct {
	PlanID string `json:"planId" jsonschema:"required"`
}

// RunView is a run's state: done, failed (error), awaiting_confirmation
// (runId for GetRun), running, rejected, expired, gone.
type RunView struct {
	State  string          `json:"state"`
	RunID  string          `json:"runId,omitempty"`
	Result *ResultView     `json:"result,omitempty"`
	Error  *api.CodedError `json:"error,omitempty"`
}

type ResultView struct {
	Message string `json:"message"`
	// Outcome: done, refused, unknown (sent; not known whether it
	// happened), skipped (not everything was done).
	Outcome string     `json:"outcome"`
	Parts   []PartView `json:"parts,omitempty"`
	// Version: an edit's new version.
	Version string `json:"version,omitempty"`
}

type PartView struct {
	Title   string `json:"title"`
	Outcome string `json:"outcome"`
	Why     string `json:"why,omitempty"`
}

func (s *Server) runAction(ctx context.Context, c caller, req *RunRequest) (*RunView, error) {
	return s.runPlan(ctx, c, "RunAction", req.PlanID)
}

func (s *Server) runEdit(ctx context.Context, c caller, req *RunRequest) (*RunView, error) {
	return s.runPlan(ctx, c, "RunEdit", req.PlanID)
}

// runPlan judges a plan again (the grants may have changed since it was
// prepared) and runs it, or leaves it to the user's confirmation.
func (s *Server) runPlan(ctx context.Context, c caller, method, id string) (*RunView, error) {
	p, ok := s.plans.take(id)
	if !ok || p.method != method {
		return nil, notFound("plan", id)
	}
	x, err := s.open(ctx, c, method, p.provider, p.target)
	if err != nil {
		return nil, err
	}
	confirm, err := s.judge(c, x, method, p)
	x.done()
	if err != nil {
		return nil, err
	}
	p.agent = c.agent
	if confirm {
		pr, err := s.pend.add(p)
		if err != nil {
			return nil, err
		}
		s.audit(store.AuditEntry{Agent: c.agent, Method: method, Provider: p.provider, Target: p.target, Scope: p.ref.Scope, Object: objectOf(p.ref), Verb: p.verb, Destructive: true, ExpectHash: expectHash(p), Phase: store.AuditOutcome, Outcome: "awaiting_confirmation"})
		return &RunView{State: stateAwaiting, RunID: pr.id}, nil
	}
	v := s.execute(p)
	if v.Error != nil {
		return nil, v.Error
	}
	return &v, nil
}

// execute writes p on the server's context (not the agent's request: a
// hang-up does not cut a write), bounded by runTimeout: the intent before,
// the outcome after. The grants and the identity are checked again at the
// moment of the write.
func (s *Server) execute(p *plan) RunView {
	if !s.track() {
		return failed(&api.CodedError{Code: api.CodeGone, Detail: "Ocular is closing"})
	}
	defer s.runs.Done()
	ctx, cancel := context.WithTimeout(s.runCtx, runTimeout)
	defer cancel()
	c := caller{agent: p.agent}
	x, err := s.open(ctx, c, p.method, p.provider, p.target)
	if err != nil {
		return failed(err)
	}
	defer x.done()
	if _, err := s.judge(c, x, p.method, p); err != nil {
		return failed(err)
	}
	e := store.AuditEntry{Agent: p.agent, Method: p.method, Provider: p.provider, Target: p.target, Scope: p.ref.Scope, Object: objectOf(p.ref), Verb: p.verb, Destructive: p.destructive, ExpectHash: expectHash(p), Phase: store.AuditIntent}
	s.audit(e)
	e.Phase = store.AuditOutcome
	var v RunView
	switch {
	case p.action != nil:
		res, err := s.o.Service.RunAction(x.call.Context(), *p.action)
		if err != nil {
			v = failed(err)
			break
		}
		rv := &ResultView{Message: res.Message.Text, Outcome: string(res.Outcome)}
		for _, part := range res.Parts {
			pv := PartView{Title: part.Title, Outcome: string(part.Outcome)}
			if part.Why != nil {
				pv.Why = part.Why.Text
			}
			rv.Parts = append(rv.Parts, pv)
		}
		v = RunView{State: stateDone, Result: rv}
	case p.edit != nil:
		res, err := s.o.Service.RunEdit(x.call.Context(), *p.edit)
		if err != nil {
			v = failed(err)
			break
		}
		v = RunView{State: stateDone, Result: &ResultView{Message: res.Message, Outcome: string(core.OutcomeDone), Version: res.Version}}
	}
	switch {
	case v.Result != nil:
		e.Outcome, e.Detail = v.Result.Outcome, v.Result.Message
	case s.runCtx.Err() != nil: // Ocular is closing: sent or not, unknown
		e.Outcome, e.Detail = string(core.OutcomeUnknown), "Ocular closed during the write"
	default:
		e.Outcome, e.Detail = v.Error.Code, v.Error.Detail
	}
	s.audit(e)
	return v
}

func failed(err error) RunView {
	return RunView{State: stateFailed, Error: errCoded(err)}
}

// expectHash stands for the plan in the journal (never the plan itself).
func expectHash(p *plan) string {
	var v string
	switch {
	case p.action != nil:
		v = p.action.Expect
	case p.edit != nil:
		v = p.edit.Token
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:6])
}

type GetEditSourceRequest struct {
	Ref core.Ref `json:"ref" jsonschema:"required"`
}

type EditSourceView struct {
	SourceID string   `json:"sourceId" jsonschema_description:"For PrepareEdit (valid 10 minutes)."`
	Ref      core.Ref `json:"ref"`
	Text     string   `json:"text"`
	Version  string   `json:"version,omitempty"`
}

func (s *Server) getEditSource(ctx context.Context, c caller, req *GetEditSourceRequest) (*EditSourceView, error) {
	ref := req.Ref
	x, err := s.open(ctx, c, "GetEditSource", ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(ref.Kind)
	if err != nil {
		return nil, err
	}
	if !k.Editable {
		return nil, &api.CodedError{Code: api.CodeUnsupported, Detail: fmt.Sprintf("%s cannot be edited", k.ID)}
	}
	p := &plan{provider: ref.Provider, target: ref.Target, ref: ref, kind: k, verb: agentgrant.VerbEdit}
	if _, err := s.judge(c, x, "GetEditSource", p); err != nil {
		return nil, err
	}
	doc, err := s.o.Service.GetEditSource(x.call.Context(), ref)
	if err != nil {
		return nil, err
	}
	id := s.sources.put("src-", &editSource{ref: doc.Ref, base: doc.Base, original: doc.Text})
	s.read(c, "GetEditSource", ref.Provider, ref.Target, ref.Scope, objectOf(ref))
	return &EditSourceView{SourceID: id, Ref: doc.Ref, Text: doc.Text, Version: doc.Version}, nil
}

type PrepareEditRequest struct {
	SourceID string `json:"sourceId" jsonschema:"required" jsonschema_description:"From GetEditSource."`
	Edited   string `json:"edited" jsonschema:"required" jsonschema_description:"The whole edited text."`
}

func (s *Server) prepareEdit(ctx context.Context, c caller, req *PrepareEditRequest) (*PrepareView, error) {
	src, ok := s.sources.get(req.SourceID)
	if !ok {
		return nil, notFound("edit source", req.SourceID)
	}
	ref := src.ref
	x, err := s.open(ctx, c, "PrepareEdit", ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(ref.Kind)
	if err != nil {
		return nil, err
	}
	p := &plan{agent: c.agent, method: "RunEdit", provider: ref.Provider, target: ref.Target, title: x.call.Title(), ref: ref, kind: k, verb: agentgrant.VerbEdit}
	if _, err := s.judge(c, x, "PrepareEdit", p); err != nil {
		return nil, err
	}
	ep, err := s.o.Service.PrepareEdit(x.call.Context(), api.EditPrepareRequest{Ref: ref, Base: src.base, Original: src.original, Edited: req.Edited})
	if err != nil {
		return nil, err
	}
	p.destructive = ep.Destructive
	confirm, err := s.judge(c, x, "PrepareEdit", p)
	if err != nil {
		return nil, err
	}
	v := PlanView{Where: ref, TargetTitle: ep.Where.TargetTitle, Destructive: ep.Destructive, Warnings: texts(ep.Warnings), Rights: ep.Rights,
		Before: ep.Before, After: ep.After, Checked: ep.Checked, Changed: ep.Changed, Rebased: ep.Rebased, Collisions: ep.Collisions}
	if ep.Unavailable != nil {
		v.Unavailable = ep.Unavailable.Text
	}
	out := &PrepareView{Plan: v, Confirmation: "none"}
	if confirm {
		out.Confirmation = "user"
	}
	if ep.Token != "" {
		p.edit = &api.EditRunRequest{Ref: ref, Base: src.base, Original: src.original, Edited: req.Edited, Token: ep.Token}
		shown := ep
		shown.Token = ""
		p.editPlan = &shown
		out.PlanID = s.plans.put("plan-", p)
	} else if !ep.Changed && v.Unavailable == "" {
		v.Unavailable = "the edit changes nothing"
		out.Plan = v
	}
	s.read(c, "PrepareEdit", ref.Provider, ref.Target, ref.Scope, objectOf(ref))
	return out, nil
}
