package kubernetes

import (
	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const maxReviewManagerHints = 16

// reviewManagement projects existing metadata only. In particular this must
// not call ArgoCD/FluxCD, read referenced credentials or start an observation.
func (s *session) reviewManagement(def *kindDef, u *unstructured.Unstructured) *core.ManagementContext {
	if u.GetUID() == "" {
		return nil
	}
	argo, flux := argoTracking(u), fluxTracking(u)
	if argo == nil && flux == nil {
		return nil
	}
	out := &core.ManagementContext{
		Ref:     s.ref(def, u.GetNamespace(), u.GetName(), string(u.GetUID())),
		Version: u.GetResourceVersion(), CapturedAt: s.now().UnixMilli(),
		ArgoTracking: argo, FluxTracking: flux,
	}
	if flux != nil {
		if len(flux.Candidates) > maxReviewManagerHints {
			flux.Candidates = flux.Candidates[:maxReviewManagerHints]
			out.Truncated = true
		}
		// An overlong UID must never become a shortened, apparently pinned link.
		valid := flux.Candidates[:0]
		for _, candidate := range flux.Candidates {
			if len(candidate.UID) > 256 {
				out.Truncated = true
				continue
			}
			valid = append(valid, candidate)
		}
		flux.Candidates = valid
	}
	return out
}
