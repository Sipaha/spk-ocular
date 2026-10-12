package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/configuration"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
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
		name, args := configurationFileManager(runtime.GOOS, path)
		if name == "" {
			return configuration.ConfigState{}, coded(CodeBadRequest, errors.New("file manager is unavailable on this platform"))
		}
		launchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Fixed executable and separate arguments: never execute a kubeconfig or a shell.
		if err := exec.CommandContext(launchCtx, name, args...).Run(); err != nil {
			return configuration.ConfigState{}, coded(CodeBadRequest, errors.New("could not open the file manager"))
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

func configurationFileManager(platform, path string) (string, []string) {
	switch platform {
	case "linux":
		return "xdg-open", []string{filepath.Dir(path)}
	case "darwin":
		return "open", []string{"-R", path}
	case "windows":
		return "explorer.exe", []string{"/select," + path}
	default:
		return "", nil
	}
}
