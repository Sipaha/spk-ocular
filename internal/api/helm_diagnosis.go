package api

import (
	"context"
	"errors"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/helm"
	"k8s.io/apimachinery/pkg/util/validation"
)

func helmDiagnosisCommand(command string) bool {
	switch command {
	case "diagnose", "compare", "observe-hook", "flux-context":
		return true
	}
	return false
}
func (s *Service) helmDiagnosis(ctx context.Context, req HelmRequest, out HelmResponse, e *sessionEntry, sess helmSession, storage helm.Storage, owner string) (HelmResponse, error) {
	fail := func() (HelmResponse, error) {
		return HelmResponse{}, coded(CodeConflict, errors.New("helm selection or storage changed; reload the release"))
	}
	if req.ReadOwner == "" || req.ReadOwner != s.configRev(owner) || req.ConfigRev != s.configRev(e.hash) {
		return fail()
	}
	if req.Namespace == "" || len(validation.IsDNS1123Label(req.Namespace)) != 0 || req.Name == "" || len(req.Name) > 253 || req.Revision < 1 {
		return HelmResponse{}, coded(CodeBadRequest, errors.New("select an explicit storage namespace and revision"))
	}
	if req.Command == "flux-context" && (req.ControllerNamespace == "" || len(validation.IsDNS1123Label(req.ControllerNamespace)) != 0) {
		return HelmResponse{}, coded(CodeBadRequest, errors.New("select one controller namespace"))
	}
	if gate, ok := e.sess.(interface {
		HelmReadContext(context.Context) (context.Context, func(), error)
	}); ok {
		bounded, done, err := gate.HelmReadContext(ctx)
		if err != nil {
			return HelmResponse{}, err
		}
		defer done()
		ctx = bounded
	}
	a, err := sess.HelmConfiguration(ctx, req.Namespace, storage)
	if err != nil {
		return HelmResponse{}, err
	}
	if req.Command == "compare" {
		v, err := helm.Compare(ctx, a, req.Name, req.Revision, req.OtherRevision)
		if err != nil {
			return HelmResponse{}, coded(CodeUnsupported, err)
		}
		out.Comparison = &v
	} else {
		v, err := helm.Diagnose(ctx, a, req.Name, req.Revision)
		if err != nil {
			return HelmResponse{}, err
		}
		v.Selected.StorageNamespace = req.Namespace
		for i := range v.History {
			v.History[i].StorageNamespace = req.Namespace
		}
		switch req.Command {
		case "diagnose":
			out.Diagnosis = &v
		case "observe-hook":
			var hook *helm.HookRecord
			for i := range v.Hooks {
				if v.Hooks[i].Index == req.HookIndex {
					hook = &v.Hooks[i]
				}
			}
			if hook == nil || hook.Coverage != "supported" {
				return HelmResponse{}, coded(CodeUnsupported, errors.New("selected hook is not covered"))
			}
			source, ok := e.sess.(interface {
				HelmHookCurrent(context.Context, helm.HookRecord) (core.Diagnosis, error)
			})
			if !ok {
				return HelmResponse{}, coded(CodeUnsupported, errors.New("current hook evidence is unavailable"))
			}
			current, err := source.HelmHookCurrent(ctx, *hook)
			if err != nil {
				return HelmResponse{}, err
			}
			out.CurrentHook = &current
		case "flux-context":
			source, ok := e.sess.(interface {
				HelmFluxContext(context.Context, string, string, helm.Release) (helm.FluxContext, error)
			})
			if !ok {
				return HelmResponse{}, coded(CodeUnsupported, errors.New("flux context is unavailable"))
			}
			flux, err := source.HelmFluxContext(ctx, req.ControllerNamespace, req.Namespace, v.Selected)
			if err != nil {
				return HelmResponse{}, err
			}
			out.Flux = &flux
		}
		if err = v.Verify(a); err != nil {
			return HelmResponse{}, err
		}
	}
	checked, err := s.checkedSession(ctx, req.Provider, req.Target, req.ConfigRev)
	if err != nil || checked != e.sess {
		return fail()
	}
	latest, err := s.helmRepos.Storage()
	if err != nil {
		return HelmResponse{}, err
	}
	if latest != storage {
		return fail()
	}
	if err = ctx.Err(); err != nil {
		return HelmResponse{}, err
	}
	return out, nil
}
