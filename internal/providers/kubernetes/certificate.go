package kubernetes

import (
	"context"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

func certificateSupported(d *kindDef) bool {
	return d != nil && d.namespaced && d.gvr == (schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}) && kindOf(d) == "Certificate"
}

// CertificateStatus observes the controller's report. It never acquires a Secret,
// even if a Certificate, issuer or consumer refers to one.
func (s *session) CertificateStatus(parent context.Context, ref core.Ref) (core.CertificateStatus, error) {
	out := core.CertificateStatus{Ref: ref, Conditions: []core.CertificateCondition{}, RequestedNames: []core.CertificateName{}, Consumers: []core.Ref{}, IssuerState: "unknown", ConsumerState: "unknown"}
	d := s.kind(ref.Kind)
	if ref.Provider != ProviderID || ref.Target != s.target || len(validation.IsDNS1123Label(ref.Scope)) != 0 || !certificateName(ref.Name) || len(ref.Scope) > 253 || ref.UID == "" || len(ref.UID) > 256 {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "certificate status requires a namespaced UID-pinned reference"}
	}
	if !certificateSupported(d) {
		return out, &provider.Error{Class: provider.ClassUnsupported, Message: "certificate status supports discovered cert-manager.io/v1 Certificates"}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	r := certificateReader{s: s, ctx: ctx, out: &out}
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(d.gvr).Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			return nil, r.failure(e, "certificate status could not be read")
		}
		if !r.admit(u) {
			return nil, &provider.Error{Class: provider.ClassScopeLimit, Message: "certificate status exceeds snapshot budget"}
		}
		if !certificateIdentity(u, d.gvr, "Certificate", ref.Scope) || u.GetName() != ref.Name {
			return nil, &provider.Error{Class: provider.ClassUnavailable, Message: "certificate identity is invalid"}
		}
		if string(u.GetUID()) != ref.UID {
			return nil, &provider.Error{Class: provider.ClassGone, Message: "certificate was replaced"}
		}
		return u, nil
	}
	u, err := read()
	if err != nil {
		return out, err
	}
	now := s.now()
	projectCertificate(&out, u, now)
	r.issuer(u)
	r.consumers(u)
	fresh, err := read()
	if err != nil {
		return core.CertificateStatus{}, err
	}
	if fresh.GetResourceVersion() != u.GetResourceVersion() {
		return core.CertificateStatus{}, &provider.Error{Class: provider.ClassConflict, Message: "certificate changed; load a new snapshot"}
	}
	if err = ctx.Err(); err != nil {
		return core.CertificateStatus{}, err
	}
	out.CapturedAt = now.UnixMilli()
	return out, nil
}
func certificateName(s string) bool { return s != "" && len(validation.IsDNS1123Subdomain(s)) == 0 }
func certificateIdentity(u *unstructured.Unstructured, gvr schema.GroupVersionResource, kind, ns string) bool {
	return u.GetAPIVersion() == gvr.GroupVersion().String() && u.GetKind() == kind && u.GetNamespace() == ns && certificateName(u.GetName()) && u.GetUID() != "" && len(u.GetUID()) <= 256 && u.GetResourceVersion() != "" && len(u.GetResourceVersion()) <= 256
}
func certificateTime(o map[string]any, key string) core.CertificateTime {
	v, found, err := unstructured.NestedString(o, "status", key)
	if err != nil {
		return core.CertificateTime{State: "invalid"}
	}
	if !found || v == "" {
		return core.CertificateTime{State: "missing"}
	}
	if len(v) > 64 {
		return core.CertificateTime{State: "invalid"}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil || t.Year() < 1 || t.Year() > 9999 {
		return core.CertificateTime{State: "invalid"}
	}
	return core.CertificateTime{State: "reported", Value: t.UTC().Format(time.RFC3339Nano)}
}
func projectCertificate(out *core.CertificateStatus, u *unstructured.Unstructured, now time.Time) {
	out.NotBefore = certificateTime(u.Object, "notBefore")
	out.NotAfter = certificateTime(u.Object, "notAfter")
	out.Renewal = certificateTime(u.Object, "renewalTime")
	out.Validity = "unknown"
	if out.NotBefore.State == "invalid" || out.NotAfter.State == "invalid" {
		out.Validity = "invalid"
	} else if out.NotAfter.State == "missing" {
		out.Validity = "missing"
	} else {
		after, _ := time.Parse(time.RFC3339Nano, out.NotAfter.Value)
		before, _ := time.Parse(time.RFC3339Nano, out.NotBefore.Value)
		switch {
		case out.NotBefore.State == "reported" && !before.Before(after):
			out.Validity = "invalid"
		case !now.Before(after):
			out.Validity = "expired"
		case out.NotBefore.State == "missing":
			out.Validity = "unknown"
		case now.Before(before):
			out.Validity = "not_yet_valid"
		case !now.Before(after.Add(-7 * 24 * time.Hour)):
			out.Validity = "within_7_days"
		case !now.Before(after.Add(-30 * 24 * time.Hour)):
			out.Validity = "within_30_days"
		default:
			out.Validity = "valid"
		}
	}
	out.SecretName = str(u.Object, "spec", "secretName")
	if !certificateName(out.SecretName) {
		out.SecretName = ""
	}
	conditions, _, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil {
		out.Truncated = true
	}
	if len(conditions) > 32 {
		conditions = conditions[:32]
		out.Truncated = true
	}
	seen := map[string]bool{}
	for _, v := range conditions {
		c, ok := v.(map[string]any)
		if !ok {
			out.Truncated = true
			continue
		}
		typ := str(c, "type")
		if typ != "Ready" && typ != "Issuing" {
			continue
		}
		if seen[typ] {
			for i := range out.Conditions {
				if out.Conditions[i].Type == typ {
					out.Conditions[i].Status = "Unknown"
					out.Conditions[i].Freshness = "unknown"
					out.Conditions[i].Reason = ""
				}
			}
			continue
		}
		seen[typ] = true
		status := str(c, "status")
		if status != "True" && status != "False" {
			status = "Unknown"
		}
		freshness := "unknown"
		observed, found, e := unstructured.NestedInt64(c, "observedGeneration")
		if e == nil && found && observed > 0 && u.GetGeneration() > 0 {
			if observed == u.GetGeneration() {
				freshness = "current"
			} else if observed < u.GetGeneration() {
				freshness = "stale"
			}
		}
		reason := str(c, "reason")
		if len(reason) > 2048 {
			reason = ""
			out.Truncated = true
		}
		out.Conditions = append(out.Conditions, core.CertificateCondition{Type: typ, Status: status, Freshness: freshness, Reason: reason})
	}
	for _, key := range []string{"dnsNames", "ipAddresses", "uris", "emailAddresses"} {
		names, _, e := unstructured.NestedStringSlice(u.Object, "spec", key)
		if e != nil {
			out.Truncated = true
			continue
		}
		for _, name := range names {
			if len(out.RequestedNames) >= 64 {
				out.Truncated = true
				break
			}
			if name == "" || len(name) > 2048 {
				out.Truncated = true
				continue
			}
			out.RequestedNames = append(out.RequestedNames, core.CertificateName{Type: key, Value: name})
		}
	}
}

type certificateReader struct {
	s              *session
	ctx            context.Context
	out            *core.CertificateStatus
	bytes, objects int
}

func (r *certificateReader) admit(u *unstructured.Unstructured) bool {
	n, e := encodedJSONSize(u.Object)
	if e != nil || n > 1<<20 || r.bytes+n > 4<<20 || r.objects >= 132 {
		r.out.Truncated = true
		return false
	}
	r.bytes += n
	r.objects++
	return true
}
func (r *certificateReader) failure(err error, message string) error {
	if e := r.ctx.Err(); e != nil {
		return e
	}
	c, _ := classify(err)
	return &provider.Error{Class: c, Message: message}
}
func (r *certificateReader) issuer(u *unstructured.Unstructured) {
	name, _, nameErr := unstructured.NestedString(u.Object, "spec", "issuerRef", "name")
	group, _, groupErr := unstructured.NestedString(u.Object, "spec", "issuerRef", "group")
	kind, _, kindErr := unstructured.NestedString(u.Object, "spec", "issuerRef", "kind")
	if nameErr != nil || groupErr != nil || kindErr != nil || !certificateName(name) {
		r.out.IssuerState = "invalid"
		return
	}
	group = nonEmpty(group, "cert-manager.io")
	kind = nonEmpty(kind, "Issuer")
	if group != "cert-manager.io" || (kind != "Issuer" && kind != "ClusterIssuer") {
		r.out.IssuerState = "unsupported"
		return
	}
	resource, ns := "issuers", u.GetNamespace()
	if kind == "ClusterIssuer" {
		resource, ns = "clusterissuers", ""
	}
	d := r.s.kind(group + "/" + resource)
	if d == nil || d.gvr.Version != "v1" || kindOf(d) != kind || d.namespaced != (ns != "") {
		r.out.IssuerState = "unsupported"
		return
	}
	if r.s.graphMetadata == nil {
		return
	}
	m, err := r.s.graphMetadata.Resource(d.gvr).Namespace(ns).Get(r.ctx, name, metav1.GetOptions{})
	if err != nil {
		c, _ := classify(err)
		r.out.IssuerState = string(c)
		return
	}
	if m.GetName() != name || m.GetNamespace() != ns || m.GetUID() == "" || len(m.GetUID()) > 256 {
		r.out.IssuerState = "unknown"
		return
	}
	r.out.Issuer = &core.Ref{Provider: ProviderID, Target: r.s.target, Kind: d.desc.ID, Scope: ns, Name: name, UID: string(m.GetUID())}
	r.out.IssuerState = "ready"
}
func (r *certificateReader) consumers(u *unstructured.Unstructured) {
	if r.out.SecretName == "" {
		r.out.ConsumerState = "invalid"
		return
	}
	cursor := inventoryCursor{}
	seen := map[string]bool{}
	count := 0
	r.out.ConsumerState = "ready"
	for range 4 {
		list, err := r.s.dyn.Resource(ingressesKind.gvr).Namespace(u.GetNamespace()).List(r.ctx, metav1.ListOptions{Limit: 32, Continue: cursor.token})
		if err != nil {
			c, _ := classify(err)
			r.out.ConsumerState = string(c)
			return
		}
		for i := range list.Items {
			in := &list.Items[i]
			count++
			if count > 128 || !r.admit(in) {
				r.out.ConsumerState = "partial"
				r.out.Truncated = true
				return
			}
			if !certificateIdentity(in, ingressesKind.gvr, "Ingress", u.GetNamespace()) || seen[in.GetName()] {
				r.out.ConsumerState = "unknown"
				continue
			}
			seen[in.GetName()] = true
			tls, _, e := unstructured.NestedSlice(in.Object, "spec", "tls")
			if e != nil {
				r.out.ConsumerState = "unknown"
				continue
			}
			for _, v := range tls {
				entry, ok := v.(map[string]any)
				if !ok {
					r.out.ConsumerState = "unknown"
					continue
				}
				name, _, err := unstructured.NestedString(entry, "secretName")
				if err != nil {
					r.out.ConsumerState = "unknown"
					continue
				}
				if name == r.out.SecretName {
					if len(r.out.Consumers) >= 32 {
						r.out.ConsumerState = "partial"
						r.out.Truncated = true
						return
					}
					r.out.Consumers = append(r.out.Consumers, r.s.ref(ingressesKind, u.GetNamespace(), in.GetName(), string(in.GetUID())))
					break
				}
			}
		}
		more, repeated := cursor.advance(list.GetContinue())
		if repeated {
			r.out.ConsumerState = "partial"
			r.out.Truncated = true
			return
		}
		if !more {
			return
		}
	}
	r.out.ConsumerState = "partial"
	r.out.Truncated = true
}
