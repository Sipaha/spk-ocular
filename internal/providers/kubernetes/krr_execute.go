package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/krr"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

var errKRRExecution = errors.New("KRR execution did not produce an admitted complete report")

type krrExecution struct {
	s       *session
	g       krrGrant
	journal provider.HelperJournal
	writer  actionWriter
	started atomic.Bool
}

func (h *krrExecution) Execute(ctx context.Context) (data []byte, err error) {
	if !h.started.CompareAndSwap(false, true) {
		return nil, errKRRJob
	}
	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	stopSession := context.AfterFunc(h.s.ctx, cancel)
	defer stopSession()
	if !h.s.now().Before(time.UnixMilli(h.g.Exp)) || h.s.validateKRR(ctx, h.g) != nil {
		return nil, errKRRJob
	}
	if h.s.krrRights(ctx, h.g.Options.Namespace, h.g.Name) != nil {
		return nil, errKRRExecution
	}
	creator, ok := h.writer.(resultCreator)
	if !ok || h.s.logs == nil {
		return nil, errKRRExecution
	}
	r := h.g.identity()
	r.Target = h.s.conn.target
	r.CreatedAt = h.s.now().UnixMilli()
	lease, e := h.journal.Begin(ctx, r, cancel)
	if e != nil {
		return nil, errKRRExecution
	}
	cleanupNeeded := false
	defer func() {
		finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		clean := true
		if cleanupNeeded {
			clean, e = h.s.cleanupKRRJob(finish, r, func(uid string) error { return lease.PinUID(finish, uid) })
			if e != nil || !clean {
				err = errors.Join(err, errKRRCleanup)
			}
		}
		// Finalization gets a separate budget after cleanup exhausts its deadline.
		persist, end := context.WithTimeout(context.Background(), time.Second)
		defer end()
		if lease.Finish(persist, clean) != nil {
			err = errors.Join(err, errKRRCleanup)
		}
	}()
	if !h.s.now().Before(time.UnixMilli(h.g.Exp)) || h.s.validateKRR(ctx, h.g) != nil || ctx.Err() != nil {
		return nil, errKRRJob
	}
	create, cend := context.WithTimeout(ctx, getTimeout)
	job, e := creator.createResult(create, krrJobs, h.g.Options.Namespace, h.g.job())
	cend()
	if e != nil {
		cleanupNeeded = ambiguous(e) || !isAPIStatus(e)
		return nil, errKRRExecution
	}
	cleanupNeeded = true
	if !h.g.ownsJob(job) {
		return nil, errKRRJob
	}
	r.UID = string(job.GetUID())
	if lease.PinUID(ctx, r.UID) != nil {
		return nil, errKRRCleanup
	}
	suspended, _, _ := unstructured.NestedBool(job.Object, "spec", "suspend")
	if !suspended || !h.g.validJob(job) || job.GetResourceVersion() == "" || h.s.validateKRR(ctx, h.g) != nil {
		return nil, errKRRJob
	}
	patch, _ := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": r.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": job.GetResourceVersion()},
		{"op": "test", "path": "/spec/suspend", "value": true},
		{"op": "replace", "path": "/spec/suspend", "value": false},
	})
	if h.writer.patch(ctx, krrJobs, r.Namespace, r.Name, types.JSONPatchType, patch, "") != nil {
		return nil, errKRRExecution
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil, errKRRExecution
		}
		job, e = h.s.dyn.Resource(krrJobs).Namespace(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
		if e != nil || string(job.GetUID()) != r.UID || !h.g.validJob(job) || h.s.validateKRR(ctx, h.g) != nil {
			return nil, errKRRJob
		}
		suspended, _, _ = unstructured.NestedBool(job.Object, "spec", "suspend")
		if suspended {
			return nil, errKRRJob
		}
		pods, e := h.s.krrPods(ctx, r.Namespace)
		if e != nil {
			return nil, errKRRExecution
		}
		var pod *unstructured.Unstructured
		for i := range pods {
			if krrChild(&pods[i], r) {
				if pod != nil {
					return nil, errKRRExecution
				}
				pod = &pods[i]
			}
		}
		if pod != nil {
			spec, _, _ := unstructured.NestedMap(pod.Object, "spec")
			if pod.GetUID() == "" || pod.GetLabels()[krrJobLabel] != r.ID || !h.g.validPodSpec(spec) {
				return nil, errKRRJob
			}
			phase := str(pod.Object, "status", "phase")
			if phase == "Failed" {
				return nil, errKRRExecution
			}
			if phase == "Succeeded" {
				statuses := slice(pod.Object, "status", "containerStatuses")
				if len(statuses) != 1 || str(statuses[0], "name") != "krr" {
					return nil, errKRRExecution
				}
				code, found, _ := unstructured.NestedInt64(statuses[0], "state", "terminated", "exitCode")
				if !found || code != 0 {
					return nil, errKRRExecution
				}
				return h.readReport(ctx, pod, r.UID)
			}
		}
		for _, condition := range slice(job.Object, "status", "conditions") {
			if str(condition, "type") == "Failed" && str(condition, "status") == "True" {
				return nil, errKRRExecution
			}
		}
		select {
		case <-ctx.Done():
			return nil, errKRRExecution
		case <-ticker.C:
		}
	}
}
func (h *krrExecution) readReport(ctx context.Context, p *unstructured.Unstructured, jobUID string) ([]byte, error) {
	// pods/log has no UID precondition. Fence both sides of the read and never
	// admit output from an observed same-named replacement.
	fresh, e := h.s.dyn.Resource(podsKind.gvr).Namespace(p.GetNamespace()).Get(ctx, p.GetName(), metav1.GetOptions{})
	if e != nil || !h.validLogPod(fresh, p.GetUID(), jobUID) {
		return nil, errKRRJob
	}
	stream, e := h.s.logs(ctx, podLogRequest{Namespace: p.GetNamespace(), Pod: p.GetName(), Container: "krr", LimitBytes: krr.MaxBytes + 128})
	if e != nil {
		return nil, errKRRExecution
	}
	raw, e := io.ReadAll(io.LimitReader(stream, krr.MaxBytes+129))
	_ = stream.Close()
	if e != nil || len(raw) > krr.MaxBytes+128 {
		return nil, errKRRExecution
	}
	fresh, e = h.s.dyn.Resource(podsKind.gvr).Namespace(p.GetNamespace()).Get(ctx, p.GetName(), metav1.GetOptions{})
	if e != nil || !h.validLogPod(fresh, p.GetUID(), jobUID) {
		return nil, errKRRJob
	}
	line := bytes.TrimSuffix(raw, []byte("\n"))
	stamp, payload, ok := strings.Cut(string(line), " ")
	if !ok || strings.Contains(payload, "\n") {
		return nil, errKRRExecution
	}
	if _, e = time.Parse(time.RFC3339Nano, stamp); e != nil {
		return nil, errKRRExecution
	}
	data := []byte(payload)
	report, e := krr.Parse(data, "1.31.0")
	if e != nil {
		return nil, errKRRExecution
	}
	for _, row := range report.Rows {
		if row.Namespace != h.g.Options.Namespace || (row.Kind != "Deployment" && row.Kind != "StatefulSet" && row.Kind != "DaemonSet") {
			return nil, errKRRExecution
		}
	}
	return data, nil
}

// Pod logs cannot be requested with a UID precondition. Observe the full pinned
// execution/owner/terminal contract on both sides, not merely the mutable name.
func (h *krrExecution) validLogPod(p *unstructured.Unstructured, expected types.UID, jobUID string) bool {
	if p == nil || p.GetUID() != expected || p.GetLabels()[krrJobLabel] != h.g.Nonce || str(p.Object, "status", "phase") != "Succeeded" {
		return false
	}
	identity := h.g.identity()
	identity.UID = jobUID
	spec, _, _ := unstructured.NestedMap(p.Object, "spec")
	if !krrChild(p, identity) || !h.g.validPodSpec(spec) {
		return false
	}
	statuses := slice(p.Object, "status", "containerStatuses")
	if len(statuses) != 1 || str(statuses[0], "name") != "krr" {
		return false
	}
	code, found, _ := unstructured.NestedInt64(statuses[0], "state", "terminated", "exitCode")
	return found && code == 0
}
