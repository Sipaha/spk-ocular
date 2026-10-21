package synthetic

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"time"
)

// Argo fixtures contain only controller projection data. They never contact Git,
// a controller or a Kubernetes cluster. Operation fixtures use the HTTP test seam.
var argoApplicationKind = core.KindDescriptor{ID: "argoproj.io/applications", Title: "Applications", Singular: "Application", Group: "Synthetic", Hidden: true, Scoped: true, Columns: []core.Column{}, Actions: []core.ActionDescriptor{
	{ID: "argo-sync", Title: "Sync", Destructive: true, NoAgents: true},
	{ID: "argo-sync-prune", Title: "Sync with prune", Destructive: true, NoAgents: true},
	{ID: "argo-refresh", Title: "Refresh", NoAgents: true},
	{ID: "argo-refresh-hard", Title: "Hard refresh", NoAgents: true},
}}

func (s *session) ArgoCD(ctx context.Context, scope core.ScopeSel) (core.ArgoSnapshot, error) {
	out := core.ArgoSnapshot{Objects: []core.ArgoObject{}, Servers: []core.Ref{}, Events: []core.ArgoEvent{}, Coverage: []core.ArgoCoverage{}, Discovery: "ready", CapturedAt: time.Now().UnixMilli()}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !scope.Contains("blue") {
		return out, nil
	}
	ref := core.Ref{Provider: ID, Target: s.target, Kind: argoApplicationKind.ID, Scope: "blue", Name: "storefront", UID: "argo-storefront"}
	app := core.ArgoObject{Ref: ref, Kind: "Application", Project: "commerce", Sync: "OutOfSync", Health: "Degraded", Operation: "Failed", OperationMessage: "Comparison completed; deployment needs attention.", Destination: "https://kubernetes.default.svc", DestinationNamespace: "blue", LocalDestination: true, ReconciledAt: out.CapturedAt - 60000,
		Sources:    []core.ArgoSource{{Repository: "https://git.example.test/commerce/storefront.git", Path: "deploy/production", TargetRevision: "main", ObservedRevision: "0123456789abcdef0123456789abcdef01234567"}},
		Conditions: []core.ArgoCondition{{Type: "SyncError", Message: "Deployment has unavailable replicas"}},
		Resources:  []core.ArgoResource{{Kind: "Crate", Name: allCrates[0].key, Namespace: "blue", Sync: "OutOfSync", Health: "Degraded", Message: "Fixture workload"}}, Generators: []string{}}
	out.Objects = append(out.Objects, app)
	other := app
	other.Ref.Name = "catalog"
	other.Ref.UID = "argo-catalog"
	other.Health = "Unknown"
	other.Sync = "Synced"
	other.Operation = "Unknown"
	other.OperationMessage = ""
	other.Conditions = []core.ArgoCondition{}
	other.Resources = []core.ArgoResource{}
	other.LocalDestination = false
	other.Destination = "remote-production"
	out.Objects = append(out.Objects, other)
	for _, kind := range []string{"ApplicationSet", "AppProject"} {
		name, id := "commerce", "argoproj.io/appprojects"
		if kind == "ApplicationSet" {
			name, id = "commerce-regions", "argoproj.io/applicationsets"
		}
		out.Objects = append(out.Objects, core.ArgoObject{Ref: core.Ref{Provider: ID, Target: s.target, Kind: id, Scope: "blue", Name: name, UID: "argo-" + name}, Kind: kind, Project: "commerce", Description: "Synthetic controller fixture", Generators: []string{"list"}, Sources: []core.ArgoSource{}, Conditions: []core.ArgoCondition{}, Resources: []core.ArgoResource{}})
	}
	out.Events = append(out.Events, core.ArgoEvent{ID: "argo-event", Ref: ref, Openable: true, Type: "Warning", Reason: "OperationCompleted", Message: "Sync operation failed; inspect the reported resources.", Count: 2, At: out.CapturedAt - 30000})
	for _, kind := range []string{"Application", "ApplicationSet", "AppProject", "Event", "Service"} {
		out.Coverage = append(out.Coverage, core.ArgoCoverage{Kind: kind, Scope: "blue", State: "ready"})
	}
	return out, nil
}
