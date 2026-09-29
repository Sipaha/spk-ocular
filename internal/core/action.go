package core

import (
	"errors"
	"fmt"
)

// ActionDescriptor is an action objects of a kind offer (k8s: restart,
// scale, delete; Docker Compose: restart, stop, start, rm).
type ActionDescriptor struct {
	ID string `json:"id"`
	// Title is the provider's label; the UI translates the IDs it knows.
	Title string `json:"title"`
	// Destructive: removes something; a plan can be destructive for its
	// parameters even when the action is not (scale to zero).
	Destructive bool         `json:"destructive,omitempty"`
	Param       *ActionParam `json:"param,omitempty"`
}

// ActionParam is the one parameter an action takes.
type ActionParam struct {
	// Kind: "count" (an integer in Min..Max).
	Kind string `json:"kind"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
}

const ParamCount = "count"

// ActionParams are the values of an action's parameter.
type ActionParams struct {
	Count *int `json:"count,omitempty"`
}

// CheckParams: p carries exactly what d takes. A plan may be prepared
// before the value is chosen (final false: a missing count is fine); a run
// needs it (final true).
func (d ActionDescriptor) CheckParams(p ActionParams, final bool) error {
	if d.Param == nil {
		if p.Count != nil {
			return fmt.Errorf("%s takes no parameters", d.ID)
		}
		return nil
	}
	switch {
	case p.Count == nil && final:
		return fmt.Errorf("%s needs a count", d.ID)
	case p.Count == nil:
		return nil
	case *p.Count < d.Param.Min || *p.Count > d.Param.Max:
		return fmt.Errorf("the count must be %d..%d", d.Param.Min, d.Param.Max)
	}
	return nil
}

// Message is a provider's sentence for the UI: Key (namespaced by the
// provider: "kubernetes.scale.down") and Params let the UI say it in its
// language; Text is the provider's English, shown for keys the UI does not
// know.
type Message struct {
	Key    string            `json:"key,omitempty"`
	Params map[string]string `json:"params,omitempty"`
	Text   string            `json:"text"`
}

// Texts are the English texts of ms (logs, tests).
func Texts(ms []Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

// RightsState: what a permission check said.
type RightsState string

const (
	RightsAllowed RightsState = "allowed"
	RightsDenied  RightsState = "denied"
	// RightsUnknown: the check could not be made; the action may still run.
	RightsUnknown RightsState = "unknown"
)

type Rights struct {
	State  RightsState `json:"state"`
	Reason string      `json:"reason,omitempty"`
}

// ActionPlan is what an action would do, read without changing anything:
// the confirmation shows it, the run carries Expect back.
type ActionPlan struct {
	// Where: the target (title, endpoint, revision) and the object, its
	// UID as read now.
	Where  LiveTarget       `json:"where"`
	Action ActionDescriptor `json:"action"`
	// Params the plan was made for.
	Params ActionParams `json:"params"`
	// Current: the count now (scale).
	Current *int `json:"current,omitempty"`
	// Destructive for these parameters.
	Destructive bool      `json:"destructive,omitempty"`
	Effects     []Message `json:"effects,omitempty"`
	// Warnings: what may interfere or could not be checked.
	Warnings []Message `json:"warnings,omitempty"`
	Rights   Rights    `json:"rights"`
	// Unavailable: why the action cannot run in the object's state.
	Unavailable *Message `json:"unavailable,omitempty"`
	// Expect is the provider's opaque fingerprint of the action, its
	// parameters and the state the effects depend on; a run whose object
	// no longer matches it is refused (conflict).
	Expect string `json:"expect"`
}

// ActionResult: the change was requested (its progress shows in views).
type ActionResult struct {
	Message string `json:"message"`
}

// ErrNoAction: the kind has no such action.
var ErrNoAction = errors.New("no such action")

// FindAction returns kind's action id.
func FindAction(kinds []KindDescriptor, kind, id string) (ActionDescriptor, error) {
	for _, k := range kinds {
		if k.ID != kind {
			continue
		}
		for _, a := range k.Actions {
			if a.ID == id {
				return a, nil
			}
		}
		return ActionDescriptor{}, fmt.Errorf("%w: %s has no action %q", ErrNoAction, k.Title, id)
	}
	return ActionDescriptor{}, fmt.Errorf("%w: unknown kind %q", ErrNoAction, kind)
}
