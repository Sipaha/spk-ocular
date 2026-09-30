package core

import (
	"errors"
	"fmt"
	"strings"
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
	// Text: a text value the action takes next to its Param (a debug
	// container's image).
	Text *ActionText `json:"text,omitempty"`
	// NoAgents: exec-level access (a debug container) — never granted to
	// agents, whatever their grants say.
	NoAgents bool `json:"noAgents,omitempty"`
	// Single: only one object at a time (a node's drain: several at once
	// evict everything together) — never in a bulk action.
	Single bool `json:"single,omitempty"`
}

// ActionText describes an action's text value.
type ActionText struct {
	Title   Message `json:"title"`
	Default string  `json:"default,omitempty"`
	// Max: the longest value, in bytes.
	Max int `json:"max"`
}

// ActionParam is the one parameter an action takes.
type ActionParam struct {
	// Kind: "count" (an integer in Min..Max) or "choice" (one of the
	// plan's Choices: they are live, the provider checks the value).
	Kind string `json:"kind"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	// Title heads a choice in the provider's words ("Revision"); nil — the
	// UI's generic word.
	Title *Message `json:"title,omitempty"`
}

const (
	ParamCount  = "count"
	ParamChoice = "choice"
)

// ActionParams are the values of an action's parameter.
type ActionParams struct {
	Count  *int    `json:"count,omitempty"`
	Choice *string `json:"choice,omitempty"`
	Text   *string `json:"text,omitempty"`
}

// CheckParams: p carries exactly what d takes. A plan may be prepared
// before the value is chosen (final false: a missing value is fine); a run
// needs it (final true).
func (d ActionDescriptor) CheckParams(p ActionParams, final bool) error {
	switch {
	case d.Text == nil && p.Text != nil:
		return fmt.Errorf("%s takes no text", d.ID)
	case d.Text != nil && p.Text == nil && final:
		return fmt.Errorf("%s needs %s", d.ID, strings.ToLower(d.Text.Title.Text))
	case p.Text != nil && (*p.Text == "" || len(*p.Text) > d.Text.Max):
		return fmt.Errorf("%s must be 1..%d characters", d.Text.Title.Text, d.Text.Max)
	}
	if d.Param == nil {
		if p.Count != nil || p.Choice != nil {
			return fmt.Errorf("%s takes no parameters", d.ID)
		}
		return nil
	}
	if d.Param.Kind == ParamChoice {
		switch {
		case p.Count != nil:
			return fmt.Errorf("%s takes a choice, not a count", d.ID)
		case p.Choice == nil && final:
			return fmt.Errorf("%s needs a choice", d.ID)
		case p.Choice != nil && *p.Choice == "":
			return fmt.Errorf("%s: the choice is empty", d.ID)
		}
		return nil
	}
	switch {
	case p.Choice != nil:
		return fmt.Errorf("%s takes a count, not a choice", d.ID)
	case p.Count == nil && final:
		return fmt.Errorf("%s needs a count", d.ID)
	case p.Count == nil:
		return nil
	case *p.Count < d.Param.Min || *p.Count > d.Param.Max:
		return fmt.Errorf("the count must be %d..%d", d.Param.Min, d.Param.Max)
	}
	return nil
}

// ActionChoice is one value a choice parameter may take (a revision to
// roll back to), as the provider offers it now.
type ActionChoice struct {
	// Value is what ActionParams.Choice carries.
	Value   string    `json:"value"`
	Title   Message   `json:"title"`
	Details []Message `json:"details,omitempty"`
	// At: when it came to be, unix ms; 0 — unknown.
	At int64 `json:"at,omitempty"`
	// Current: what the object has now.
	Current bool `json:"current,omitempty"`
	// Unavailable: why it cannot be chosen.
	Unavailable *Message `json:"unavailable,omitempty"`
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
	// Choices: the values a choice parameter may take now (with or
	// without one chosen).
	Choices []ActionChoice `json:"choices,omitempty"`
	// Changes: what changes in the object itself (lines of a difference) —
	// whole: the UI shows them in portions. Not objects: no grants apply.
	Changes []Message `json:"changes,omitempty"`
	// Lists: the objects the plan concerns, by group — whole: the UI shows
	// them in portions, never a cut list.
	Lists []ActionList `json:"lists,omitempty"`
	// Expect is the provider's opaque fingerprint of the action, its
	// parameters and the state the effects depend on; a run whose object
	// no longer matches it is refused (conflict).
	Expect string `json:"expect"`
}

// ActionList is a group of objects a plan concerns (to be evicted, left
// alone, …); Title in the provider's words.
type ActionList struct {
	Title Message `json:"title"`
	// Destructive: what happens to them may lose something.
	Destructive bool `json:"destructive,omitempty"`
	// Collapsed: shown on request (what the action leaves alone).
	Collapsed bool         `json:"collapsed,omitempty"`
	Items     []ActionItem `json:"items"`
}

// ActionItem is one object of a list, with a note about it. Ref names the
// object when the provider can (an agent's run is refused when an item lies
// outside its grants; an item without Ref counts as outside).
type ActionItem struct {
	Name string   `json:"name"`
	Note *Message `json:"note,omitempty"`
	Ref  *Ref     `json:"ref,omitempty"`
}

// ActionOutcome: what became of a run or of one of its parts.
type ActionOutcome string

const (
	OutcomeDone ActionOutcome = "done"
	// OutcomeRefused: the target refused it (nothing changed by it).
	OutcomeRefused ActionOutcome = "refused"
	// OutcomeUnknown: sent, the outcome is not known.
	OutcomeUnknown ActionOutcome = "unknown"
	// OutcomeSkipped: not run (an earlier part was refused or unknown, the
	// action leaves it alone, the time ran out); of a run: not everything
	// was done, nothing refused or unknown.
	OutcomeSkipped ActionOutcome = "skipped"
)

// ActionResult: the change was requested (its progress shows in views).
// A run of several writes (a service's containers) reports each in Parts
// and returns its result, not an error, once any write was sent: Outcome
// is done only when every part is, else the worst part's (unknown, then
// refused, then skipped — PartsOutcome).
// Message says what was requested (the UI shows it for a run without Parts).
type ActionResult struct {
	Message Message       `json:"message"`
	Outcome ActionOutcome `json:"outcome"`
	Parts   []ActionPart  `json:"parts,omitempty"`
	// Terminal: open a terminal there once done (a debug container's).
	Terminal *TerminalOpen `json:"terminal,omitempty"`
}

// ActionPart is one write of a run: its object (ID: the full id, Title:
// its name), its outcome and why it is not done — always set for refused
// and unknown parts, where possible for skipped ones.
type ActionPart struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Outcome ActionOutcome `json:"outcome"`
	Why     *Message      `json:"why,omitempty"`
}

// PartsOutcome is a run's outcome from its parts: unknown outranks refused,
// refused outranks skipped; done only when every part is (none: done).
func PartsOutcome(parts []ActionPart) ActionOutcome {
	rank := map[ActionOutcome]int{OutcomeDone: 0, OutcomeSkipped: 1, OutcomeRefused: 2, OutcomeUnknown: 3}
	out := OutcomeDone
	for _, p := range parts {
		if rank[p.Outcome] > rank[out] {
			out = p.Outcome
		}
	}
	return out
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

// TerminalOpen is a terminal an action's result asks the UI to open.
type TerminalOpen struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
	Channel  string `json:"channel,omitempty"`
	// Attach: to the container's own process (its stdin), not a new one.
	Attach bool `json:"attach,omitempty"`
}
