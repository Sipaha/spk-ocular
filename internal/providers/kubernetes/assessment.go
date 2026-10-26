package kubernetes

import (
	"context"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const assessmentResources = 32

// AssessNamespace deliberately reuses the selected-object readers rather than
// inferring healthy workloads from a successful collection. Each reader retains
// its identity fences and interpretation limits. No watches or writes are added.
func (s *session) AssessNamespace(parent context.Context, req core.NamespaceAssessmentRequest) (core.NamespaceAssessment, error) {
	out := core.NamespaceAssessment{Namespace: req.Namespace, Check: req.Check, Resources: []core.AssessmentResource{}, Coverage: []core.AssessmentCoverage{}}
	if req.Provider != ProviderID || req.Target != s.target || len(validation.IsDNS1123Label(req.Namespace)) != 0 || (req.Check != "configuration" && req.Check != "vpa" && req.Check != "diagnosis") {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "select one namespace and a supported check"}
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	defs := []*kindDef{deploymentsKind, statefulSetsKind, daemonSetsKind}
	if req.Check != "vpa" {
		defs = append(defs, podsKind)
	}
	if req.Check == "diagnosis" {
		defs = append(defs, discoveredDef(apiResource{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespaced: true}), discoveredDef(apiResource{Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true}))
	}
	refs := []core.Ref{}
	bytes := 0
	seen := map[string]bool{}
	for _, def := range defs {
		coverage := core.AssessmentCoverage{Kind: def.desc.ID, State: "complete"}
		d := s.kind(def.desc.ID)
		if d == nil || d.gvr != def.gvr || !d.namespaced || kindOf(d) != kindOf(def) {
			coverage.State = "not-discovered"
		} else {
			readCtx, release, err := s.inventoryContext(ctx)
			if err != nil {
				return core.NamespaceAssessment{}, err
			}
			list, e := s.dyn.Resource(d.gvr).Namespace(req.Namespace).List(newScopeBudget().context(readCtx), metav1.ListOptions{Limit: assessmentResources})
			release()
			if e != nil {
				class, _ := classify(e)
				coverage.State = string(class)
			} else {
				if list.GetContinue() != "" || len(list.Items) > assessmentResources {
					coverage.State = "partial"
				}
				for i := range list.Items {
					u := &list.Items[i]
					n, e := encodedJSONSize(u.Object)
					if i >= assessmentResources || len(refs) >= assessmentResources || e != nil || n > 1<<20 || bytes+n > 4<<20 {
						coverage.State = "partial"
						break
					}
					bytes += n
					if !certificateIdentity(u, d.gvr, kindOf(d), req.Namespace) || !certificateName(u.GetName()) || u.GetUID() == "" || len(u.GetUID()) > 256 || u.GetResourceVersion() == "" || len(u.GetResourceVersion()) > 256 {
						coverage.State = "partial"
						continue
					}
					key := d.desc.ID + "/" + u.GetName()
					if seen[key] || seen["uid/"+string(u.GetUID())] {
						coverage.State = "partial"
						continue
					}
					seen[key] = true
					seen["uid/"+string(u.GetUID())] = true
					refs = append(refs, s.ref(d, req.Namespace, u.GetName(), string(u.GetUID())))
				}
			}
		}
		if coverage.State != "complete" {
			out.Partial = true
		}
		out.Coverage = append(out.Coverage, coverage)
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return core.NamespaceAssessment{}, ctx.Err()
		}
		row := core.AssessmentResource{Ref: ref, State: "complete", Highlights: []core.AssessmentHighlight{}}
		var err error
		switch req.Check {
		case "configuration":
			var result core.Reliability
			result, err = s.ResourceReliability(ctx, ref)
			if err == nil {
				for _, o := range result.Observations {
					switch o.Outcome {
					case "observed":
						row.Counts.Observed++
					case "review":
						row.Counts.Review++
					case "not-applicable":
						row.Counts.Excluded++
					default:
						row.Counts.Unknown++
					}
				}

				// Contextual review first; unknown observations remain visible after it.
				for _, outcome := range []string{"review", "unknown"} {
					for _, o := range result.Observations {
						if o.Outcome == outcome && len(row.Highlights) < 3 {
							row.Highlights = append(row.Highlights, core.AssessmentHighlight{Field: o.Field, Container: o.Container, Code: o.Code, Value: o.Value})
						}
					}
				}
				if result.Partial || (result.PDBState != "complete" && result.PDBState != "not-applicable") || (result.HPAState != "complete" && result.HPAState != "not-applicable") {
					row.State = "partial"
				}
			}
		case "vpa":
			var result core.VPAInspection
			result, err = s.ResourceVPA(ctx, ref)
			if err == nil {
				row.State = result.State
				for _, source := range result.Sources {
					row.Counts.Observed += len(source.Recommendations)
					for _, r := range source.Recommendations {
						if len(row.Highlights) < 3 {
							row.Highlights = append(row.Highlights, core.AssessmentHighlight{Field: source.Ref.Name, Container: r.Container, Code: "vpa", SourceState: source.State, Freshness: source.Freshness, Value: "CPU: " + assessmentQuantity(r.Target.CPU) + "; RAM: " + assessmentQuantity(r.Target.Memory)})
						}
					}
					if len(source.Recommendations) == 0 && len(row.Highlights) < 3 {
						row.Highlights = append(row.Highlights, core.AssessmentHighlight{Field: source.Ref.Name, Code: "vpa", SourceState: source.State, Freshness: source.Freshness})
					}
					if source.Partial || source.Freshness != "current" || source.State != "reported" {
						row.Counts.Unknown++
					}
				}
				if result.State != "no-matches" && (result.State != "complete" || (result.HPAState != "complete" && result.HPAState != "not-applicable")) {
					row.Counts.Unknown++
				}
			}
		case "diagnosis":
			var result core.Diagnosis
			result, err = s.DiagnoseResource(ctx, ref)
			if err == nil {
				for _, o := range result.Evidence {
					switch o.State {
					case core.HealthWarning, core.HealthError:
						row.Counts.Review++
					case core.HealthOK:
						row.Counts.Observed++
					default:
						row.Counts.Unknown++
					}
				}

				for _, o := range result.Evidence {
					if (o.State == core.HealthWarning || o.State == core.HealthError) && len(row.Highlights) < 3 {
						row.Highlights = append(row.Highlights, core.AssessmentHighlight{Field: o.Field, Container: o.Container, Value: o.Reason})
					}
				}
				if result.Truncated {
					row.State = "partial"
				}
				for _, c := range result.Coverage {
					if c.State != "ready" {
						row.State = "partial"
					}
				}
			}
		}
		if err != nil {
			class, _ := classify(err)
			row.State = string(class)
			row.Counts.Unknown++
		}
		if (row.State != "complete" && row.State != "no-matches") || row.Counts.Unknown > 0 {
			out.Partial = true
		}
		out.Resources = append(out.Resources, row)
	}
	if ctx.Err() != nil {
		return core.NamespaceAssessment{}, ctx.Err()
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}

func assessmentQuantity(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
