package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/configuration"
	"github.com/spk/spk-ocular/internal/filemanager"
)

func (s *Service) Configurations(ctx context.Context, req configuration.ConfigRequest) (configuration.ConfigState, error) {
	if !fromUI(ctx) {
		return configuration.ConfigState{}, coded("forbidden", errors.New("configuration management is available only in the UI"))
	}
	p, ok := s.reg.Get(configuration.KubernetesProviderID)
	if !ok {
		return configuration.ConfigState{Initialized: true}, nil
	}
	kube, ok := p.(configuration.Source)
	if !ok {
		return configuration.ConfigState{}, coded(CodeUnsupported, errors.New("connection does not support configuration management"))
	}
	if req.Command == "reveal" {
		path, err := kube.ConfigurationFile(req)
		if err != nil {
			return configuration.ConfigState{}, coded(CodeBadRequest, err)
		}
		if err := s.revealFile(ctx, path); err != nil {
			return configuration.ConfigState{}, coded(CodeBadRequest, err)
		}
		req = configuration.ConfigRequest{Command: "status"}
	}
	out, err := kube.Configurations(req)
	if err != nil {
		return out, coded(CodeBadRequest, err)
	}
	if req.Command == "remove" || req.Command == "update" || req.Command == "reset" {
		s.revalidateSessions(ctx, configuration.KubernetesProviderID)
	}
	return out, nil
}

// The per-service hook allows UI admission tests without invoking a real desktop.
func (s *Service) revealFile(ctx context.Context, path string) error {
	if s.revealLocalFile != nil {
		return s.revealLocalFile(ctx, path)
	}
	return filemanager.Reveal(ctx, path)
}
