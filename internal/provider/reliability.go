package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// ReliabilitySource is a UI-only optional declaration inspector.
type ReliabilitySource interface {
	ResourceReliability(context.Context, core.Ref) (core.Reliability, error)
}
