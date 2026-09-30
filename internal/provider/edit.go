package provider

import (
	"context"

	"github.com/spk/spk-ocular/internal/core"
)

// Editor changes an object from its edited text (KindDescriptor.Editable):
// a plan first (nothing changes), the user confirms it, the write is that
// plan only. The API signs EditBase and EditGrant (the UI never makes
// them); the provider gets them back verified.
type Editor interface {
	// EditSource reads the object's text for the editor.
	EditSource(ctx context.Context, ref core.Ref) (core.EditDoc, EditBase, error)
	// PrepareEdit reads what the edit would do; the grant is nil when
	// nothing can be written.
	PrepareEdit(ctx context.Context, req EditRequest) (core.EditPlan, *EditGrant, error)
	// RunEdit writes a confirmed plan once (no retries).
	RunEdit(ctx context.Context, run EditRun) (core.EditResult, error)
}

// EditBase is the object and text an edit starts from.
type EditBase struct {
	// Route: where the object is read and written (provider-defined).
	Route      string `json:"route"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Version    string `json:"version"`
	// DocHash is the SHA-256 of the text shown; Format the version of how
	// objects are turned into text.
	DocHash string `json:"docHash"`
	Format  int    `json:"format"`
}

// EditGrant is what a plan allows to write: this patch, to this object,
// at this version.
type EditGrant struct {
	Route     string `json:"route"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
	// Version the plan was made against (the write's precondition).
	Version   string `json:"version"`
	PatchHash string `json:"patchHash"`
	// Mode: "checked" (a server dry run) or "local".
	Mode string `json:"mode"`
}

// EditRequest is an edit to plan: the original text (as EditSource gave
// it, checked against Base) and the edited one.
type EditRequest struct {
	Ref      core.Ref
	Base     EditBase
	Original string
	Edited   string
}

// EditRun is a confirmed plan.
type EditRun struct {
	EditRequest
	Grant EditGrant
}
