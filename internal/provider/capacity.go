package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// NodeCapacitySource is an explicit UI-only, single-namespace declaration reader.
type NodeCapacitySource interface {
	NodeCapacity(context.Context, core.NodeCapacityRequest) (core.NodeCapacity, error)
}
