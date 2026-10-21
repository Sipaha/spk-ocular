package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// NetworkPathSource is optional and admitted only through the application UI.
type NetworkPathSource interface {
	ResourceNetworkPath(context.Context, core.Ref) (core.NetworkPath, error)
}
