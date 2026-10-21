package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// DiagnosisSource is an optional explicit UI-only selected-object read.
type DiagnosisSource interface {
	DiagnoseResource(context.Context, core.Ref) (core.Diagnosis, error)
}
