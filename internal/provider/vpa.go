package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// VPASource is a finite UI-only reader of already installed VPA resources.
type VPASource interface {
	ResourceVPA(context.Context, core.Ref) (core.VPAInspection, error)
}
