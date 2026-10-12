package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// SecuritySource is an optional read-only, UI-only snapshot source.
type SecuritySource interface {
	Security(context.Context, core.ScopeSel) (core.SecuritySnapshot, error)
}
