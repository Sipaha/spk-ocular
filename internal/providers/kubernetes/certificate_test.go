package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	kt "k8s.io/client-go/testing"
)

var certificateGVR = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
var certificateNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func certificateObject() *unstructured.Unstructured {
	u := mk("cert-manager.io/v1", "Certificate", "blue", "web-tls", "cert-uid", map[string]any{"spec": map[string]any{"secretName": "web-tls", "issuerRef": map[string]any{"name": "public"}, "dnsNames": []any{"web.example"}, "privateKey": map[string]any{"secret": "PRIVATE-DESIRED"}}, "status": map[string]any{"notBefore": certificateNow.Add(-time.Hour).Format(time.RFC3339), "notAfter": certificateNow.Add(30 * 24 * time.Hour).Format(time.RFC3339), "renewalTime": certificateNow.Format(time.RFC3339), "conditions": []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(2), "message": "PRIVATE-MESSAGE"}}}})
	u.SetResourceVersion("1")
	u.SetGeneration(2)
	return u
}
func certificateRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "t", Kind: "cert-manager.io/certificates", Scope: "blue", Name: "web-tls", UID: "cert-uid"}
}
func certificateCatalog(s *session) {
	defs := append([]*kindDef{}, allKinds.list...)
	for _, x := range []struct {
		resource, kind string
		ns             bool
	}{{"certificates", "Certificate", true}, {"issuers", "Issuer", true}, {"clusterissuers", "ClusterIssuer", false}} {
		defs = append(defs, discoveredDef(apiResource{Group: "cert-manager.io", Version: "v1", Resource: x.resource, Kind: x.kind, Namespaced: x.ns, Verbs: lw}))
	}
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(defs...), state: catalogReady})
}
func certificateFake(objs ...runtime.Object) *fake.FakeDynamicClient {
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{certificateGVR: "CertificateList", ingressesKind.gvr: "IngressList"}, objs...)
}
func TestCertificateTimeBoundariesAndUnknownEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		delta time.Duration
		state string
	}{{"expired", 0, "expired"}, {"future", time.Nanosecond, "within_7_days"}, {"7inclusive", 7 * 24 * time.Hour, "within_7_days"}, {"after7", 7*24*time.Hour + time.Nanosecond, "within_30_days"}, {"30inclusive", 30 * 24 * time.Hour, "within_30_days"}, {"after30", 30*24*time.Hour + time.Nanosecond, "valid"}} {
		t.Run(tc.name, func(t *testing.T) {
			u := certificateObject()
			require.NoError(t, unstructured.SetNestedField(u.Object, certificateNow.Add(tc.delta).Format(time.RFC3339Nano), "status", "notAfter"))
			out := core.CertificateStatus{}
			projectCertificate(&out, u, certificateNow)
			require.Equal(t, tc.state, out.Validity)
		})
	}
	for _, tc := range []struct {
		name, state string
		change      func(*unstructured.Unstructured)
	}{
		{"not yet", "not_yet_valid", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, certificateNow.Add(time.Minute).Format(time.RFC3339), "status", "notBefore")
		}},
		{"missing expiry", "missing", func(u *unstructured.Unstructured) { unstructured.RemoveNestedField(u.Object, "status", "notAfter") }},
		{"missing start", "unknown", func(u *unstructured.Unstructured) { unstructured.RemoveNestedField(u.Object, "status", "notBefore") }},
		{"malformed", "invalid", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, "PRIVATE-BAD-TIME", "status", "notAfter")
		}},
		{"wrong type", "invalid", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, int64(3), "status", "notAfter")
		}},
		{"inverted", "invalid", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, certificateNow.Add(31*24*time.Hour).Format(time.RFC3339), "status", "notBefore")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := certificateObject()
			tc.change(u)
			out := core.CertificateStatus{}
			projectCertificate(&out, u, certificateNow)
			require.Equal(t, tc.state, out.Validity)
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
		})
	}
}
func TestCertificateConditionFreshnessAndProjectionLimits(t *testing.T) {
	u := certificateObject()
	c := slice(u.Object, "status", "conditions")[0]
	c["observedGeneration"] = int64(1)
	c["reason"] = strings.Repeat("x", 2049)
	names := []any{}
	for range 70 {
		names = append(names, "web.example")
	}
	_ = unstructured.SetNestedSlice(u.Object, names, "spec", "dnsNames")
	out := core.CertificateStatus{}
	projectCertificate(&out, u, certificateNow)
	require.True(t, out.Truncated)
	require.Len(t, out.RequestedNames, 64)
	require.Equal(t, "stale", out.Conditions[0].Freshness)
	require.Empty(t, out.Conditions[0].Reason)
	delete(c, "observedGeneration")
	out = core.CertificateStatus{}
	projectCertificate(&out, u, certificateNow)
	require.Equal(t, "unknown", out.Conditions[0].Freshness)
	_ = unstructured.SetNestedSlice(u.Object, []any{c, c}, "status", "conditions")
	out = core.CertificateStatus{}
	projectCertificate(&out, u, certificateNow)
	require.Equal(t, "Unknown", out.Conditions[0].Status)
}
func TestCertificateScopeIdentityReadFailuresAndLimits(t *testing.T) {
	for _, mode := range []string{"denied", "missing", "repeated", "wrong namespace", "large", "many", "changed", "replaced", "verify denied"} {
		t.Run(mode, func(t *testing.T) {
			u := certificateObject()
			client := certificateFake(u)
			gets := 0
			client.PrependReactor("get", "certificates", func(kt.Action) (bool, runtime.Object, error) {
				gets++
				x := u.DeepCopy()
				if gets > 1 {
					switch mode {
					case "changed":
						x.SetResourceVersion("2")
					case "replaced":
						x.SetUID("replacement")
					case "verify denied":
						return true, nil, apierrors.NewForbidden(certificateGVR.GroupResource(), "web-tls", fmt.Errorf("PRIVATE-SERVER"))
					}
				}
				return true, x, nil
			})
			client.PrependReactor("list", "ingresses", func(kt.Action) (bool, runtime.Object, error) {
				if mode == "denied" {
					return true, nil, apierrors.NewForbidden(ingressesKind.gvr.GroupResource(), "", fmt.Errorf("PRIVATE-SERVER"))
				}
				if mode == "missing" {
					return true, nil, apierrors.NewNotFound(ingressesKind.gvr.GroupResource(), "")
				}
				list := &unstructured.UnstructuredList{}
				if mode == "repeated" {
					list.SetContinue("repeat")
				}
				count := 1
				if mode == "many" {
					count = 129
				}
				for i := range count {
					x := mk("networking.k8s.io/v1", "Ingress", "blue", fmt.Sprint("entry-", i), fmt.Sprint("ing-", i), map[string]any{"spec": map[string]any{"tls": []any{map[string]any{"secretName": "web-tls"}}}})
					x.SetResourceVersion("1")
					if mode == "wrong namespace" {
						x.SetNamespace("foreign")
					}
					if mode == "large" {
						x.Object["private"] = strings.Repeat("x", 1<<20)
					}
					list.Items = append(list.Items, *x)
				}
				return true, list, nil
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			certificateCatalog(s)
			s.now = func() time.Time { return certificateNow }
			out, err := s.CertificateStatus(t.Context(), certificateRef())
			if mode == "changed" || mode == "replaced" || mode == "verify denied" {
				var e *provider.Error
				require.ErrorAs(t, err, &e)
				require.Equal(t, map[string]provider.ErrorClass{"changed": provider.ClassConflict, "replaced": provider.ClassGone, "verify denied": provider.ClassForbidden}[mode], e.Class)
				require.NotContains(t, e.Message, "PRIVATE")
				return
			}
			require.NoError(t, err)
			require.NotEqual(t, "ready", out.ConsumerState)
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
			require.NotContains(t, string(b), "foreign")
			for _, a := range client.Actions() {
				require.Equal(t, "blue", a.GetNamespace())
				require.Contains(t, []string{"certificates", "ingresses"}, a.GetResource().Resource)
				require.Contains(t, []string{"get", "list"}, a.GetVerb())
			}
		})
	}
}
func TestCertificateMissingCRDCancellationAndScope(t *testing.T) {
	s := newSession("t", "h", certificateFake(certificateObject()), false)
	defer s.Close()
	_, err := s.CertificateStatus(t.Context(), certificateRef())
	require.Error(t, err)
	certificateCatalog(s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.CertificateStatus(ctx, certificateRef())
	require.ErrorIs(t, err, context.Canceled)
	for _, ref := range []core.Ref{{Provider: ProviderID, Target: "other", Kind: certificateRef().Kind, Scope: "blue", Name: "web-tls", UID: "cert-uid"}, {Provider: ProviderID, Target: "t", Kind: certificateRef().Kind, Scope: "", Name: "web-tls", UID: "cert-uid"}} {
		_, err = s.CertificateStatus(t.Context(), ref)
		require.Error(t, err)
	}
}
func TestCertificateActualHTTPNoSecretAndMetadataIssuer(t *testing.T) {
	for _, kind := range []string{"Issuer", "ClusterIssuer"} {
		t.Run(kind, func(t *testing.T) {
			u := certificateObject()
			_ = unstructured.SetNestedField(u.Object, kind, "spec", "issuerRef", "kind")
			paths := []string{}
			page := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				require.Equal(t, "GET", q.Method)
				paths = append(paths, q.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch q.URL.Path {
				case "/apis/cert-manager.io/v1/namespaces/blue/certificates/web-tls":
					_ = json.NewEncoder(w).Encode(u.Object)
				case "/apis/cert-manager.io/v1/namespaces/blue/issuers/public", "/apis/cert-manager.io/v1/clusterissuers/public":
					require.Contains(t, q.Header.Get("Accept"), "PartialObjectMetadata")
					ns := "blue"
					if kind == "ClusterIssuer" {
						ns = ""
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "meta.k8s.io/v1", "kind": "PartialObjectMetadata", "metadata": map[string]any{"name": "public", "namespace": ns, "uid": "issuer-uid"}})
				case "/apis/networking.k8s.io/v1/namespaces/blue/ingresses":
					require.Equal(t, "32", q.URL.Query().Get("limit"))
					page++
					items := []any{}
					meta := map[string]any{}
					if page == 1 {
						meta["continue"] = "next"
					} else {
						require.Equal(t, "next", q.URL.Query().Get("continue"))
						in := mk("networking.k8s.io/v1", "Ingress", "blue", "entry", "ing-uid", map[string]any{"spec": map[string]any{"tls": []any{map[string]any{"secretName": "web-tls"}, map[string]any{"secretName": "web-tls"}}}, "private": "PRIVATE-INGRESS"})
						in.SetResourceVersion("1")
						items = append(items, in.Object)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "IngressList", "metadata": meta, "items": items})
				default:
					t.Errorf("unexpected request %s", q.URL)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			cfg := &rest.Config{Host: server.URL}
			dyn, err := dynamic.NewForConfig(cfg)
			require.NoError(t, err)
			meta, err := metadata.NewForConfig(cfg)
			require.NoError(t, err)
			s := newSession("t", "h", dyn, false)
			defer s.Close()
			certificateCatalog(s)
			s.graphMetadata = meta
			s.now = func() time.Time { return certificateNow }
			out, err := s.CertificateStatus(t.Context(), certificateRef())
			require.NoError(t, err)
			require.Len(t, paths, 5)
			require.Equal(t, "ready", out.IssuerState)
			require.Equal(t, "issuer-uid", out.Issuer.UID)
			require.Len(t, out.Consumers, 1)
			require.Equal(t, "ing-uid", out.Consumers[0].UID)
			require.Equal(t, "ready", out.ConsumerState)
			require.Equal(t, "within_30_days", out.Validity)
			if kind == "ClusterIssuer" {
				require.Empty(t, out.Issuer.Scope)
			} else {
				require.Equal(t, "blue", out.Issuer.Scope)
			}
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
			for _, p := range paths {
				require.NotContains(t, p, "secrets")
			}
		})
	}
}

func TestCertificateIssuerErrorsAndExternalReferencesNeverAcquireSecrets(t *testing.T) {
	for _, mode := range []string{"denied", "missing", "external", "invalid", "wrong group type", "wrong kind type", "metadata unavailable"} {
		t.Run(mode, func(t *testing.T) {
			u := certificateObject()
			if mode == "wrong group type" {
				_ = unstructured.SetNestedField(u.Object, int64(3), "spec", "issuerRef", "group")
			}
			if mode == "wrong kind type" {
				_ = unstructured.SetNestedField(u.Object, true, "spec", "issuerRef", "kind")
			}
			if mode == "external" {
				_ = unstructured.SetNestedField(u.Object, "external.example", "spec", "issuerRef", "group")
			}
			if mode == "invalid" {
				_ = unstructured.SetNestedField(u.Object, "../secrets/private", "spec", "issuerRef", "name")
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				requests++
				require.Contains(t, q.Header.Get("Accept"), "PartialObjectMetadata")
				require.Equal(t, "/apis/cert-manager.io/v1/namespaces/blue/issuers/public", q.URL.Path)
				code := 403
				reason := "Forbidden"
				if mode == "missing" {
					code = 404
					reason = "NotFound"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": reason, "code": code, "message": "PRIVATE-SERVER"})
			}))
			defer server.Close()
			client := certificateFake(u)
			s := newSession("t", "h", client, false)
			defer s.Close()
			certificateCatalog(s)
			var err error
			if mode != "metadata unavailable" {
				s.graphMetadata, err = metadata.NewForConfig(&rest.Config{Host: server.URL})
				require.NoError(t, err)
			}
			out, err := s.CertificateStatus(t.Context(), certificateRef())
			require.NoError(t, err)
			require.Equal(t, map[string]string{"denied": "forbidden", "missing": "not_found", "external": "unsupported", "invalid": "invalid", "metadata unavailable": "unknown", "wrong group type": "invalid", "wrong kind type": "invalid"}[mode], out.IssuerState)
			require.Nil(t, out.Issuer)
			expected := 0
			if mode == "denied" || mode == "missing" {
				expected = 1
			}
			require.Equal(t, expected, requests)
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
			for _, a := range client.Actions() {
				require.NotEqual(t, "secrets", a.GetResource().Resource)
			}
		})
	}
}

func TestCertificateMalformedConsumerDoesNotProveAbsence(t *testing.T) {
	u := certificateObject()
	in := mk("networking.k8s.io/v1", "Ingress", "blue", "entry", "ing-uid", map[string]any{"spec": map[string]any{"tls": []any{map[string]any{"secretName": int64(7)}}}})
	in.SetResourceVersion("1")
	s := newSession("t", "h", certificateFake(u, in), false)
	defer s.Close()
	certificateCatalog(s)
	out, err := s.CertificateStatus(t.Context(), certificateRef())
	require.NoError(t, err)
	require.Equal(t, "unknown", out.ConsumerState)
	require.Empty(t, out.Consumers)
}
