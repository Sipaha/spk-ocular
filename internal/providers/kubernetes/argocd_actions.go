package kubernetes

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var argoApplicationGVR = schema.GroupVersionResource{Group: argoGroup, Version: "v1alpha1", Resource: "applications"}
var argoActions = []core.ActionDescriptor{
	{ID: "argo-sync", Title: "Sync", NoAgents: true, Destructive: true},
	{ID: "argo-sync-prune", Title: "Sync with prune", NoAgents: true, Destructive: true},
	{ID: "argo-refresh", Title: "Refresh", NoAgents: true},
	{ID: "argo-refresh-hard", Title: "Hard refresh", NoAgents: true},
}

const argoRefreshKey = "argocd.argoproj.io/refresh"

func isArgoApplication(d *kindDef) bool {
	return d.discovered && d.namespaced && d.gvr == argoApplicationGVR && d.kind == "Application"
}
func isArgoAction(action string) bool {
	for _, a := range argoActions {
		if a.ID == action {
			return true
		}
	}
	return false
}
func argoSyncAction(action string) bool { return action == "argo-sync" || action == "argo-sync-prune" }

// A reviewed request pins controller-observed revisions, never a branch name
// silently resolved after review. A source/destination edit requires refresh.
var argoCommit = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)
var argoChartVersion = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,255}$`)

func argoRevisions(u *unstructured.Unstructured) ([]string, bool) {
	o := u.Object
	if (str(o, "spec", "destination", "server") == "") == (str(o, "spec", "destination", "name") == "") {
		return nil, false
	}
	status := str(o, "status", "sync", "status")
	if status != "Synced" && status != "OutOfSync" {
		return nil, false
	}
	if !reflect.DeepEqual(argoComparedDestination(fieldAt(o, "spec", "destination")), argoComparedDestination(fieldAt(o, "status", "sync", "comparedTo", "destination"))) {
		return nil, false
	}
	if !reflect.DeepEqual(argoComparedIgnores(fieldAt(o, "spec", "ignoreDifferences")), argoComparedIgnores(fieldAt(o, "status", "sync", "comparedTo", "ignoreDifferences"))) {
		return nil, false
	}
	var revisions []string
	sources := slice(o, "spec", "sources")
	if len(sources) > 0 {
		if len(sources) > 32 || !reflect.DeepEqual(argoComparedSource(fieldAt(o, "spec", "sources")), argoComparedSource(fieldAt(o, "status", "sync", "comparedTo", "sources"))) {
			return nil, false
		}
		revisions = strs(o, "status", "sync", "revisions")
		if len(revisions) != len(sources) {
			return nil, false
		}
	} else {
		source, sourceOK := fieldAt(o, "spec", "source").(map[string]any)
		if !sourceOK || !reflect.DeepEqual(argoComparedSource(source), argoComparedSource(fieldAt(o, "status", "sync", "comparedTo", "source"))) {
			return nil, false
		}
		revisions = []string{str(o, "status", "sync", "revision")}
		sources = []map[string]any{source}
	}
	for i, revision := range revisions {
		if str(sources[i], "chart") == "" {
			if !argoCommit.MatchString(revision) {
				return nil, false
			}
		} else if !argoChartVersion.MatchString(revision) {
			return nil, false
		}
		if revision == "" || len(revision) > 256 || strings.ContainsAny(revision, "\r\n\t ") {
			return nil, false
		}
	}
	return revisions, true
}
func argoUnavailable(action string, u *unstructured.Unstructured) *core.Message {
	key := ""
	o := u.Object
	switch {
	case u.GetAnnotations()["argocd.argoproj.io/skip-reconcile"] == "true":
		key = "argo.skipped"
	case fieldAt(o, "operation") != nil || str(o, "status", "operationState", "phase") == "Running" || str(o, "status", "operationState", "phase") == "Terminating":
		key = "argo.busy"
	case u.GetAnnotations()[argoRefreshKey] != "":
		key = "argo.refreshPending"
	}
	if key == "" && argoSyncAction(action) {
		if _, ok := argoRevisions(u); !ok {
			key = "argo.comparisonRequired"
		}
		options := strs(o, "spec", "syncPolicy", "syncOptions")
		tooLong := false
		for _, option := range options {
			tooLong = tooLong || len(option) > 512
		}
		if len(options) > 64 || tooLong {
			key = "argo.optionsLimit"
		}
	}
	if key != "" {
		m := msg(key)
		return &m
	}
	return nil
}
func argoEffectState(o map[string]any, st map[string]any) {
	st["spec"] = fieldAt(o, "spec")
	st["operation"] = fieldAt(o, "operation")
	st["comparison"] = fieldAt(o, "status", "sync")
	st["phase"] = fieldAt(o, "status", "operationState", "phase")
	st["refresh"] = fieldAt(o, "metadata", "annotations", argoRefreshKey)
	st["skip"] = fieldAt(o, "metadata", "annotations", "argocd.argoproj.io/skip-reconcile")
}
func argoEffects(action string, u *unstructured.Unstructured) actionEffects {
	fx := actionEffects{warnings: []core.Message{msg("argo.authorization"), msg("argo.requestOnly")}}
	if action == "argo-refresh-hard" {
		fx.warnings = append(fx.warnings, msg("argo.hardRefresh"))
	}
	if !argoSyncAction(action) {
		fx.effects = []core.Message{msg("argo.refreshEffect", "mode", map[bool]string{true: "hard", false: "normal"}[action == "argo-refresh-hard"])}
		return fx
	}
	revisions, _ := argoRevisions(u)
	destination := argoURL(str(u.Object, "spec", "destination", "server"))
	if destination == "" {
		destination = argoString(u.Object, "spec", "destination", "name")
	}
	fx.effects = []core.Message{msg("argo.syncEffect", "destination", destination, "namespace", argoString(u.Object, "spec", "destination", "namespace"), "revisions", strings.Join(revisions, ", ")), msg("argo.prune", "enabled", map[bool]string{true: "true", false: "false"}[action == "argo-sync-prune"])}
	fx.warnings = append(fx.warnings, msg("argo.controllerWrites"))
	fx.destructive = true
	for _, option := range strs(u.Object, "spec", "syncPolicy", "syncOptions") {
		fx.warnings = append(fx.warnings, msg("argo.syncOption", "option", argoText(option)))
	}
	return fx
}
func (s *session) writeArgo(ctx context.Context, def *kindDef, run provider.ActionRun, u *unstructured.Unstructured) (core.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	patch := map[string]any{"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()}}
	if argoSyncAction(run.Action) {
		revisions, ok := argoRevisions(u)
		if !ok {
			return core.Message{}, provider.Said(provider.ClassConflict, msg("argo.comparisonRequired"))
		}
		sync := map[string]any{"prune": run.Action == "argo-sync-prune"}
		if len(slice(u.Object, "spec", "sources")) > 0 {
			sync["revisions"] = revisions
		} else {
			sync["revision"] = revisions[0]
		}
		if options := strs(u.Object, "spec", "syncPolicy", "syncOptions"); len(options) > 0 {
			sync["syncOptions"] = options
		}
		patch["operation"] = map[string]any{"sync": sync, "info": []any{map[string]any{"name": "Requested by", "value": "SPK Ocular"}}}
	} else {
		mode := "normal"
		if run.Action == "argo-refresh-hard" {
			mode = "hard"
		}
		patch["metadata"].(map[string]any)["annotations"] = map[string]any{argoRefreshKey: mode}
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return core.Message{}, err
	}
	err = wr.patch(ctx, def.gvr, u.GetNamespace(), u.GetName(), types.MergePatchType, data, "")
	return msg("argo.requested", "name", u.GetName()), err
}

// runArgo sends exactly one CAS write. In particular it never retries when a
// controller consumes an operation or refresh annotation faster than a reply.
func (s *session) runArgo(ctx context.Context, def *kindDef, run provider.ActionRun) (core.ActionResult, error) {
	if !isArgoApplication(def) || !sameRoute(def, run.Expect) {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.routeChanged", "kind", def.desc.Title))
	}
	u, err := s.getConfirmed(ctx, def, run.Ref)
	if err != nil {
		return core.ActionResult{}, err
	}
	if why := actionUnavailable(def, run.Action, u); why != nil {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, *why)
	}
	if actionExpect(def, run.Action, run.Params, u) != run.Expect {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.changed", "kind", singular(def), "name", u.GetName()))
	}
	if s.beforeWrite != nil {
		s.beforeWrite(run.Action, u)
	}
	said, err := s.writeArgo(ctx, def, run, u)
	if err != nil {
		_, err = s.failedWrite(ctx, def, run, u, err, func(_ context.Context, now *unstructured.Unstructured) (string, error) {
			return actionExpect(def, run.Action, run.Params, now), nil
		})
		return core.ActionResult{}, err
	}
	return core.ActionResult{Message: said, Outcome: core.OutcomeDone}, nil
}

// Compare known JSON omitempty defaults without discarding opaque Helm values,
// plugin parameters or unknown source fields. The write fingerprint still binds
// the entire unmodified spec; this is only the controller-comparison match.
func argoComparedSource(value any) any {
	if values, ok := value.([]any); ok {
		out := make([]any, len(values))
		for i, v := range values {
			out[i] = argoComparedSource(v)
		}
		return out
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := runtime.DeepCopyJSON(object)
	dropEmpty := func(m map[string]any, keys ...string) {
		for _, key := range keys {
			value, found := m[key]
			if !found {
				continue
			}
			empty := value == nil
			switch v := value.(type) {
			case bool:
				empty = !v
			case string:
				empty = v == ""
			case []any:
				empty = len(v) == 0
			}
			if empty {
				delete(m, key)
			}
		}
	}
	dropEmpty(out, "path", "chart", "ref", "name")
	if str(out, "chart") == "" && str(out, "targetRevision") == "" {
		out["targetRevision"] = "HEAD"
	}
	for section, keys := range map[string][]string{
		"helm":      {"passCredentials", "ignoreMissingValueFiles", "skipCrds", "skipTests", "skipSchemaValidation", "valueFiles", "parameters", "fileParameters", "values", "releaseName", "version", "namespace", "kubeVersion", "apiVersions"},
		"kustomize": {"forceCommonLabels", "forceCommonAnnotations", "labelWithoutSelector", "labelIncludeTemplates", "ignoreMissingComponents", "commonAnnotationsEnvsubst", "namePrefix", "nameSuffix", "version", "namespace", "kubeVersion", "apiVersions", "images", "replicas", "components", "patches"},
		"directory": {"recurse", "include", "exclude"},
		"plugin":    {"name", "env", "parameters"},
	} {
		if m, ok := out[section].(map[string]any); ok {
			dropEmpty(m, keys...)
			if section == "helm" {
				for _, parameter := range slice(m, "parameters") {
					dropEmpty(parameter, "forceString", "value")
				}
			}
		}
	}
	return out
}
func argoComparedDestination(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := runtime.DeepCopyJSON(object)
	for _, key := range []string{"name", "server", "namespace"} {
		if out[key] == "" {
			delete(out, key)
		}
	}
	return out
}
func argoComparedIgnores(value any) any {
	if value == nil {
		return []any{}
	}
	rules, ok := value.([]any)
	if ok && len(rules) == 0 {
		return []any{}
	}
	if !ok {
		return value
	}
	out := runtime.DeepCopyJSONValue(rules).([]any)
	for _, r := range out {
		if rule, ok := r.(map[string]any); ok {
			for _, key := range []string{"group", "name", "namespace", "jsonPointers", "jqPathExpressions", "managedFieldsManagers"} {
				value := rule[key]
				empty := value == nil
				switch v := value.(type) {
				case string:
					empty = v == ""
				case []any:
					empty = len(v) == 0
				}
				if empty {
					delete(rule, key)
				}
			}
		}
	}
	return out
}
