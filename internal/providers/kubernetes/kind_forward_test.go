package kubernetes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/provider"
)

const kindNS = "ocular-demo"

// httpGet fetches / through a tunnel address (never through a proxy).
func httpGet(addr string) (string, error) {
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := c.Get("http://" + addr + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return string(b), fmt.Errorf("status %d", resp.StatusCode)
	}
	return string(b), err
}

func kindRef(t *testing.T, s *session, def *kindDef, name string) core.Ref {
	t.Helper()
	u, err := s.dyn.Resource(def.gvr).Namespace(kindNS).Get(context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err)
	return s.ref(def, kindNS, name, string(u.GetUID()))
}

func kindTunnel(t *testing.T, m *forwards.Manager, s *session, ref core.Ref, port int, spdyOnly bool) forwards.Info {
	t.Helper()
	h, err := s.PrepareForward(context.Background(), ref, provider.ForwardRequest{Port: port})
	require.NoError(t, err)
	if spdyOnly {
		h.(*forwardHandle).dialer.ws = false
	}
	in, err := m.Start(context.Background(), h, forwards.StartRequest{})
	require.NoError(t, err)
	return in
}

func tunnelInfo(m *forwards.Manager, id string) forwards.Info {
	for _, f := range m.List() {
		if f.ID == id {
			return f
		}
	}
	return forwards.Info{}
}

func TestKindForwardHTTPToPodsAndServicesOverBothPaths(t *testing.T) {
	s := kindSession(t)
	targets := map[string]struct {
		ref  core.Ref
		port int
	}{
		"pod":              {kindWebPod(t, s), 80},
		"service":          {kindRef(t, s, servicesKind, "web"), 80},
		"named targetPort": {kindRef(t, s, servicesKind, "web-named"), 8080},
		"default target":   {kindRef(t, s, servicesKind, "web-default"), 80},
		"deployment":       {kindRef(t, s, deploymentsKind, "web"), 80},
	}
	for _, path := range []string{"websocket", "spdy"} {
		for name, tc := range targets {
			t.Run(path+"/"+name, func(t *testing.T) {
				// the path really taken
				h, err := s.PrepareForward(context.Background(), tc.ref, provider.ForwardRequest{Port: tc.port})
				require.NoError(t, err)
				h.(*forwardHandle).dialer.ws = path == "websocket"
				u, err := h.Connect(context.Background())
				require.NoError(t, err)
				assert.Equal(t, path, u.(*pfUpstream).via)
				assert.True(t, strings.HasSuffix(u.Label(), ":80"), "resolved to nginx's port: %s", u.Label())
				u.Close()

				m := forwards.NewManager(forwards.Options{})
				defer m.Close()
				in := kindTunnel(t, m, s, tc.ref, tc.port, path == "spdy")
				for range 3 {
					body, err := httpGet(in.Addresses[0])
					require.NoError(t, err)
					assert.Contains(t, body, "Welcome to nginx")
				}
			})
		}
	}
}

func TestKindForwardServiceMovesToANewPodWhenItsPodIsDeleted(t *testing.T) {
	s := kindSession(t)
	m := forwards.NewManager(forwards.Options{})
	defer m.Close()
	in := kindTunnel(t, m, s, kindRef(t, s, servicesKind, "web"), 80, false)
	_, err := httpGet(in.Addresses[0])
	require.NoError(t, err)
	first := tunnelInfo(m, in.ID).Upstream
	victim, _, _ := strings.Cut(first, ":")
	zero := int64(0)
	require.NoError(t, s.dyn.Resource(podsKind.gvr).Namespace(kindNS).Delete(context.Background(), victim, metav1.DeleteOptions{GracePeriodSeconds: &zero}))

	// connections that meet the dead pod fail; soon one lands elsewhere
	require.Eventually(t, func() bool {
		_, err := httpGet(in.Addresses[0])
		up := tunnelInfo(m, in.ID).Upstream
		return err == nil && up != "" && !strings.HasPrefix(up, victim+":")
	}, 90*time.Second, 500*time.Millisecond, "still %s", tunnelInfo(m, in.ID).Upstream)
	t.Logf("moved from %s to %s", first, tunnelInfo(m, in.ID).Upstream)
}

// kindScratchPod creates a throwaway nginx pod and waits until it runs.
func kindScratchPod(t *testing.T, s *session, name string) *unstructured.Unstructured {
	t.Helper()
	ctx := context.Background()
	pods := s.dyn.Resource(podsKind.gvr).Namespace(kindNS)
	zero := int64(0)
	_ = pods.Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
	require.Eventually(t, func() bool {
		_, err := pods.Get(ctx, name, metav1.GetOptions{})
		return err != nil
	}, 60*time.Second, 200*time.Millisecond)
	p := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "labels": map[string]any{"app": name}},
		"spec": map[string]any{"terminationGracePeriodSeconds": int64(0),
			"containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine", "ports": []any{map[string]any{"containerPort": int64(80)}}}}},
	}}
	_, err := pods.Create(ctx, p, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pods.Delete(context.Background(), name, metav1.DeleteOptions{GracePeriodSeconds: &zero}) })
	var got *unstructured.Unstructured
	require.Eventually(t, func() bool {
		got, err = pods.Get(ctx, name, metav1.GetOptions{})
		return err == nil && runnable(got) && podReady(got)
	}, 120*time.Second, 300*time.Millisecond)
	return got
}

func TestKindForwardAReplacedPodIsNotFollowed(t *testing.T) {
	s := kindSession(t)
	kindScratchPod(t, s, "pf-victim")
	ref := kindRef(t, s, podsKind, "pf-victim")
	m := forwards.NewManager(forwards.Options{})
	defer m.Close()
	in := kindTunnel(t, m, s, ref, 80, false)
	_, err := httpGet(in.Addresses[0])
	require.NoError(t, err)

	kindScratchPod(t, s, "pf-victim") // deleted and created again: a new UID
	require.Eventually(t, func() bool {
		_, err := httpGet(in.Addresses[0])
		f := tunnelInfo(m, in.ID)
		return err != nil && f.State == forwards.StateError && f.LastError != nil && strings.Contains(f.LastError.Message, "replaced")
	}, 60*time.Second, 500*time.Millisecond, "%+v", tunnelInfo(m, in.ID))
}

func TestKindForwardAReplacedServiceIsNotFollowed(t *testing.T) {
	s := kindSession(t)
	ctx := context.Background()
	svcs := s.dyn.Resource(servicesKind.gvr).Namespace(kindNS)
	mk := func() {
		_ = svcs.Delete(ctx, "pf-svc", metav1.DeleteOptions{})
		require.Eventually(t, func() bool { _, err := svcs.Get(ctx, "pf-svc", metav1.GetOptions{}); return err != nil }, 30*time.Second, 100*time.Millisecond)
		_, err := svcs.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "pf-svc"},
			"spec": map[string]any{"selector": map[string]any{"app": "pf-backend"}, "ports": []any{map[string]any{"port": int64(80)}}},
		}}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	mk()
	t.Cleanup(func() { _ = svcs.Delete(context.Background(), "pf-svc", metav1.DeleteOptions{}) })
	backend := kindScratchPod(t, s, "pf-backend")
	_ = backend
	m := forwards.NewManager(forwards.Options{})
	defer m.Close()
	in := kindTunnel(t, m, s, kindRef(t, s, servicesKind, "pf-svc"), 80, false)
	_, err := httpGet(in.Addresses[0])
	require.NoError(t, err)

	// the Service is replaced; a live upstream carries on until its pod
	// goes, then a new choice meets the replacement and refuses it
	mk()
	kindScratchPod(t, s, "pf-backend")
	require.Eventually(t, func() bool {
		_, err := httpGet(in.Addresses[0])
		f := tunnelInfo(m, in.ID)
		return err != nil && f.LastError != nil && strings.Contains(f.LastError.Message, "replaced")
	}, 90*time.Second, 500*time.Millisecond, "%+v", tunnelInfo(m, in.ID))
}

func TestKindForwardToAPortNobodyListensOnFailsThatConnectionOnly(t *testing.T) {
	s := kindSession(t)
	ref := kindWebPod(t, s)
	m := forwards.NewManager(forwards.Options{})
	defer m.Close()
	good := kindTunnel(t, m, s, ref, 80, false)
	bad := kindTunnel(t, m, s, ref, 81, false)
	_, err := httpGet(bad.Addresses[0])
	require.Error(t, err)
	require.Eventually(t, func() bool { f := tunnelInfo(m, bad.ID); return f.Failed >= 1 && f.LastError != nil }, 15*time.Second, 100*time.Millisecond)
	f := tunnelInfo(m, bad.ID)
	t.Logf("kubelet says: %s", f.LastError.Message)
	assert.Contains(t, f.LastError.Message, "nothing listens on port 81")
	assert.Equal(t, forwards.StateReady, f.State, "the pod connection stays")
	body, err := httpGet(good.Addresses[0])
	require.NoError(t, err)
	assert.Contains(t, body, "nginx")
}

func TestKindForwardWithoutPermissionIsForbidden(t *testing.T) {
	rbac := os.Getenv("OCULAR_KIND_RBAC_DIR")
	if rbac == "" {
		t.Skip("OCULAR_KIND_RBAC_DIR not set (make test-kind)")
	}
	cfg, _ := filepath.Abs(filepath.Join(rbac, "viewer.kubeconfig"))
	p := NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return cfg
		}
		return ""
	}, t.TempDir())
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	sess, err := p.Open(context.Background(), d.Targets[0].ID)
	require.NoError(t, err)
	defer sess.Close()
	s := sess.(*session)
	h, err := s.PrepareForward(context.Background(), kindWebPod(t, s), provider.ForwardRequest{Port: 80})
	require.NoError(t, err, "reading the pod is allowed")
	m := forwards.NewManager(forwards.Options{})
	defer m.Close()
	_, err = m.Start(context.Background(), h, forwards.StartRequest{})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class, pe.Message)
	assert.Contains(t, pe.Message, "pods/portforward")
	assert.Equal(t, 0, m.Len())
}

// An idle tunnel stays connected past the liveness watchdog: the pod's
// side answers the pings (a real cluster, both paths).
func TestKindForwardAnIdleTunnelStaysConnected(t *testing.T) {
	if testing.Short() {
		t.Skip("waits past the watchdog")
	}
	s := kindSession(t)
	ref := kindWebPod(t, s)
	for _, path := range []string{"websocket", "spdy"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			m := forwards.NewManager(forwards.Options{})
			defer m.Close()
			in := kindTunnel(t, m, s, ref, 80, path == "spdy")
			time.Sleep(pfDeadAfter + 5*time.Second)
			f := tunnelInfo(m, in.ID)
			require.Equal(t, forwards.StateReady, f.State, "%+v", f)
			assert.Nil(t, f.LastError)
			_, err := httpGet(in.Addresses[0])
			require.NoError(t, err)
		})
	}
}
