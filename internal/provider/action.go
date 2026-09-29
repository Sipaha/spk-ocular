package provider

import (
	"context"

	"github.com/spk/spk-ocular/internal/core"
)

// Actioner changes objects (KindDescriptor.Actions): a plan is read first
// (nothing changes), the user confirms it, the run acts on the confirmed
// object only (its UID) and only while Expect still holds.
type Actioner interface {
	// PrepareAction reads what action would do for ref with p (a count may
	// still be missing). Where.ConfigHash is this session's.
	PrepareAction(ctx context.Context, ref core.Ref, action string, p core.ActionParams) (core.ActionPlan, error)
	// RunAction performs a confirmed plan. Errors: gone (the object was
	// deleted or replaced), conflict (it changed since the plan), unknown
	// (sent, outcome not known), forbidden, ...
	RunAction(ctx context.Context, run ActionRun) (core.ActionResult, error)
}

// ActionRun is a confirmed plan.
type ActionRun struct {
	Ref    core.Ref
	Action string
	Params core.ActionParams
	Expect string
}
