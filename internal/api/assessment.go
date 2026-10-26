package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"k8s.io/apimachinery/pkg/util/validation"
	"time"
)

func (s *Service) AssessNamespace(ctx context.Context, req core.NamespaceAssessmentRequest) (core.NamespaceAssessment, error) {
	if !fromUI(ctx) {
		return core.NamespaceAssessment{}, coded("forbidden", errors.New("namespace checks are available only to the application UI"))
	}
	if req.Namespace == "" || len(validation.IsDNS1123Label(req.Namespace)) != 0 || (req.Check != "configuration" && req.Check != "vpa" && req.Check != "diagnosis") {
		return core.NamespaceAssessment{}, coded(CodeBadRequest, errors.New("select one namespace and a supported check"))
	}
	// The deadline includes session resolution and the provider's shared gate.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.NamespaceAssessment{}, err
	}
	source, ok := e.sess.(provider.NamespaceAssessmentSource)
	if !ok {
		return core.NamespaceAssessment{}, coded(CodeUnsupported, errors.New("connection does not support namespace checks"))
	}
	out, err := source.AssessNamespace(ctx, req)
	if err != nil {
		return core.NamespaceAssessment{}, fromProvider(err)
	}
	current, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.NamespaceAssessment{}, err
	}
	if current != e {
		return core.NamespaceAssessment{}, coded(CodeGone, errors.New("connection changed"))
	}
	if ctx.Err() != nil {
		return core.NamespaceAssessment{}, ctx.Err()
	}
	return out, nil
}

func assessmentKind() core.KindDescriptor {
	return core.KindDescriptor{ID: "ocular.assessment", Title: "Namespace checks", Group: "Cluster", Workspace: "assessment", Scoped: true, Columns: []core.Column{}}
}
