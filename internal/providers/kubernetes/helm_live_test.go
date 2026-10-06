package kubernetes

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ocularhelm "github.com/spk/spk-ocular/internal/helm"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type helmLiveFixture struct {
	s      *session
	client kubernetes.Interface
	ns     string
}

func helmLive(t *testing.T) helmLiveFixture {
	t.Helper()
	if os.Getenv("OCULAR_HELM_LIVE") != "1" {
		t.Skip("run make test-helm-live")
	}
	path := os.Getenv("OCULAR_KIND_KUBECONFIG")
	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "kind-ocular-dev", config.CurrentContext)
	require.Len(t, config.Contexts, 1)
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*config, "kind-ocular-dev", nil, nil).ClientConfig()
	require.NoError(t, err)
	u, err := url.Parse(cfg.Host)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, u.Hostname())
	client, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	nodes, err := client.CoreV1().Nodes().List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, nodes.Items)
	for _, node := range nodes.Items {
		require.True(t, strings.HasPrefix(node.Spec.ProviderID, "kind://docker/ocular-dev/"))
	}
	ns := os.Getenv("OCULAR_HELM_LIVE_NAMESPACE")
	require.True(t, strings.HasPrefix(ns, "ocular-helm-live-"))
	namespace, err := client.CoreV1().Namespaces().Get(t.Context(), ns, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, ns, namespace.Labels["ocular.helm.owner"])
	p := NewWith(func(key string) string {
		if key == "KUBECONFIG" {
			return path
		}
		return ""
	}, t.TempDir())
	targets, err := p.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, targets.Targets, 1)
	sess, err := p.Open(t.Context(), targets.Targets[0].ID)
	require.NoError(t, err)
	t.Cleanup(sess.Close)
	return helmLiveFixture{sess.(*session), client, ns}
}
func (f helmLiveFixture) factory(storage ocularhelm.Storage) ocularhelm.Factory {
	return func(ctx context.Context, ns string) (*action.Configuration, error) {
		return f.s.HelmConfiguration(ctx, ns, storage)
	}
}
func liveDetail(t *testing.T, f ocularhelm.Factory, ns, name string) ocularhelm.Detail {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	a, err := f(ctx, ns)
	require.NoError(t, err)
	d, err := ocularhelm.Get(a, name, 0)
	require.NoError(t, err)
	return d
}
func liveRun(t *testing.T, p *ocularhelm.Plans, f ocularhelm.Factory, o ocularhelm.Operation, ch chart.Charter) ocularhelm.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	plan, err := p.Prepare(ctx, "live", f, o, ch)
	require.NoError(t, err)
	result, err := p.Run(ctx, "live", plan.ID, f)
	require.NoError(t, err)
	return result
}

const liveConfigMap = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
data:
  greeting: {{ .Values.greeting | quote }}
`
const liveDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
spec:
  replicas: 1
  selector:
    matchLabels: {app: {{ .Release.Name }}}
  template:
    metadata:
      labels: {app: {{ .Release.Name }}}
      annotations: {greeting: {{ .Values.greeting | quote }}}
    spec:
      terminationGracePeriodSeconds: 1
      containers:
        - name: app
          image: busybox:1.36
          imagePullPolicy: IfNotPresent
          command: [sh, -c, "{{ if .Values.ready }}touch /ready; {{ end }}exec sleep 3600"]
          readinessProbe:
            exec: {command: [test, -f, /ready]}
            periodSeconds: 1
          resources:
            requests: {cpu: 10m, memory: 8Mi}
            limits: {memory: 32Mi}
`
const liveHook = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Release.Name }}-hook
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: hook
          image: busybox:1.36
          imagePullPolicy: IfNotPresent
          command: [sh, -c, "exit {{ if .Values.failHook }}1{{ else }}0{{ end }}"]
`

// Chart bytes go through the same repository catalogue/version/digest path as UI installs.
func liveChart(t *testing.T, ns string, withCRD bool) chart.Charter {
	t.Helper()
	ch := &chartv2.Chart{Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "ocular-live", Version: "1.0.0"},
		Values: map[string]any{"greeting": "first", "ready": true, "failHook": false},
		Templates: []*common.File{{Name: "templates/config.yaml", Data: []byte(liveConfigMap)},
			{Name: "templates/deploy.yaml", Data: []byte(liveDeployment)}, {Name: "templates/hook.yaml", Data: []byte(liveHook)}}}
	if withCRD {
		ch.Templates = []*common.File{{Name: "templates/config.yaml", Data: []byte(liveConfigMap)},
			{Name: "templates/widget.yaml", Data: []byte(fmt.Sprintf("apiVersion: %s.ocular.test/v1\nkind: Widget\nmetadata:\n  name: {{ .Release.Name }}\nspec:\n  message: fixture\n", ns))}}
		ch.Files = []*common.File{{Name: "crds/widgets.yaml", Data: []byte(fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.%s.ocular.test
  labels: {ocular.helm.owner: %s}
spec:
  group: %s.ocular.test
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                message: {type: string}
`, ns, ns, ns))}}
	}
	archive, err := chartutil.Save(ch, t.TempDir())
	require.NoError(t, err)
	bytes, err := os.ReadFile(archive)
	require.NoError(t, err)
	digest := sha256.Sum256(bytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			_, _ = fmt.Fprintf(w, "apiVersion: v1\nentries:\n  ocular-live:\n    - name: ocular-live\n      version: 1.0.0\n      urls: [chart.tgz]\n      digest: %x\n", digest)
			return
		}
		_, _ = w.Write(bytes)
	}))
	t.Cleanup(server.Close)
	repos := ocularhelm.NewRepositories(t.TempDir())
	require.NoError(t, repos.Save(ocularhelm.Settings{Repositories: []ocularhelm.Repository{{Name: "fixture", URL: server.URL}}, Storage: ocularhelm.Storage{Driver: "secret"}}))
	rows, err := repos.Catalog(t.Context(), "fixture")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	loaded, _, err := repos.Load(t.Context(), rows[0].ChartRef)
	require.NoError(t, err)
	return loaded
}

func TestHelmLiveLifecycle(t *testing.T) {
	fixture := helmLive(t)
	ch := liveChart(t, fixture.ns, false)
	for _, driver := range []string{"secret", "configmap", "sql"} {
		t.Run(driver, func(t *testing.T) {
			settings := ocularhelm.Storage{Driver: driver}
			if driver == "sql" {
				dsn, err := os.ReadFile(os.Getenv("OCULAR_HELM_SQL_DSN_FILE"))
				require.NoError(t, err)
				settings.SQLConnection = string(dsn)
			}
			f := fixture.factory(settings)
			p := ocularhelm.NewPlans()
			t.Cleanup(p.Close)
			o := ocularhelm.Operation{Action: "install", Name: "lifecycle-" + driver, Namespace: fixture.ns, TimeoutSeconds: 45, Wait: true, Values: "greeting: first\nready: true\n"}
			plan, err := p.Prepare(t.Context(), "live", f, o, ch)
			require.NoError(t, err)
			require.Equal(t, "server", plan.PreviewMode)
			require.Contains(t, plan.Manifest, "kind: Job")
			_, err = fixture.client.CoreV1().ConfigMaps(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
			require.True(t, apierrors.IsNotFound(err), "preview wrote resources: %v", err)
			result, err := p.Run(t.Context(), "live", plan.ID, f)
			require.NoError(t, err)
			require.Equal(t, "done", result.Outcome, result.Message)
			_, err = p.Run(t.Context(), "live", plan.ID, f)
			require.Error(t, err, "one-shot plan was replayed")
			d := liveDetail(t, f, fixture.ns, o.Name)
			require.Equal(t, 1, d.Revision)
			require.Equal(t, "deployed", d.Status)
			dep, err := fixture.client.AppsV1().Deployments(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.EqualValues(t, 1, dep.Status.ReadyReplicas)
			o.Action = "upgrade"
			o.Values = "greeting: second\nready: true\n"
			stale, err := p.Prepare(t.Context(), "live", f, o, ch)
			require.NoError(t, err)
			result = liveRun(t, p, f, o, ch)
			require.Equal(t, "done", result.Outcome, result.Message)
			_, err = p.Run(t.Context(), "live", stale.ID, f)
			require.ErrorContains(t, err, "changed after review")
			d = liveDetail(t, f, fixture.ns, o.Name)
			require.Equal(t, 2, d.Revision)
			o.Values = "greeting: failed-upgrade\nready: true\nfailHook: true\n"
			result = liveRun(t, p, f, o, ch)
			require.Equal(t, "unknown", result.Outcome)
			d = liveDetail(t, f, fixture.ns, o.Name)
			require.Equal(t, "failed", d.Status)
			require.Equal(t, 3, d.Revision)
			cm, err := fixture.client.CoreV1().ConfigMaps(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, "failed-upgrade", cm.Data["greeting"], "failed hook can follow actual resource writes")
			job, err := fixture.client.BatchV1().Jobs(fixture.ns).Get(t.Context(), o.Name+"-hook", metav1.GetOptions{})
			require.NoError(t, err)
			require.Positive(t, job.Status.Failed)
			o.Action = "rollback"
			o.Revision = 1
			result = liveRun(t, p, f, o, nil)
			require.Equal(t, "done", result.Outcome, result.Message)
			d = liveDetail(t, f, fixture.ns, o.Name)
			require.Equal(t, 4, d.Revision)
			require.Equal(t, "deployed", d.Status)
			require.Len(t, d.History, 4)
			require.Contains(t, d.Values, "first")
			cm, err = fixture.client.CoreV1().ConfigMaps(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, "first", cm.Data["greeting"])
			o.Action = "uninstall"
			o.KeepHistory = true
			result = liveRun(t, p, f, o, nil)
			require.Equal(t, "done", result.Outcome, result.Message)
			_, err = fixture.client.CoreV1().ConfigMaps(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
			require.True(t, apierrors.IsNotFound(err))
			d = liveDetail(t, f, fixture.ns, o.Name)
			require.Equal(t, "uninstalled", d.Status)
			t.Log("repository → preview → install → upgrade → stale review rejection → failed hook → diagnosis → rollback → uninstall with history")
		})
	}
}

func TestHelmLiveCRDInstall(t *testing.T) {
	fixture := helmLive(t)
	f := fixture.factory(ocularhelm.Storage{Driver: "secret"})
	p := ocularhelm.NewPlans()
	t.Cleanup(p.Close)
	ch := liveChart(t, fixture.ns, true)
	dynamicClient, err := dynamic.NewForConfig(fixture.s.conn.cfg)
	require.NoError(t, err)
	crds := dynamicClient.Resource(schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"})
	name := "widgets." + fixture.ns + ".ocular.test"
	o := ocularhelm.Operation{Action: "install", Name: "crd-live", Namespace: fixture.ns, TimeoutSeconds: 45, Values: "{}"}
	plan, err := p.Prepare(t.Context(), "live", f, o, ch)
	require.NoError(t, err)
	require.Equal(t, "client", plan.PreviewMode)
	require.Contains(t, plan.Manifest, "kind: CustomResourceDefinition")
	require.Contains(t, plan.Manifest, "kind: Widget")
	_, err = crds.Get(t.Context(), name, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
	result, err := p.Run(t.Context(), "live", plan.ID, f)
	require.NoError(t, err)
	require.Equal(t, "done", result.Outcome, result.Message)
	widgets := dynamicClient.Resource(schema.GroupVersionResource{Group: fixture.ns + ".ocular.test", Version: "v1", Resource: "widgets"}).Namespace(fixture.ns)
	_, err = widgets.Get(t.Context(), o.Name, metav1.GetOptions{})
	require.NoError(t, err)
	o.Action = "uninstall"
	result = liveRun(t, p, f, o, nil)
	require.Equal(t, "done", result.Outcome, result.Message)
	_, err = widgets.Get(t.Context(), o.Name, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
	_, err = crds.Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err, "Helm deliberately retains CRDs; runner owns cleanup")
	t.Log("local CRD preview wrote nothing; real installation admitted the new kind; uninstall retained the CRD")
}

func TestHelmLiveRBACFailure(t *testing.T) {
	fixture := helmLive(t)
	_, err := fixture.client.CoreV1().ServiceAccounts(fixture.ns).Create(t.Context(), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "reader"}}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fixture.client.RbacV1().Roles(fixture.ns).Create(t.Context(), &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "reader"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets", "configmaps"}, Verbs: []string{"get", "list", "watch"}}}}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fixture.client.RbacV1().RoleBindings(fixture.ns).Create(t.Context(), &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "reader"}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "reader", Namespace: fixture.ns}}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "reader"}}, metav1.CreateOptions{})
	require.NoError(t, err)
	cfg := rest.CopyConfig(fixture.s.conn.cfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + fixture.ns + ":reader"}
	f := func(ctx context.Context, ns string) (*action.Configuration, error) {
		return ocularhelm.Configuration(ctx, cfg, ns, ocularhelm.Storage{Driver: "secret"})
	}
	p := ocularhelm.NewPlans()
	t.Cleanup(p.Close)
	ch := &chartv2.Chart{Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "rbac", Version: "1.0.0"}, Templates: []*common.File{{Name: "templates/config.yaml", Data: []byte(liveConfigMap)}}}
	o := ocularhelm.Operation{Action: "install", Name: "denied-live", Namespace: fixture.ns, TimeoutSeconds: 20, Values: "greeting: denied"}
	result := liveRun(t, p, f, o, ch)
	require.Equal(t, "unknown", result.Outcome)
	require.Contains(t, result.Message, "forbidden")
	_, err = fixture.client.CoreV1().ConfigMaps(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
	require.NoError(t, fixture.client.RbacV1().RoleBindings(fixture.ns).Delete(t.Context(), "reader", metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		_, err := p.Prepare(t.Context(), "live", f, o, ch)
		return err != nil && strings.Contains(err.Error(), "forbidden")
	}, 10*time.Second, 100*time.Millisecond)
	t.Log("real RBAC denied writes without resource creation; revoked reads surfaced as errors")
}

func TestHelmLiveTimeoutAndCancellation(t *testing.T) {
	fixture := helmLive(t)
	f := fixture.factory(ocularhelm.Storage{Driver: "secret"})
	ch := liveChart(t, fixture.ns, false)
	for _, mode := range []string{"timeout", "cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			p := ocularhelm.NewPlans()
			defer p.Close()
			o := ocularhelm.Operation{Action: "install", Name: "wait-" + mode, Namespace: fixture.ns, TimeoutSeconds: 30, Wait: true, DisableHooks: true, Values: "ready: false"}
			if mode == "timeout" {
				o.TimeoutSeconds = 3
			}
			plan, err := p.Prepare(t.Context(), "live", f, o, ch)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type answer struct {
				result ocularhelm.Result
				err    error
			}
			done := make(chan answer, 1)
			go func() { r, e := p.Run(ctx, "live", plan.ID, f); done <- answer{r, e} }()
			require.Eventually(t, func() bool {
				_, err := fixture.client.AppsV1().Deployments(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
				return err == nil
			}, 10*time.Second, 20*time.Millisecond)
			started := time.Now()
			if mode == "cancel" {
				cancel()
			}
			if mode == "session-close" {
				fixture.s.Close()
			}
			select {
			case a := <-done:
				require.NoError(t, a.err)
				require.Equal(t, "unknown", a.result.Outcome)
			case <-time.After(8 * time.Second):
				cancel()
				<-done
				t.Fatal("wait did not stop after timeout/cancellation")
			}
			_, err = p.Run(t.Context(), "live", plan.ID, f)
			require.Error(t, err)
			t.Logf("%s returned uncertain outcome in %s; no plan replay", mode, time.Since(started))
		})
	}
}

type loseHelmResponse struct {
	base      http.RoundTripper
	namespace string
	writes    *atomic.Int32
}

func (r loseHelmResponse) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err == nil && (req.Method == http.MethodPost || req.Method == http.MethodPatch) && strings.HasPrefix(req.URL.Path, "/api/v1/namespaces/"+r.namespace+"/configmaps") && req.URL.Query().Get("dryRun") == "" {
		r.writes.Add(1)
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return response, err
}
func TestHelmLiveLostMutationResponse(t *testing.T) {
	fixture := helmLive(t)
	var writes atomic.Int32
	cfg := rest.CopyConfig(fixture.s.conn.cfg)
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper { return loseHelmResponse{rt, fixture.ns, &writes} })
	f := func(ctx context.Context, ns string) (*action.Configuration, error) {
		return ocularhelm.Configuration(ctx, cfg, ns, ocularhelm.Storage{Driver: "secret"})
	}
	p := ocularhelm.NewPlans()
	t.Cleanup(p.Close)
	ch := &chartv2.Chart{Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "loss", Version: "1.0.0"}, Templates: []*common.File{{Name: "templates/config.yaml", Data: []byte(liveConfigMap)}}}
	o := ocularhelm.Operation{Action: "install", Name: "response-loss", Namespace: fixture.ns, TimeoutSeconds: 20, Values: "greeting: committed"}
	result := liveRun(t, p, f, o, ch)
	require.Equal(t, "unknown", result.Outcome)
	require.EqualValues(t, 1, writes.Load(), "ambiguous mutation was retried")
	cm, err := fixture.client.CoreV1().ConfigMaps(fixture.ns).Get(t.Context(), o.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "committed", cm.Data["greeting"])
	t.Log("real API accepted the write; dropped response produced unknown with exactly one mutation request")
}
