package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func (s *Service) Configurations(ctx context.Context, req kubernetes.ConfigRequest) (kubernetes.ConfigState, error) {
	if !fromUI(ctx) {
		return kubernetes.ConfigState{}, coded("forbidden", errors.New("configuration management is available only in the UI"))
	}
	p, ok := s.reg.Get(kubernetes.ProviderID)
	if !ok {
		return kubernetes.ConfigState{Initialized: true}, nil
	}
	kube, ok := p.(*kubernetes.Provider)
	if !ok {
		return kubernetes.ConfigState{Initialized: true}, nil
	}
	if req.Command == "reveal" {
		path, err := kube.ConfigurationFile(req)
		if err != nil {
			return kubernetes.ConfigState{}, coded(CodeBadRequest, err)
		}
		name, args := configurationFileManager(runtime.GOOS, path)
		if name == "" {
			return kubernetes.ConfigState{}, coded(CodeBadRequest, errors.New("file manager is unavailable on this platform"))
		}
		launchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Fixed executable and separate arguments: never execute a kubeconfig or a shell.
		if err := exec.CommandContext(launchCtx, name, args...).Run(); err != nil {
			return kubernetes.ConfigState{}, coded(CodeBadRequest, errors.New("could not open the file manager"))
		}
		req = kubernetes.ConfigRequest{Command: "status"}
	}
	out, err := kube.Configurations(req)
	if err != nil {
		return out, coded(CodeBadRequest, err)
	}
	if req.Command == "remove" || req.Command == "update" || req.Command == "reset" {
		s.revalidateSessions(ctx, kubernetes.ProviderID)
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
