package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/helm"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
)

type helmSession interface {
	HelmConfiguration(context.Context, string, helm.Storage) (*action.Configuration, error)
}
type HelmRequest struct {
	Command    string         `json:"command"`
	Provider   string         `json:"provider"`
	Target     string         `json:"target"`
	ConfigRev  string         `json:"configRev"`
	Scope      core.ScopeSel  `json:"scope"`
	Namespace  string         `json:"namespace"`
	Name       string         `json:"name"`
	Revision   int            `json:"revision"`
	Repository string         `json:"repository"`
	Chart      helm.ChartRef  `json:"chart"`
	Settings   *helm.Settings `json:"settings,omitempty"`
	Operation  helm.Operation `json:"operation"`
	PlanID     string         `json:"planId"`
}
type HelmResponse struct {
	Problems  []string       `json:"problems,omitempty"`
	ConfigRev string         `json:"configRev,omitempty"`
	Settings  *helm.Settings `json:"settings,omitempty"`
	Charts    []helm.Chart   `json:"charts,omitempty"`
	Chart     *helm.Chart    `json:"chart,omitempty"`
	Releases  []helm.Release `json:"releases,omitempty"`
	Detail    *helm.Detail   `json:"detail,omitempty"`
	Plan      *helm.Plan     `json:"plan,omitempty"`
	Result    *helm.Result   `json:"result,omitempty"`
}

func helmKinds() []core.KindDescriptor {
	return []core.KindDescriptor{
		{ID: "ocular.helm.releases", Title: "Releases", Group: "Helm", Workspace: "helm-releases", Scoped: true, Columns: []core.Column{}},
		{ID: "ocular.helm.charts", Title: "Charts", Group: "Helm", Workspace: "helm-charts", Columns: []core.Column{}},
	}
}

// Helm is deliberately absent from the agent socket's method registry. The UI
// marker is checked as defense in depth: wildcard resource grants never apply.
func (s *Service) Helm(ctx context.Context, req HelmRequest) (HelmResponse, error) {
	var out HelmResponse
	if !fromUI(ctx) {
		return out, coded("forbidden", errors.New("Helm is available only to the application UI"))
	}
	limit := 90 * time.Second
	if req.Command == "run" {
		limit = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	switch req.Command {
	case "settings":
		v, e := s.helmRepos.Settings()
		out.Settings = &v
		return out, e
	case "save-settings":
		if req.Settings == nil {
			return out, coded(CodeBadRequest, errors.New("settings are required"))
		}
		if err := s.helmRepos.Save(*req.Settings); err != nil {
			return out, err
		}
		v, err := s.helmRepos.Settings()
		out.Settings = &v
		return out, err
	case "catalog":
		if req.Repository != "" {
			v, e := s.helmRepos.Catalog(ctx, req.Repository)
			out.Charts = v
			return out, e
		}
		settings, err := s.helmRepos.Settings()
		if err != nil {
			return out, err
		}
		out.Charts = []helm.Chart{}
		for _, repo := range settings.Repositories {
			charts, err := s.helmRepos.Catalog(ctx, repo.Name)
			if err != nil {
				out.Problems = append(out.Problems, repo.Name+": "+err.Error())
				continue
			}
			out.Charts = append(out.Charts, charts...)
		}
		return out, nil
	case "chart":
		_, v, e := s.helmRepos.Load(ctx, req.Chart)
		out.Chart = &v
		return out, e
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return out, err
	}
	sess, ok := e.sess.(helmSession)
	if !ok {
		return out, coded(CodeUnsupported, errors.New("connection does not support Helm"))
	}
	out.ConfigRev = s.configRev(e.hash)
	storage, err := s.helmRepos.Storage()
	if err != nil {
		return out, err
	}
	b, err := json.Marshal(storage)
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(b)
	owner := e.owner + hex.EncodeToString(hash[:])
	factory := func(ctx context.Context, ns string) (*action.Configuration, error) {
		return sess.HelmConfiguration(ctx, ns, storage)
	}
	switch req.Command {
	case "list":
		if !req.Scope.Valid() {
			return out, coded(CodeBadRequest, errors.New("invalid namespace selection"))
		}
		namespaces := req.Scope.SelectedNames()
		if req.Scope.Mode == core.ScopeAll {
			namespaces = []string{""}
		}
		out.Releases = []helm.Release{}
		for _, ns := range namespaces {
			label := ns
			if label == "" {
				label = "all namespaces"
			}
			a, err := factory(ctx, ns)
			if err != nil {
				out.Problems = append(out.Problems, label+": "+err.Error())
				continue
			}
			releases, err := helm.List(a)
			if err != nil {
				out.Problems = append(out.Problems, label+": "+err.Error())
				continue
			}
			out.Releases = append(out.Releases, releases...)
		}

	case "detail":
		if req.Namespace == "" || req.Name == "" {
			return out, coded(CodeBadRequest, errors.New("release and namespace are required"))
		}
		a, err := factory(ctx, req.Namespace)
		if err != nil {
			return out, err
		}
		v, err := helm.Get(a, req.Name, req.Revision)
		if err == nil {
			if resolver, ok := e.sess.(interface {
				HelmResourceRef(helm.Resource, string) *core.Ref
			}); ok {
				for i := range v.Resources {
					v.Resources[i].Ref = resolver.HelmResourceRef(v.Resources[i], v.Namespace)
				}
			}
		}
		out.Detail = &v
		return out, err
	case "prepare":
		var ch chart.Charter
		if req.Operation.Action == "install" || req.Operation.Action == "upgrade" {
			ch, _, err = s.helmRepos.Load(ctx, req.Operation.Chart)
			if err != nil {
				return out, err
			}
		}
		v, err := s.helmPlans.Prepare(ctx, owner, factory, req.Operation, ch)
		out.Plan = &v
		return out, err
	case "run":
		checked, err := s.checkedSession(ctx, req.Provider, req.Target, req.ConfigRev)
		if err != nil {
			return out, err
		}
		if checked != e.sess {
			return out, coded(CodeConflict, errors.New("connection changed after review"))
		}
		v, err := s.helmPlans.Run(ctx, owner, req.PlanID, factory)
		out.Result = &v
		return out, err
	case "forget":
		s.helmPlans.Forget(req.PlanID, owner)
	default:
		return out, coded(CodeBadRequest, errors.New("unknown Helm command"))
	}
	return out, nil
}
