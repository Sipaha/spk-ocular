package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// NamespaceAssessmentSource provides an opt-in, UI-only namespace overview.
type NamespaceAssessmentSource interface {
	AssessNamespace(context.Context, core.NamespaceAssessmentRequest) (core.NamespaceAssessment, error)
}
