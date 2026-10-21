package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// CertificateSource is an optional UI-only finite controller-status observation.
type CertificateSource interface {
	CertificateStatus(context.Context, core.Ref) (core.CertificateStatus, error)
}
