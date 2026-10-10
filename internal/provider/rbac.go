package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// RBACSource is UI-only; declaration inspection cannot expand agent grants.
type RBACSource interface {
	RBAC(context.Context, core.ScopeSel, *core.Ref) (core.RBACSnapshot, error)
	ResolveAccess(core.Ref, string, string) (core.AccessAttributes, error)
	CheckAccess(context.Context, core.AccessAttributes) (core.AccessReview, error)
}
