package helm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/spk/spk-ocular/internal/helm/sqlstore"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/storage"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Getter retains the admitted session's transport/authentication configuration.
// It never loads a kubeconfig or consults the process environment.
type Getter struct {
	Config    *rest.Config
	Namespace string
}

func (g Getter) ToRESTConfig() (*rest.Config, error) { return rest.CopyConfig(g.Config), nil }
func (g Getter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	c, err := discovery.NewDiscoveryClientForConfig(g.Config)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(c), nil
}
func (g Getter) ToRESTMapper() (meta.RESTMapper, error) {
	c, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(c), nil
}
func (g Getter) ToRawKubeConfigLoader() clientcmd.ClientConfig { return rawConfig{g} }

type rawConfig struct{ Getter }

func (r rawConfig) RawConfig() (clientcmdapi.Config, error) { return clientcmdapi.Config{}, nil }
func (r rawConfig) ClientConfig() (*rest.Config, error)     { return r.ToRESTConfig() }
func (r rawConfig) Namespace() (string, bool, error)        { return r.Getter.Namespace, true, nil }
func (r rawConfig) ConfigAccess() clientcmd.ConfigAccess    { return nil }

type requestTransport struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t requestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)

	// Helm's legacy actions use background contexts. Bind every network operation
	// to the UI operation, including discovery, storage and hooks.
	res, err := t.base.RoundTrip(r.Clone(ctx))
	if res != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Client-go retries mutations on Retry-After; an uncertain Helm operation
		// must be reconciled explicitly instead.
		res.Header.Del("Retry-After")
	}
	if err != nil {
		stop()
		cancel()
	} else if res != nil {
		res.Body = &operationBody{ReadCloser: res.Body, stop: stop, cancel: cancel}
	}
	return res, err
}

type operationBody struct {
	io.ReadCloser
	stop   func() bool
	cancel context.CancelFunc
}

func (b *operationBody) Close() error { defer b.cancel(); defer b.stop(); return b.ReadCloser.Close() }

func Configuration(ctx context.Context, cfg *rest.Config, namespace string, settings Storage) (*action.Configuration, error) {
	c := rest.CopyConfig(cfg)
	c.Wrap(func(rt http.RoundTripper) http.RoundTripper { return requestTransport{rt, ctx} })
	a := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	drv := settings.Driver
	if drv == "sql" {
		drv = "secret"
	}
	if err := a.Init(Getter{c, namespace}, namespace, drv); err != nil {
		return nil, err
	}
	client, ok := a.KubeClient.(*kube.Client)
	if !ok {
		return nil, fmt.Errorf("unsupported Helm Kubernetes client %T", a.KubeClient)
	}
	// Cancel both network traffic and the watcher's local wait loop when the
	// admitted session ends. Cancelling transport alone lets the watcher retry
	// until its original operation timeout.
	a.KubeClient = &operationKubeClient{Client: client, ctx: ctx}
	if settings.Driver == "sql" {
		d, err := sqlstore.NewSQL(ctx, settings.SQLConnection, namespace)
		if err != nil {
			return nil, err
		}
		d.SetLogger(slog.NewTextHandler(io.Discard, nil))
		a.Releases = storage.Init(d)
	}
	return a, nil
}

// The factory context includes both the operation deadline and session lifetime.
// SDK actions also pass their caller context as an option; the admitted context
// must win so that a closed session stops readiness, hooks and deletion waits.
type operationKubeClient struct {
	*kube.Client
	ctx context.Context
}

func (c *operationKubeClient) GetWaiter(strategy kube.WaitStrategy) (kube.Waiter, error) {
	return c.GetWaiterWithOptions(strategy)
}
func (c *operationKubeClient) GetWaiterWithOptions(strategy kube.WaitStrategy, options ...kube.WaitOption) (kube.Waiter, error) {
	options = append(options, kube.WithWaitContext(c.ctx))
	return c.Client.GetWaiterWithOptions(strategy, options...)
}
