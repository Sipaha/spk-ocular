package kubernetes

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/provider"
)

var svcGVR = schema.GroupVersionResource{Version: "v1", Resource: "services"}

var (
	metaUpdate = metav1.UpdateOptions{}
	metaDelete = metav1.DeleteOptions{}
	metaCreate = metav1.CreateOptions{}
)

func pfClient(objs ...kruntime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", namespacesGVR: "NamespaceList", deployGVR: "DeploymentList", rsGVR: "ReplicaSetList", svcGVR: "ServiceList",
	}, objs...)
}

func ports(ps ...map[string]any) func(map[string]any) {
	return func(o map[string]any) {
		list := make([]any, len(ps))
		for i, p := range ps {
			list[i] = p
		}
		spec := o["spec"].(map[string]any)
		c := spec["containers"].([]any)[0].(map[string]any)
		c["ports"] = list
	}
}

func labelled(l map[string]any) func(map[string]any) {
	return func(o map[string]any) { o["metadata"].(map[string]any)["labels"] = l }
}

func port(name string, n int64, proto string) map[string]any {
	p := map[string]any{"containerPort": n}
	if name != "" {
		p["name"] = name
	}
	if proto != "" {
		p["protocol"] = proto
	}
	return p
}

func service(name, uid string, selector map[string]any, sps ...map[string]any) *unstructured.Unstructured {
	list := make([]any, len(sps))
	for i, p := range sps {
		list[i] = p
	}
	spec := map[string]any{"type": "ClusterIP", "ports": list}
	if selector != nil {
		spec["selector"] = selector
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": uid},
		"spec":     spec,
	}}
}

func svcPort(name string, n int64, target any) map[string]any {
	p := map[string]any{"port": n, "protocol": "TCP"}
	if name != "" {
		p["name"] = name
	}
	if target != nil {
		p["targetPort"] = target
	}
	return p
}

var svcRef = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "services", Name: "web"}

func pfSession(t *testing.T, host string, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	client := pfClient(objs...)
	s := newSession("ctx", "h", client, false)
	t.Cleanup(s.Close)
	s.conn = newConn(&rest.Config{Host: host}, client, "ctx", "ctx", "h")
	return s, client
}

func TestForwardInfoOfPodsServicesAndWorkloads(t *testing.T) {
	web := pod("ns", "p", "uid-1", ports(port("http", 8080, ""), port("dns", 53, "UDP"), port("", 9000, "TCP")))
	d := deployment("web", "d-1", "app")
	d.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["ports"] =
		[]any{port("metrics", 9090, "")}
	s, _ := pfSession(t, "https://cluster.invalid", web, d,
		service("web", "s-1", map[string]any{"app": "web"}, svcPort("https", 443, "tls"), svcPort("", 80, nil), map[string]any{"port": int64(53), "protocol": "UDP"}),
		service("manual", "s-2", nil, svcPort("", 80, nil)),
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Service",
			"metadata": map[string]any{"name": "ext", "namespace": "ns", "uid": "s-3"},
			"spec":     map[string]any{"type": "ExternalName", "externalName": "example.com"}}})
	ctx := context.Background()

	info, err := s.ForwardInfo(ctx, podRef1)
	require.NoError(t, err)
	assert.True(t, info.AnyPort)
	require.Len(t, info.Ports, 3)
	assert.Equal(t, core.ForwardPort{Port: 8080, Name: "http", Protocol: "TCP", Note: "container app", Scheme: "http", Supported: true}, info.Ports[0])
	assert.False(t, info.Ports[1].Supported)
	assert.Contains(t, info.Ports[1].Reason, "UDP")

	info, err = s.ForwardInfo(ctx, svcRef)
	require.NoError(t, err)
	assert.False(t, info.AnyPort)
	assert.Empty(t, info.Unsupported)
	require.Len(t, info.Ports, 3)
	assert.Equal(t, "https", info.Ports[0].Scheme)
	assert.Equal(t, "→ tls", info.Ports[0].Note)
	assert.Empty(t, info.Ports[1].Note, "an empty targetPort is the port itself")
	assert.False(t, info.Ports[2].Supported)

	info, err = s.ForwardInfo(ctx, core.Ref{Kind: "services", Scope: "ns", Name: "manual"})
	require.NoError(t, err)
	assert.Contains(t, info.Unsupported, "without a selector")
	info, err = s.ForwardInfo(ctx, core.Ref{Kind: "services", Scope: "ns", Name: "ext"})
	require.NoError(t, err)
	assert.Contains(t, info.Unsupported, "ExternalName")
	_, err = s.PrepareForward(ctx, core.Ref{Kind: "services", Scope: "ns", Name: "ext"}, provider.ForwardRequest{Port: 80})
	assertClass(t, err, provider.ClassUnsupported)

	info, err = s.ForwardInfo(ctx, core.Ref{Kind: "apps/deployments", Scope: "ns", Name: "web"})
	require.NoError(t, err)
	require.Len(t, info.Ports, 1)
	assert.Equal(t, 9090, info.Ports[0].Port)

	_, err = s.ForwardInfo(ctx, core.Ref{Kind: "configmaps", Scope: "ns", Name: "x"})
	assertClass(t, err, provider.ClassUnsupported)
}

func assertClass(t *testing.T, err error, class provider.ErrorClass) {
	t.Helper()
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, class, pe.Class, pe.Message)
}

func TestPrepareForwardChecksThePort(t *testing.T) {
	s, _ := pfSession(t, "https://cluster.invalid",
		pod("ns", "p", "uid-1", ports(port("dns", 53, "UDP"))),
		service("web", "s-1", map[string]any{"app": "web"}, svcPort("http", 80, "http")))
	ctx := context.Background()
	_, err := s.PrepareForward(ctx, svcRef, provider.ForwardRequest{Port: 81})
	assertClass(t, err, provider.ClassInvalid)
	_, err = s.PrepareForward(ctx, podRef1, provider.ForwardRequest{Port: 53})
	assertClass(t, err, provider.ClassUnsupported)
	h, err := s.PrepareForward(ctx, podRef1, provider.ForwardRequest{Port: 5432})
	require.NoError(t, err, "any port of a pod")
	assert.Equal(t, 5432, h.Describe().Port)
	_, err = s.PrepareForward(ctx, core.Ref{Kind: "pods", Scope: "ns", Name: "p", UID: "other"}, provider.ForwardRequest{Port: 80})
	assertClass(t, err, provider.ClassGone)
}

// A number declared for TCP and UDP (DNS) forwards its TCP entry, in
// either order; UDP alone stays refused.
func TestATCPPortSharingItsNumberWithUDPIsAccepted(t *testing.T) {
	tcp, udp := svcPort("dns-tcp", 53, int64(53)), svcPort("dns", 53, int64(53))
	udp["protocol"] = "UDP"
	udpOnly := svcPort("syslog", 514, int64(514))
	udpOnly["protocol"] = "UDP"
	ctx := context.Background()
	for _, order := range []string{"tcp first", "udp first"} {
		t.Run(order, func(t *testing.T) {
			svcPorts := []map[string]any{tcp, udp, udpOnly}
			podPorts := []map[string]any{port("dns-tcp", 53, "TCP"), port("dns", 53, "UDP")}
			if order == "udp first" {
				svcPorts = []map[string]any{udp, tcp, udpOnly}
				podPorts = []map[string]any{port("dns", 53, "UDP"), port("dns-tcp", 53, "TCP")}
			}
			s, _ := pfSession(t, "https://cluster.invalid",
				pod("ns", "p", "uid-1", ports(podPorts...)), service("web", "s-1", map[string]any{"app": "web"}, svcPorts...))
			h, err := s.PrepareForward(ctx, svcRef, provider.ForwardRequest{Port: 53})
			require.NoError(t, err, "service")
			assert.Equal(t, 53, h.Describe().Port)
			_, err = s.PrepareForward(ctx, podRef1, provider.ForwardRequest{Port: 53})
			require.NoError(t, err, "pod")
			_, err = s.PrepareForward(ctx, svcRef, provider.ForwardRequest{Port: 514})
			assertClass(t, err, provider.ClassUnsupported)
		})
	}
}

// svcPods: web-a (not ready), web-b (ready), a pod of another app and a
// pod that is being deleted.
func svcPods(mut ...func(map[string]any)) []kruntime.Object {
	sel := labelled(map[string]any{"app": "web"})
	deleting := func(o map[string]any) {
		o["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	}
	ps := []func(map[string]any){sel, ports(port("http", 8080, ""))}
	ps = append(ps, mut...)
	return []kruntime.Object{
		pod("ns", "web-a", "a", append(ps, notReady)...),
		pod("ns", "web-b", "b", ps...),
		pod("ns", "web-gone", "g", append(ps, deleting)...),
		pod("ns", "db", "d", labelled(map[string]any{"app": "db"}), ports(port("http", 5432, ""))),
	}
}

func TestServiceTargetPortResolution(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		target any
		mut    []func(map[string]any)
		port   int
		pod    string
		err    string
	}{
		"empty is the service port": {target: nil, port: 80, pod: "web-b"},
		"zero is the service port":  {target: int64(0), port: 80, pod: "web-b"},
		"a number as it is":         {target: int64(9999), port: 9999, pod: "web-b"},
		"a name on the chosen pod":  {target: "http", port: 8080, pod: "web-b"},
		"a name nobody has":         {target: "grpc", err: `no TCP port named "grpc"`},
		"an ambiguous name": {target: "http", mut: []func(map[string]any){func(o map[string]any) {
			c := o["spec"].(map[string]any)["containers"].([]any)
			o["spec"].(map[string]any)["containers"] = append(c, map[string]any{"name": "side", "ports": []any{port("http", 8081, "")}})
		}}, err: "ambiguous"},
	} {
		t.Run(name, func(t *testing.T) {
			objs := append(svcPods(tc.mut...), service("web", "s-1", map[string]any{"app": "web"}, svcPort("", 80, tc.target)))
			s, _ := pfSession(t, "https://cluster.invalid", objs...)
			h, err := s.PrepareForward(ctx, svcRef, provider.ForwardRequest{Port: 80})
			require.NoError(t, err)
			chosen, p, err := h.(*forwardHandle).choose(ctx)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.pod, chosen.GetName(), "ready and not deleting")
			assert.Equal(t, tc.port, p)
		})
	}
}

func TestTheServiceIsReadAgainOnEveryChoice(t *testing.T) {
	ctx := context.Background()
	objs := append(svcPods(), service("web", "s-1", map[string]any{"app": "web"}, svcPort("", 80, "http")))
	s, client := pfSession(t, "https://cluster.invalid", objs...)
	h, err := s.PrepareForward(ctx, svcRef, provider.ForwardRequest{Port: 80})
	require.NoError(t, err)
	fh := h.(*forwardHandle)

	// the same Service edited: the selector now picks db, the target is a number
	edited := service("web", "s-1", map[string]any{"app": "db"}, svcPort("", 80, int64(5432)))
	_, err = client.Resource(svcGVR).Namespace("ns").Update(ctx, edited, metaUpdate)
	require.NoError(t, err)
	chosen, p, err := fh.choose(ctx)
	require.NoError(t, err)
	assert.Equal(t, "db", chosen.GetName())
	assert.Equal(t, 5432, p)

	// the port removed from the Service
	edited = service("web", "s-1", map[string]any{"app": "db"}, svcPort("", 81, nil))
	_, err = client.Resource(svcGVR).Namespace("ns").Update(ctx, edited, metaUpdate)
	require.NoError(t, err)
	_, _, err = fh.choose(ctx)
	assertClass(t, err, provider.ClassGone)

	// replaced by a new Service with the same name: never followed
	require.NoError(t, client.Resource(svcGVR).Namespace("ns").Delete(ctx, "web", metaDelete))
	_, err = client.Resource(svcGVR).Namespace("ns").Create(ctx, service("web", "s-2", map[string]any{"app": "web"}, svcPort("", 80, nil)), metaCreate)
	require.NoError(t, err)
	_, _, err = fh.choose(ctx)
	assertClass(t, err, provider.ClassGone)
	assert.Contains(t, err.Error(), "replaced")
}

func TestAPodTargetIsPinned(t *testing.T) {
	ctx := context.Background()
	s, client := pfSession(t, "https://cluster.invalid", pod("ns", "p", "uid-1"))
	h, err := s.PrepareForward(ctx, podRef1, provider.ForwardRequest{Port: 80})
	require.NoError(t, err)
	fh := h.(*forwardHandle)
	pod1, _, err := fh.choose(ctx)
	require.NoError(t, err)
	assert.Equal(t, "p", pod1.GetName())

	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(ctx, "p", metaDelete))
	_, _, err = fh.choose(ctx)
	assertClass(t, err, provider.ClassGone)
	_, err = client.Resource(podGVR).Namespace("ns").Create(ctx, pod("ns", "p", "uid-2"), metaCreate)
	require.NoError(t, err)
	_, _, err = fh.choose(ctx)
	assertClass(t, err, provider.ClassGone)
	assert.Contains(t, err.Error(), "replaced")
}

func TestAWorkloadForwardsToItsReadyNewestPod(t *testing.T) {
	ctx := context.Background()
	d := deployment("web", "d-1", "app")
	rs := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet",
		"metadata": map[string]any{"name": "web-1", "namespace": "ns", "uid": "rs-1", "labels": map[string]any{"app": "web"},
			"ownerReferences": []any{ctrlRef("Deployment", "web", "d-1")}},
		"spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}}}}}
	s, _ := pfSession(t, "https://cluster.invalid", d, rs,
		webPod("web-old", "o", "rs-1", 60), webPod("web-new", "n", "rs-1", 3600), webPod("web-unready", "u", "rs-1", 7200, notReady))
	h, err := s.PrepareForward(ctx, core.Ref{Kind: "apps/deployments", Scope: "ns", Name: "web"}, provider.ForwardRequest{Port: 0})
	assertClass(t, err, provider.ClassInvalid)
	require.Nil(t, h)
	d.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["ports"] = []any{port("", 80, "")}
	s, _ = pfSession(t, "https://cluster.invalid", d, rs,
		webPod("web-old", "o", "rs-1", 60), webPod("web-new", "n", "rs-1", 3600), webPod("web-unready", "u", "rs-1", 7200, notReady))
	h, err = s.PrepareForward(ctx, core.Ref{Kind: "apps/deployments", Scope: "ns", Name: "web"}, provider.ForwardRequest{Port: 80})
	require.NoError(t, err)
	chosen, p, err := h.(*forwardHandle).choose(ctx)
	require.NoError(t, err)
	assert.Equal(t, "web-new", chosen.GetName())
	assert.Equal(t, 80, p)
}

// startTunnel runs a real tunnel (the manager) over the handle.
func startTunnel(t *testing.T, s *session, ref core.Ref, port int) (*forwards.Manager, forwards.Info) {
	t.Helper()
	h, err := s.PrepareForward(context.Background(), ref, provider.ForwardRequest{Port: port})
	require.NoError(t, err)
	m := forwards.NewManager(forwards.Options{})
	t.Cleanup(m.Close)
	in, err := m.Start(context.Background(), h, forwards.StartRequest{})
	require.NoError(t, err)
	return m, in
}

func echoOver(t *testing.T, addr, msg string) error {
	t.Helper()
	c, err := net.DialTimeout("tcp4", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func TestTunnelOverBothPaths(t *testing.T) {
	for _, path := range []string{"ws", "spdy"} {
		t.Run(path, func(t *testing.T) {
			srv := newPFServer(t)
			srv.noWS = path == "spdy"
			srv.backend(8080, echoBackend(t))
			s, _ := pfSession(t, srv.srv.URL, append(svcPods(), service("web", "s-1", map[string]any{"app": "web"}, svcPort("http", 80, "http")))...)
			m, in := startTunnel(t, s, svcRef, 80)
			assert.Equal(t, "web-b:8080", in.Upstream)

			// many connections, in parallel, over one connection to the pod
			var wg sync.WaitGroup
			errs := make(chan error, 20)
			for range 20 {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- echoOver(t, in.Addresses[0], strings.Repeat("x", 100<<10)) }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			srv.mu.Lock()
			assert.Equal(t, []string{path}, srv.methods, "one negotiated connection")
			assert.Zero(t, srv.dupIDs, "request ids are unique per connection")
			srv.mu.Unlock()
			assert.EqualValues(t, 0, m.List()[0].Failed)
		})
	}
}

func TestAPortNobodyListensOnFailsOnlyThatConnection(t *testing.T) {
	srv := newPFServer(t)
	srv.backend(8080, echoBackend(t))
	s, _ := pfSession(t, srv.srv.URL, pod("ns", "p", "uid-1"))
	m, good := startTunnel(t, s, podRef1, 8080)
	h, err := s.PrepareForward(context.Background(), podRef1, provider.ForwardRequest{Port: 9})
	require.NoError(t, err)
	bad, err := m.Start(context.Background(), h, forwards.StartRequest{})
	require.NoError(t, err)

	live, err := net.DialTimeout("tcp4", good.Addresses[0], 5*time.Second)
	require.NoError(t, err)
	defer live.Close()
	_ = live.SetDeadline(time.Now().Add(10 * time.Second))
	_, err = live.Write([]byte("before "))
	require.NoError(t, err)

	c, err := net.DialTimeout("tcp4", bad.Addresses[0], 5*time.Second)
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, err = c.Read(make([]byte, 1))
	require.Error(t, err, "the refused connection ends")
	_ = c.Close()
	require.Eventually(t, func() bool {
		for _, f := range m.List() {
			if f.ID == bad.ID && f.Failed == 1 && f.LastError != nil {
				return strings.Contains(f.LastError.Message, "connection refused")
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	for _, f := range m.List() {
		if f.ID == bad.ID {
			assert.Equal(t, forwards.StateReady, f.State, "the pod connection stays")
		}
	}
	// its neighbour on another tunnel and a second one on the same are fine
	_, err = live.Write([]byte("after"))
	require.NoError(t, err)
	buf := make([]byte, len("before after"))
	_, err = io.ReadFull(live, buf)
	require.NoError(t, err)
	assert.Equal(t, "before after", string(buf))
	require.NoError(t, echoOver(t, good.Addresses[0], "again"))
}

// A server that accepts TCP and never answers the upgrade (or TLS) keeps
// a connect waiting only until it is cancelled or the negotiation deadline
// passes; nothing is left behind.
func TestNegotiationEndsOnCancelAndDeadline(t *testing.T) {
	tlsStall, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer tlsStall.Close()
	var heldMu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := tlsStall.Accept()
			if err != nil {
				return
			}
			heldMu.Lock()
			held = append(held, c)
			heldMu.Unlock()
		}
	}()
	defer func() {
		heldMu.Lock()
		for _, c := range held {
			_ = c.Close()
		}
		heldMu.Unlock()
	}()
	release := make(chan struct{})
	defer close(release)
	stall := func(refuseWS bool) *httptest.Server {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if refuseWS && r.Method == http.MethodGet {
				http.Error(w, "no websockets", http.StatusBadRequest)
				return
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	hosts := map[string]string{"tls": "https://" + tlsStall.Addr().String(), "ws headers": stall(false).URL, "spdy headers": stall(true).URL}
	for name, host := range hosts {
		t.Run(name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			client := pfClient(pod("ns", "p", "uid-1"))
			c := newConn(&rest.Config{Host: host, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}, client, "t", "t", "h")
			d := &forwardDialer{c: c, deadAfter: pfDeadAfter, ws: true}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, _, err := d.dial(ctx, "ns", "p"); done <- err }()
			select {
			case err := <-done:
				t.Fatalf("returned before cancel: %v", err)
			case <-time.After(time.Second):
			}
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not end the negotiation")
			}

			// the deadline, with no cancel
			ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel2()
			begin := time.Now()
			_, _, err := d.dial(ctx2, "ns", "p")
			require.Error(t, err)
			assert.Less(t, time.Since(begin), 5*time.Second)
			require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+3 }, 10*time.Second, 50*time.Millisecond,
				"goroutines left: %d (before %d)", runtime.NumGoroutine(), before)
		})
	}
}

// The negotiation deadline ends with the negotiation: an established
// connection outlives it (on both paths).
func TestAnEstablishedConnectionOutlivesTheNegotiationDeadline(t *testing.T) {
	for _, path := range []string{"ws", "spdy"} {
		t.Run(path, func(t *testing.T) {
			srv := newPFServer(t)
			srv.noWS = path == "spdy"
			srv.backend(80, echoBackend(t))
			client := pfClient(pod("ns", "p", "uid-1"))
			c := newConn(&rest.Config{Host: srv.srv.URL}, client, "t", "t", "h")
			d := &forwardDialer{c: c, deadAfter: pfDeadAfter, ws: true}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			conn, via, err := d.dial(ctx, "ns", "p")
			require.NoError(t, err)
			assert.Equal(t, map[string]string{"ws": "websocket", "spdy": "spdy"}[path], via)
			cancel()
			u := newPFUpstream(conn, "p:80", 80)
			defer u.Close()
			time.Sleep(500 * time.Millisecond) // past the deadline
			st, err := u.Open(context.Background())
			require.NoError(t, err)
			_, err = st.Write([]byte("still here"))
			require.NoError(t, err)
			require.NoError(t, st.CloseWrite())
			got, err := io.ReadAll(st)
			require.NoError(t, err)
			assert.Equal(t, "still here", string(got))
			require.NoError(t, st.Result())
			require.NoError(t, st.Close())
			select {
			case <-u.Done():
				t.Fatal("the upstream died")
			default:
			}
		})
	}
}

// A peer that stops answering (a half-open connection) is noticed by the
// watchdog: the upstream dies and says why.
func TestASilentPeerIsDeclaredDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "no websockets", http.StatusBadRequest)
			return
		}
		hj, _ := w.(http.Hijacker)
		c, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: SPDY/3.1\r\nX-Stream-Protocol-Version: portforward.k8s.io\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, bufio.NewReader(c)) // reads pings, never answers
	}))
	defer srv.Close()
	client := pfClient(pod("ns", "p", "uid-1"))
	c := newConn(&rest.Config{Host: srv.URL}, client, "t", "t", "h")
	d := &forwardDialer{c: c, deadAfter: 400 * time.Millisecond, ws: true}
	conn, _, err := d.dial(context.Background(), "ns", "p")
	require.NoError(t, err)
	u := newPFUpstream(conn, "p:80", 80)
	defer u.Close()
	select {
	case <-u.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a silent peer was not noticed")
	}
	require.Error(t, u.Err())
	assert.Contains(t, u.Err().Error(), "lost")
}

// kubelet keeps the connection of a deleted pod and fails its streams: the
// failure makes the upstream check its pod (one GET), retire, and the next
// connection goes to another pod of the Service.
func TestAServiceTunnelMovesOnWhenItsPodIsDeleted(t *testing.T) {
	srv := newPFServer(t)
	srv.backend(8080, echoBackend(t))
	s, client := pfSession(t, srv.srv.URL, append(svcPods(), service("web", "s-1", map[string]any{"app": "web"}, svcPort("http", 80, "http")))...)
	m, in := startTunnel(t, s, svcRef, 80)
	require.Equal(t, "web-b:8080", in.Upstream)
	require.NoError(t, echoOver(t, in.Addresses[0], "to b"))

	srv.mu.Lock()
	srv.dead["web-b"] = true
	srv.mu.Unlock()
	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(context.Background(), "web-b", metaDelete))

	require.Error(t, echoOver(t, in.Addresses[0], "lost"), "the connection that met the dead pod fails")
	require.Eventually(t, func() bool { return m.List()[0].State == forwards.StateIdle }, 10*time.Second, 20*time.Millisecond,
		"the upstream retires: %+v", m.List()[0])
	assert.Contains(t, m.List()[0].LastError.Message, "web-b")
	require.NoError(t, echoOver(t, in.Addresses[0], "to a"))
	assert.Equal(t, "web-a:8080", m.List()[0].Upstream)
	assert.Equal(t, []string{"web-b", "web-a"}, srv.forwarded())
}

// A refused port on a healthy pod is that connection's error only: the
// check finds the pod fine and the upstream stays.
func TestARefusedPortKeepsTheUpstream(t *testing.T) {
	srv := newPFServer(t)
	s, _ := pfSession(t, srv.srv.URL, pod("ns", "p", "uid-1"))
	m, in := startTunnel(t, s, podRef1, 81)
	require.Error(t, echoOver(t, in.Addresses[0], "x"))
	require.Eventually(t, func() bool { return m.List()[0].Failed == 1 }, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, forwards.StateReady, m.List()[0].State)
	assert.Len(t, srv.forwarded(), 1)
}

// refusedUpstream is an upstream to pod p whose port 1234 nobody listens
// on: every connection ends with a report, which triggers the pod check.
func refusedUpstream(t *testing.T, srv *pfServer) *pfUpstream {
	t.Helper()
	c := newConn(&rest.Config{Host: srv.srv.URL}, pfClient(), "t", "t", "h")
	d := &forwardDialer{c: c, deadAfter: pfDeadAfter, ws: true}
	cn, _, err := d.dial(context.Background(), "ns", "p")
	require.NoError(t, err)
	u := newPFUpstream(cn, "p:1234", 1234)
	t.Cleanup(u.Close)
	return u
}

// Closing an upstream (a tunnel's Stop) cancels the pod check a failed
// connection started: Result returns at once, not after the GET.
func TestClosingTheUpstreamCancelsTheFailureCheck(t *testing.T) {
	u := refusedUpstream(t, newPFServer(t))
	entered := make(chan struct{})
	u.alive = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	st, err := u.Open(context.Background())
	require.NoError(t, err)
	defer st.Close()
	_, _ = io.ReadAll(st)
	done := make(chan error, 1)
	go func() { done <- st.Result() }()
	<-entered
	u.Close()
	select {
	case err := <-done:
		assert.ErrorContains(t, err, "connection refused")
	case <-time.After(2 * time.Second):
		t.Fatal("Result waits for the pod check of a closed upstream")
	}
}

// Failures at once share one pod check: a burst of refused connections
// is not a burst of GETs.
func TestConcurrentFailuresShareOnePodCheck(t *testing.T) {
	u := refusedUpstream(t, newPFServer(t))
	var calls atomic.Int32
	release := make(chan struct{})
	u.alive = func(context.Context) error {
		calls.Add(1)
		<-release
		return nil
	}
	var wg sync.WaitGroup
	var returned atomic.Int32
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := u.Open(context.Background())
			if !assert.NoError(t, err) {
				return
			}
			defer st.Close()
			_, _ = io.ReadAll(st)
			assert.Error(t, st.Result())
			returned.Add(1)
		}()
	}
	// One failure checks (and blocks); the others, failing meanwhile, leave
	// it to that check and return at once.
	require.Eventually(t, func() bool { return returned.Load() == 4 }, 5*time.Second, 10*time.Millisecond)
	assert.EqualValues(t, 1, calls.Load())
	close(release)
	wg.Wait()
	assert.EqualValues(t, 1, calls.Load())
}

// An error stream that never answers ends Result after resultWait with a
// timeout, not a silent success.
func TestASilentErrorStreamIsATimeout(t *testing.T) {
	old := resultWait
	resultWait = 200 * time.Millisecond
	defer func() { resultWait = old }()
	srv := newPFServer(t)
	srv.backend(80, echoBackend(t))
	srv.holdErrs = make(chan struct{})
	defer close(srv.holdErrs)
	u := refusedUpstream(t, srv)
	u.port = 80
	st, err := u.Open(context.Background())
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, st.CloseWrite())
	_, _ = io.ReadAll(st)
	begin := time.Now()
	err = st.Result()
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassUnavailable, pe.Class)
	assert.Contains(t, pe.Message, "no report")
	assert.Less(t, time.Since(begin), 2*time.Second)
}
