package synthetic

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"strings"
	"time"
)

func fluxKinds() []core.KindDescriptor {
	out := []core.KindDescriptor{}
	for _, k := range core.FluxResourceKinds {
		out = append(out, core.KindDescriptor{ID: k.Group + "/" + k.Resource, Title: k.Kind, Singular: k.Kind, Group: "API groups", Subgroup: k.Group, Scoped: true, Hidden: false, Columns: []core.Column{}})
	}
	return out
}

// Flux fixtures are finite in-memory observations; no cluster or Git is used.
func (s *session) FluxCD(ctx context.Context, scope core.ScopeSel) (core.FluxSnapshot, error) {
	out := core.FluxSnapshot{Objects: []core.FluxObject{}, Events: []core.ArgoEvent{}, Coverage: []core.ArgoCoverage{}, Discovery: "ready", CapturedAt: time.Now().UnixMilli()}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !scope.Contains("blue") {
		return out, nil
	}
	for i, k := range core.FluxResourceKinds {
		name := strings.ToLower(k.Kind)
		state := "Ready"
		if k.Kind == "Kustomization" {
			name = "delivery"
			state = "Failed"
		}
		if k.Kind == "HelmRelease" {
			name = "web-release"
			state = "Suspended"
		}
		if k.Kind == "ImageRepository" {
			state = "Reconciling"
		}
		if k.Kind == "Provider" {
			state = "Unknown"
		}
		o := core.FluxObject{Ref: core.Ref{Provider: ID, Target: s.target, Kind: k.Group + "/" + k.Resource, Scope: "blue", Name: name, UID: "flux-" + name}, Kind: k.Kind, Section: k.Section, State: state, Message: "Synthetic controller observation", Generation: 2, ObservedGeneration: 2, Conditions: []core.FluxCondition{{Type: "Ready", Status: "True", Reason: "Succeeded", Message: "Reconciliation completed", ObservedGeneration: 2}}, Links: []core.FluxLink{}, Inventory: []core.FluxResource{}, LocalDestination: true, CreatedAt: out.CapturedAt - int64(i+1)*60000}
		if k.Kind == "GitRepository" {
			o.URL = "https://github.com/example/commerce.git"
			o.Revision = "main@sha1:0123456789abcdef0123456789abcdef01234567"
		}
		if k.Kind == "Kustomization" {
			o.Path = "./deploy"
			o.Message = "Deployment has unavailable replicas"
			o.Conditions = []core.FluxCondition{{Type: "Ready", Status: "False", Reason: "HealthCheckFailed", Message: o.Message, ObservedGeneration: 2}}
			o.Links = []core.FluxLink{{Relation: "source", Group: "source.toolkit.fluxcd.io", Kind: "GitRepository", Namespace: "blue", Name: "gitrepository"}}
			o.Inventory = []core.FluxResource{{Kind: "Crate", Name: allCrates[0].key, Namespace: "blue", Version: "v1"}}
		}
		if k.Kind == "HelmRelease" {
			o.Links = []core.FluxLink{{Relation: "source", Group: "source.toolkit.fluxcd.io", Kind: "HelmChart", Namespace: "blue", Name: "helmchart"}}
		}
		out.Objects = append(out.Objects, o)
		out.Coverage = append(out.Coverage, core.ArgoCoverage{Kind: k.Kind, Scope: "blue", State: "ready"})
	}
	out.Events = []core.ArgoEvent{{ID: "flux-event", Ref: out.Objects[6].Ref, Openable: true, Type: "Warning", Reason: "HealthCheckFailed", Message: "Deployment has unavailable replicas", At: out.CapturedAt - 30000, Count: 2}}
	return out, nil
}
func (s *session) getFlux(ref core.Ref) (*core.Resource, error) {
	snapshot, err := s.FluxCD(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: ref.Scope})
	if err != nil {
		return nil, err
	}
	for _, o := range snapshot.Objects {
		if o.Ref.Kind == ref.Kind && o.Ref.Name == ref.Name {
			return &core.Resource{Ref: o.Ref, Health: core.Health{State: core.HealthUnknown}, Facts: []core.Detail{{Key: "kind", Value: o.Kind}}, YAML: "apiVersion: " + strings.Split(ref.Kind, "/")[0] + "/v1\nkind: " + o.Kind + "\nmetadata:\n  name: " + o.Ref.Name + "\n"}, nil
		}
	}
	return nil, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
}
