package kubernetes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/krr"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const krrJobLabel = "spk-ocular.dev/krr-job"

var krrJobs = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
var krrAccounts = schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
var errKRRJob = errors.New("KRR execution contract or identity changed; review again")

type krrGrant struct {
	Options      krr.RunOptions `json:"options"`
	NamespaceUID string         `json:"namespaceUID"`
	AccountUID   string         `json:"accountUID"`
	Name         string         `json:"name"`
	Nonce        string         `json:"nonce"`
	Inc          string         `json:"inc"`
	Exp          int64          `json:"exp"`
}

func krrSignature(b []byte) string {
	m := hmac.New(sha256.New, printKey)
	m.Write([]byte("spk-ocular/krr-job/v1\x00"))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}
func (g krrGrant) token() string {
	b, _ := json.Marshal(g)
	return base64.RawURLEncoding.EncodeToString(b) + "." + krrSignature(b)
}
func (s *session) ReviewKRR(ctx context.Context, o krr.RunOptions) (krr.Review, error) {
	if !o.Valid() {
		return krr.Review{}, errKRRJob
	}
	ns, e := s.dyn.Resource(namespaceGV).Get(ctx, o.Namespace, metav1.GetOptions{})
	if e != nil {
		return krr.Review{}, errKRRJob
	}
	sa, e := s.dyn.Resource(krrAccounts).Namespace(o.Namespace).Get(ctx, o.ServiceAccount, metav1.GetOptions{})
	if e != nil {
		return krr.Review{}, errKRRJob
	}
	if ns.GetUID() == "" || sa.GetUID() == "" || ns.GetDeletionTimestamp() != nil || sa.GetDeletionTimestamp() != nil {
		return krr.Review{}, errKRRJob
	}
	g := krrGrant{Options: o, NamespaceUID: string(ns.GetUID()), AccountUID: string(sa.GetUID()), Name: "ocular-krr-" + randomHex(8), Nonce: randomHex(16), Inc: s.incarnation, Exp: s.now().Add(runGrantTTL).UnixMilli()}
	if e = s.krrRights(ctx, g.Options.Namespace, g.Name); e != nil {
		return krr.Review{}, e
	}
	return krr.Review{Options: o, Expect: g.token(), Image: krr.Image, NamespaceUID: g.NamespaceUID, ServiceAccountUID: g.AccountUID, Name: g.Name, DeadlineSeconds: 600}, nil
}
func (s *session) validateKRR(ctx context.Context, g krrGrant) error {
	ns, e := s.dyn.Resource(namespaceGV).Get(ctx, g.Options.Namespace, metav1.GetOptions{})
	if e != nil {
		return errKRRJob
	}
	sa, e := s.dyn.Resource(krrAccounts).Namespace(g.Options.Namespace).Get(ctx, g.Options.ServiceAccount, metav1.GetOptions{})
	if e != nil {
		return errKRRJob
	}
	if string(ns.GetUID()) != g.NamespaceUID || string(sa.GetUID()) != g.AccountUID || ns.GetDeletionTimestamp() != nil || sa.GetDeletionTimestamp() != nil {
		return errKRRJob
	}
	return nil
}
func (s *session) PrepareKRR(ctx context.Context, r krr.Review, j provider.HelperJournal) (krr.Execution, error) {
	var g krrGrant
	if j == nil || len(r.Expect) > 8192 || !r.Options.Valid() {
		return nil, errKRRJob
	}
	enc, sig, ok := strings.Cut(r.Expect, ".")
	if !ok {
		return nil, errKRRJob
	}
	b, e := base64.RawURLEncoding.DecodeString(enc)
	if e != nil || !hmac.Equal([]byte(sig), []byte(krrSignature(b))) || json.Unmarshal(b, &g) != nil || g.Options != r.Options || g.Inc != s.incarnation || g.Nonce == "" || g.Name != r.Name || g.NamespaceUID != r.NamespaceUID || g.AccountUID != r.ServiceAccountUID || r.Image != krr.Image || r.DeadlineSeconds != 600 || !s.now().Before(time.UnixMilli(g.Exp)) {
		return nil, errKRRJob
	}
	if e = s.validateKRR(ctx, g); e != nil {
		return nil, e
	}
	if e = s.spent.spend(runGrant{Nonce: g.Nonce, Exp: g.Exp}, s.now); e != nil {
		return nil, e
	}
	w := s.writer
	if w == nil {
		w = dynWriter{s.dyn}
	}
	return &krrExecution{s: s, g: g, journal: j, writer: w}, nil
}
func (g krrGrant) identity() core.OwnedHelper {
	return core.OwnedHelper{Kind: "krr-job", ContractSHA: krrContractSHA(g.podSpec()), ID: g.Nonce, Provider: ProviderID, Target: "", Namespace: g.Options.Namespace, NamespaceUID: g.NamespaceUID, ServiceAccount: g.Options.ServiceAccount, ServiceAccountUID: g.AccountUID, Name: g.Name}
}
func (g krrGrant) podSpec() map[string]any {
	return map[string]any{
		"nodeSelector": map[string]any{"kubernetes.io/os": "linux"}, "enableServiceLinks": false,
		"serviceAccountName": g.Options.ServiceAccount, "automountServiceAccountToken": false, "restartPolicy": "Never", "activeDeadlineSeconds": int64(600), "terminationGracePeriodSeconds": int64(0),
		"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": int64(65532), "runAsGroup": int64(65532), "fsGroup": int64(65532), "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"volumes": []any{
			map[string]any{"name": "work", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "64Mi"}},
			map[string]any{"name": "api", "projected": map[string]any{
				"defaultMode": int64(420),
				"sources": []any{
					map[string]any{"serviceAccountToken": map[string]any{"path": "token", "expirationSeconds": int64(600)}},
					map[string]any{"configMap": map[string]any{"name": "kube-root-ca.crt", "items": []any{map[string]any{"key": "ca.crt", "path": "ca.crt"}}}},
					map[string]any{"downwardAPI": map[string]any{"items": []any{map[string]any{"path": "namespace", "fieldRef": map[string]any{"apiVersion": "v1", "fieldPath": "metadata.namespace"}}}}},
				},
			}},
		},
		"containers": []any{map[string]any{"name": "krr", "image": krr.Image, "imagePullPolicy": "IfNotPresent", "workingDir": "/tmp", "command": []any{"python", "-c", krr.RunnerScript, g.Options.Namespace, g.Options.Source, g.Options.Label, g.Options.Cluster},
			"env":                    []any{map[string]any{"name": "HOME", "value": "/tmp"}, map[string]any{"name": "TMPDIR", "value": "/tmp"}, map[string]any{"name": "PYTHONDONTWRITEBYTECODE", "value": "1"}},
			"volumeMounts":           []any{map[string]any{"name": "work", "mountPath": "/tmp"}, map[string]any{"name": "api", "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount", "readOnly": true}},
			"securityContext":        map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}}},
			"terminationMessagePath": "/dev/termination-log", "terminationMessagePolicy": "File",
			"resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "256Mi"}, "limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
		}},
	}
}
func (g krrGrant) job() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": g.Name, "namespace": g.Options.Namespace, "labels": map[string]any{krrJobLabel: g.Nonce}, "ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Namespace", "name": g.Options.Namespace, "uid": g.NamespaceUID}}}, "spec": map[string]any{"suspend": true, "backoffLimit": int64(0), "activeDeadlineSeconds": int64(600), "ttlSecondsAfterFinished": int64(300), "parallelism": int64(1), "completions": int64(1), "template": map[string]any{"metadata": map[string]any{"labels": map[string]any{krrJobLabel: g.Nonce}}, "spec": g.podSpec()}}}}
}

// Admission defaults are tolerated only outside execution/security fields. Sidecars,
// Secret mounts, token automount, host access and changed entrypoints fail closed.
func (g krrGrant) validPodSpec(p map[string]any) bool {
	for k, v := range g.podSpec() {
		if !reflect.DeepEqual(p[k], v) {
			return false
		}
	}
	allowed := map[string]bool{"dnsPolicy": true, "schedulerName": true, "nodeName": true, "enableServiceLinks": true, "preemptionPolicy": true, "priority": true, "tolerations": true, "serviceAccount": true}
	for k := range g.podSpec() {
		allowed[k] = true
	}
	for k := range p {
		if !allowed[k] {
			return false
		}
	}
	if v, ok := p["serviceAccount"]; ok && v != g.Options.ServiceAccount {
		return false
	}
	return true
}
func (g krrGrant) ownsJob(p *unstructured.Unstructured) bool {
	if p == nil || p.GetUID() == "" || p.GetName() != g.Name || p.GetNamespace() != g.Options.Namespace || p.GetLabels()[krrJobLabel] != g.Nonce {
		return false
	}
	owned := false
	for _, o := range p.GetOwnerReferences() {
		if o.Kind == "Namespace" && o.APIVersion == "v1" && o.Name == g.Options.Namespace && string(o.UID) == g.NamespaceUID {
			owned = true
		}
	}
	return owned
}
func (g krrGrant) validJob(p *unstructured.Unstructured) bool {
	if !g.ownsJob(p) {
		return false
	}
	expected, _, _ := unstructured.NestedMap(g.job().Object, "spec")
	actual, _, _ := unstructured.NestedMap(p.Object, "spec")
	for _, k := range []string{"backoffLimit", "activeDeadlineSeconds", "ttlSecondsAfterFinished", "parallelism", "completions"} {
		if !reflect.DeepEqual(actual[k], expected[k]) {
			return false
		}
	}
	allowed := map[string]bool{"suspend": true, "backoffLimit": true, "activeDeadlineSeconds": true, "ttlSecondsAfterFinished": true, "parallelism": true, "completions": true, "template": true, "selector": true, "manualSelector": true, "completionMode": true, "podReplacementPolicy": true, "managedBy": true}
	for key := range actual {
		if !allowed[key] {
			return false
		}
	}
	if value, exists := actual["manualSelector"]; exists && value != false {
		return false
	}
	if value, exists := actual["completionMode"]; exists && value != "NonIndexed" {
		return false
	}
	if value, exists := actual["managedBy"]; exists && value != "kubernetes.io/job-controller" {
		return false
	}
	if value, exists := actual["podReplacementPolicy"]; exists && value != "TerminatingOrFailed" && value != "Failed" {
		return false
	}
	if _, ok := actual["suspend"].(bool); !ok {
		return false
	}
	ps, _, _ := unstructured.NestedMap(p.Object, "spec", "template", "spec")
	if node, exists := ps["nodeName"]; exists && node != "" {
		return false
	}
	labels, _, _ := unstructured.NestedStringMap(p.Object, "spec", "template", "metadata", "labels")
	return labels[krrJobLabel] == g.Nonce && g.validPodSpec(ps)
}

// SelfSubjectAccessReviews test the connected operator's lifecycle permissions,
// never impersonate or grant the selected runner account any authority.
func (s *session) krrRights(ctx context.Context, namespace, name string) error {
	for _, q := range []struct{ verb, group, resource, sub, name string }{
		{"create", "batch", "jobs", "", ""}, {"get", "batch", "jobs", "", name}, {"patch", "batch", "jobs", "", name}, {"delete", "batch", "jobs", "", name},
		{"list", "", "pods", "", ""}, {"get", "", "pods", "", ""}, {"delete", "", "pods", "", ""}, {"get", "", "pods", "log", ""},
	} {
		attrs := map[string]any{"verb": q.verb, "group": q.group, "resource": q.resource, "namespace": namespace}
		if q.name != "" {
			attrs["name"] = q.name
		}
		if q.sub != "" {
			attrs["subresource"] = q.sub
		}
		state, _ := s.access(ctx, attrs)
		if state != core.RightsAllowed {
			return errors.New("KRR requires confirmed Job create/get/patch/delete and Pod list/get/delete/log permissions in the selected namespace")
		}
	}
	return nil
}
