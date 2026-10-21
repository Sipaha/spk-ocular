package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// ArgoSource is a UI-only finite projection; it does not admit Git/Argo secrets.
type ArgoSource interface {
	ArgoCD(context.Context, core.ScopeSel) (core.ArgoSnapshot, error)
}

// ArgoSettingsSource is an explicitly requested whitelist projection from
// label-selected settings Secrets. It never returns Secret payloads.
type ArgoSettingsSource interface {
	ArgoSettings(context.Context, core.ScopeSel, string) (core.ArgoSnapshot, error)
}
