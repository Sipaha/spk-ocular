package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// GraphSource is optional and UI-only; wildcard agent grants do not enable it.
type GraphSource interface {
	Graph(context.Context, core.ScopeSel) (core.Graph, error)
}
