package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/krr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

var errKRRCleanup = krr.ErrCleanup

// A cancelled/lost CREATE response can precede a late server commit. Do not
// convert an immediate 404 into success; retain the durable intent through the
// existing five-minute ownership horizon before confirming observed absence.
const krrCreateAbsenceGrace = 5 * time.Minute

// Only execution fields are hashed; no source URL or script is persisted.
func krrContractSHA(spec map[string]any) string {
	selected := make(map[string]any, len(spec))
	for key, value := range spec {
		selected[key] = value
	}
	for key, value := range map[string]any{"dnsPolicy": "ClusterFirst", "schedulerName": "default-scheduler", "preemptionPolicy": "PreemptLowerPriority"} {
		if selected[key] == value {
			delete(selected, key)
		}
	}
	if selected["serviceAccount"] == selected["serviceAccountName"] {
		delete(selected, "serviceAccount")
	}
	b, _ := json.Marshal(selected)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func krrChild(p *unstructured.Unstructured, r core.OwnedHelper) bool {
	for _, o := range p.GetOwnerReferences() {
		if o.APIVersion == "batch/v1" && o.Kind == "Job" && o.Name == r.Name && string(o.UID) == r.UID && r.UID != "" {
			return true
		}
	}
	return false
}

// List without a mutable label selector, so editing labels cannot hide owned
// children after foreground deletion or a TTL controller removed the parent.
func (s *session) krrPods(ctx context.Context, ns string) ([]unstructured.Unstructured, error) {
	var pods []unstructured.Unstructured
	next := ""
	for page := 0; page < 16; page++ {
		list, e := s.dyn.Resource(podsKind.gvr).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 256, Continue: next})
		if e != nil {
			return nil, errKRRCleanup
		}
		if len(list.Items) > 256 {
			return nil, errKRRCleanup
		}
		pods = append(pods, list.Items...)
		next = list.GetContinue()
		if next == "" {
			return pods, nil
		}
	}
	return nil, errKRRCleanup
}
func (s *session) cleanupKRRJob(ctx context.Context, r core.OwnedHelper, pin func(string) error) (bool, error) {
	if r.Kind != "krr-job" || r.Provider != ProviderID || s.conn == nil || r.Target != s.conn.target || len(r.ID) != 32 || !strings.HasPrefix(r.Name, "ocular-krr-") || r.NamespaceUID == "" || r.ServiceAccountUID == "" || len(r.ContractSHA) != 64 || r.NodeUID != "" || r.NodeName != "" {
		return false, errKRRCleanup
	}
	ns, e := s.dyn.Resource(namespaceGV).Get(ctx, r.Namespace, metav1.GetOptions{})
	if e != nil || string(ns.GetUID()) != r.NamespaceUID {
		return false, errKRRCleanup
	}
	w := s.writer
	if w == nil {
		w = dynWriter{s.dyn}
	}
	for {
		job, e := s.dyn.Resource(krrJobs).Namespace(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
		if e != nil && !apierrors.IsNotFound(e) {
			return false, errKRRCleanup
		}
		if e == nil && (r.UID == "" || string(job.GetUID()) == r.UID) {
			g := krrGrant{Options: structOptions(r), NamespaceUID: r.NamespaceUID, Name: r.Name, Nonce: r.ID}
			if !g.ownsJob(job) {
				return false, errKRRCleanup
			}
			if r.UID == "" {
				ps, _, _ := unstructured.NestedMap(job.Object, "spec", "template", "spec")
				if krrContractSHA(ps) != r.ContractSHA || job.GetDeletionTimestamp() != nil {
					return false, errKRRCleanup
				}
				r.UID = string(job.GetUID())
				if pin == nil || pin(r.UID) != nil {
					return false, errKRRCleanup
				}
			}
			uid := types.UID(r.UID)
			policy := metav1.DeletePropagationForeground
			e = w.delete(ctx, krrJobs, r.Namespace, r.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, PropagationPolicy: &policy})
			if e != nil && !apierrors.IsNotFound(e) {
				return false, errKRRCleanup
			}
		}
		pods, e := s.krrPods(ctx, r.Namespace)
		if e != nil {
			return false, e
		}
		remaining := false
		for i := range pods {
			p := &pods[i]
			owned := krrChild(p, r)
			uncertain := !owned && p.GetLabels()[krrJobLabel] == r.ID
			for _, owner := range p.GetOwnerReferences() {
				if r.UID == "" && owner.APIVersion == "batch/v1" && owner.Kind == "Job" && owner.Name == r.Name {
					uncertain = true
				}
				if r.UID != "" && string(owner.UID) == r.UID && !owned {
					uncertain = true
				}
			}
			// Mutated labels or owner flags cannot turn an observed possible child
			// into confirmed absence. Uncertain ownership never grants a delete.
			if uncertain {
				return false, errKRRCleanup
			}
			if !owned {
				continue
			}
			if p.GetUID() == "" {
				return false, errKRRCleanup
			}
			remaining = true
			uid := p.GetUID()
			zero := int64(0)
			e = w.delete(ctx, podsKind.gvr, r.Namespace, p.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, GracePeriodSeconds: &zero})
			if e != nil && !apierrors.IsNotFound(e) {
				return false, errKRRCleanup
			}
		}
		// A successful DELETE is not confirmation: verify absence on a later read.
		if !remaining && (apierrors.IsNotFound(e) || job == nil || (r.UID != "" && string(job.GetUID()) != r.UID)) {
			if r.UID == "" && (r.CreatedAt <= 0 || s.now().Before(time.UnixMilli(r.CreatedAt).Add(krrCreateAbsenceGrace))) {
				return false, errKRRCleanup
			}
			return true, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, errKRRCleanup
		case <-timer.C:
		}
	}
}

func structOptions(r core.OwnedHelper) krr.RunOptions {
	return krr.RunOptions{Namespace: r.Namespace, ServiceAccount: r.ServiceAccount}
}
