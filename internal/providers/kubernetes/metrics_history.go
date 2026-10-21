package kubernetes

import (
	"context"
	"math"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/metricshistory"
	"github.com/spk/spk-ocular/internal/provider"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

func (s *session) ResourceMetricsHistory(parent context.Context, req core.HistoryRequest) (core.MetricsHistory, error) {
	ref := req.Ref
	if ref.Provider != ProviderID || ref.Target != s.target || ref.Kind != "pods" || len(validation.IsDNS1123Label(ref.Scope)) != 0 || !certificateName(ref.Name) || !metricshistory.ValidUID(ref.UID) {
		return core.MetricsHistory{}, &provider.Error{Class: provider.ClassInvalid, Message: "history requires a namespaced UID-pinned Pod"}
	}
	if err := metricshistory.Validate(req.Source, req.Window); err != nil {
		return core.MetricsHistory{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return core.MetricsHistory{}, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(podsKind.gvr).Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			class, _ := classify(e)
			return nil, &provider.Error{Class: class, Message: "history Pod could not be read"}
		}
		size, e := encodedJSONSize(u.Object)
		if e != nil || size > 1<<20 {
			return nil, &provider.Error{Class: provider.ClassScopeLimit, Message: "history Pod exceeds source limit"}
		}
		if !certificateIdentity(u, podsKind.gvr, "Pod", ref.Scope) || u.GetName() != ref.Name || string(u.GetUID()) != ref.UID {
			return nil, &provider.Error{Class: provider.ClassGone, Message: "history Pod identity changed"}
		}
		return u, nil
	}
	u, err := read()
	if err != nil {
		return core.MetricsHistory{}, err
	}
	created := u.GetCreationTimestamp()
	if created.IsZero() {
		return core.MetricsHistory{}, &provider.Error{Class: provider.ClassUnavailable, Message: "history Pod creation time is unknown"}
	}
	now := s.now()
	duration, _ := time.ParseDuration(req.Window)
	end := now.Unix() / 60 * 60
	start := end - int64(duration/time.Second)
	requestedStart := start
	if birth := (created.Unix() + 59) / 60 * 60; birth > start {
		start = birth
	}
	out := core.MetricsHistory{Ref: ref, ResourceVersion: u.GetResourceVersion(), Source: metricshistory.SourceDescription(req.Source), Scope: "dedicated-asserted", CapturedAt: now.UnixMilli(), RequestedStart: requestedStart, Start: start, End: end, Step: 60, Metrics: []core.HistoryMetric{}, Declarations: []core.HistoryDeclaration{}}
	if !req.Source.Dedicated {
		out.Scope = "cluster-label-asserted"
	}
	containers := slice(u.Object, "spec", "containers")
	if len(containers) == 0 || len(containers) > metricshistory.MaxContainers {
		return core.MetricsHistory{}, &provider.Error{Class: provider.ClassScopeLimit, Message: "history supports up to sixteen app containers"}
	}
	names := make([]string, 0, len(containers))
	seen := map[string]bool{}
	for _, c := range containers {
		name := str(c, "name")
		if len(validation.IsDNS1123Label(name)) != 0 || seen[name] {
			return core.MetricsHistory{}, &provider.Error{Class: provider.ClassUnavailable, Message: "invalid history container identity"}
		}
		seen[name] = true
		names = append(names, name)
		declaration := core.HistoryDeclaration{Container: name}
		quantity := func(group, key string) *float64 {
			q, e := resource.ParseQuantity(str(c, "resources", group, key))
			if e != nil || q.Sign() < 0 {
				return nil
			}
			v := q.AsApproximateFloat64()
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil
			}
			return &v
		}
		declaration.CPURequest = quantity("requests", "cpu")
		declaration.CPULimit = quantity("limits", "cpu")
		declaration.MemoryRequest = quantity("requests", "memory")
		declaration.MemoryLimit = quantity("limits", "memory")
		out.Declarations = append(out.Declarations, declaration)
	}
	if start <= end {
		out.Metrics, err = metricshistory.Read(ctx, req, start, end, names)
		if err != nil {
			if ctx.Err() != nil {
				return core.MetricsHistory{}, ctx.Err()
			}
			return core.MetricsHistory{}, &provider.Error{Class: provider.ClassUnavailable, Message: "history source could not be read"}
		}
	}
	fresh, err := read()
	if err != nil {
		return core.MetricsHistory{}, err
	}
	if fresh.GetResourceVersion() != u.GetResourceVersion() {
		return core.MetricsHistory{}, &provider.Error{Class: provider.ClassConflict, Message: "history Pod changed; load again"}
	}
	if ctx.Err() != nil {
		return core.MetricsHistory{}, ctx.Err()
	}
	return out, nil
}
