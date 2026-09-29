package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type ActionRequest struct {
	Ref    core.Ref          `json:"ref"`
	Action string            `json:"action"`
	Params core.ActionParams `json:"params"`
}

// ActionRunRequest is a confirmed plan: its object (with UID), action,
// parameters, Expect and the target revision it was shown with.
type ActionRunRequest struct {
	Ref       core.Ref          `json:"ref"`
	Action    string            `json:"action"`
	Params    core.ActionParams `json:"params"`
	Expect    string            `json:"expect"`
	ConfigRev string            `json:"configRev"`
}

// actioner is the target's session as an Actioner plus the action's
// descriptor; the plan's target comes from that same session.
func actioner(sess provider.Session, ref core.Ref, action string) (provider.Actioner, core.ActionDescriptor, error) {
	act, ok := sess.(provider.Actioner)
	if !ok {
		return nil, core.ActionDescriptor{}, coded(CodeUnsupported, errors.New("objects of this target cannot be changed"))
	}
	d, err := core.FindAction(sess.Kinds(), ref.Kind, action)
	if err != nil {
		return nil, core.ActionDescriptor{}, coded(CodeUnsupported, err)
	}
	return act, d, nil
}

func (s *Service) PrepareAction(ctx context.Context, req ActionRequest) (core.ActionPlan, error) {
	e, err := s.sessionFor(ctx, req.Ref.Provider, req.Ref.Target)
	if err != nil {
		return core.ActionPlan{}, err
	}
	act, d, err := actioner(e.sess, req.Ref, req.Action)
	if err != nil {
		return core.ActionPlan{}, err
	}
	if err := d.CheckParams(req.Params, false); err != nil {
		return core.ActionPlan{}, coded(CodeBadRequest, err)
	}
	plan, err := act.PrepareAction(ctx, req.Ref, req.Action, req.Params)
	if err != nil {
		return core.ActionPlan{}, fromProvider(err)
	}
	plan.Action = d
	plan.Where.Provider, plan.Where.Target = req.Ref.Provider, req.Ref.Target
	plan.Where.ConfigHash = e.hash // the revision of the session the plan was read in
	plan.Where = s.live(plan.Where)
	return plan, nil
}

// checkRun validates a run on its own: nothing from the UI is trusted.
func checkRun(req ActionRunRequest) error {
	r := req.Ref
	switch {
	case r.Provider == "" || r.Target == "" || r.Kind == "" || r.Name == "":
		return errors.New("the object is not fully named")
	case r.UID == "":
		return errors.New("the object's UID is missing: act only on a confirmed object")
	case req.Action == "":
		return errors.New("no action")
	case req.Expect == "":
		return errors.New("no plan (expect) to check against")
	case req.ConfigRev == "":
		return errors.New("no target revision to check against")
	}
	return nil
}

func (s *Service) RunAction(ctx context.Context, req ActionRunRequest) (core.ActionResult, error) {
	if err := checkRun(req); err != nil {
		return core.ActionResult{}, coded(CodeBadRequest, err)
	}
	sess, err := s.checkedSession(ctx, req.Ref.Provider, req.Ref.Target, req.ConfigRev)
	if err != nil {
		return core.ActionResult{}, err
	}
	act, d, err := actioner(sess, req.Ref, req.Action)
	if err != nil {
		return core.ActionResult{}, err
	}
	if err := d.CheckParams(req.Params, true); err != nil {
		return core.ActionResult{}, coded(CodeBadRequest, err)
	}
	// Runs on the session checked above, without holding sessMu: a change
	// of configuration from now on neither redirects nor fails it.
	res, err := act.RunAction(ctx, provider.ActionRun{Ref: req.Ref, Action: req.Action, Params: req.Params, Expect: req.Expect})
	if err != nil {
		return core.ActionResult{}, fromProvider(err)
	}
	return res, nil
}

// checkedSession is the target's current session if its configuration
// is still the one with revision rev (the one the user confirmed on).
func (s *Service) checkedSession(ctx context.Context, providerID, target, rev string) (provider.Session, error) {
	if _, err := s.session(ctx, providerID, target); err != nil {
		return nil, err
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	e := s.sessions[ownerKey(providerID, target)]
	if e == nil {
		return nil, coded(CodeGone, errors.New("the session closed meanwhile; try again"))
	}
	if s.configRev(e.hash) != rev {
		return nil, coded(CodeConflict, fmt.Errorf("the configuration of %s changed since the action was reviewed; review it again", target))
	}
	e.lastUsed = s.now()
	return e.sess, nil
}
