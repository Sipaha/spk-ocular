package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// FluxSource is a UI-only bounded projection of discovered Flux controllers.
type FluxSource interface {
	FluxCD(context.Context, core.ScopeSel) (core.FluxSnapshot, error)
}
