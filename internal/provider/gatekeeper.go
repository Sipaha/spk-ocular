package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// GatekeeperSource is an optional explicit UI-only finite source observation.
type GatekeeperSource interface {
	GatekeeperAudit(context.Context, core.Ref) (core.GatekeeperAudit, error)
}
