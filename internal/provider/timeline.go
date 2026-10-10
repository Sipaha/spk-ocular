package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// TimelineSource is an optional UI-only investigation workspace.
type TimelineSource interface {
	Timeline(context.Context, core.ScopeSel) (core.Timeline, error)
}
