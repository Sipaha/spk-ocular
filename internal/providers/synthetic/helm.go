package synthetic

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	ocularhelm "github.com/spk/spk-ocular/internal/helm"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/kube/fake"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/resource"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"
)

type helmFixture struct {
	once   sync.Once
	client *kubefake.Clientset
}

// HelmConfiguration exercises the actual SDK against disposable in-memory
// storage. Synthetic mode never sends Helm traffic to an external cluster.
func (s *session) HelmConfiguration(_ context.Context, namespace string, _ ocularhelm.Storage) (*action.Configuration, error) {
	s.helm.once.Do(func() {
		s.helm.client = kubefake.NewSimpleClientset()
		st := storage.Init(driver.NewSecrets(s.helm.client.CoreV1().Secrets("blue")))
		_ = st.Create(&release.Release{Name: "demo-web", Namespace: "blue", Version: 1, Chart: &chart.Chart{Metadata: &chart.Metadata{APIVersion: "v2", Name: "demo-web", Version: "1.2.0", AppVersion: "2.4.1"}, Values: map[string]any{"replicas": 1}}, Config: map[string]any{"replicas": 2}, Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo-web\n  namespace: blue\ndata:\n  greeting: hello\n", Info: &release.Info{Status: releasecommon.StatusDeployed, LastDeployed: time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), Notes: "Synthetic Helm release. No external cluster is used."}})
	})
	a := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	a.KubeClient = &helmKubeClient{PrintingKubeClient: fake.PrintingKubeClient{Out: io.Discard}}
	a.Capabilities = common.DefaultCapabilities
	a.Releases = storage.Init(driver.NewSecrets(s.helm.client.CoreV1().Secrets(namespace)))
	return a, nil
}

// The SDK requires a nonempty resource list for CRD installation. Ordinary
// resources keep the upstream printing client's behavior; no external API is
// consulted by this fixture.
type helmKubeClient struct{ fake.PrintingKubeClient }

func (c *helmKubeClient) Build(r io.Reader, validate bool) (kube.ResourceList, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := yaml.Unmarshal(b, &obj); err == nil && obj["kind"] == "CustomResourceDefinition" {
		u := &unstructured.Unstructured{Object: obj}
		return kube.ResourceList{&resource.Info{Name: u.GetName(), Object: u, Mapping: &meta.RESTMapping{Resource: schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}, GroupVersionKind: u.GroupVersionKind(), Scope: meta.RESTScopeRoot}}}, nil
	}
	return c.PrintingKubeClient.Build(bytes.NewReader(b), validate)
}
