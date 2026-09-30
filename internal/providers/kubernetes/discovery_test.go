package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

type v2res struct {
	name, kind, singular, scope string
	verbs, short, subs          []string
}

type v2ver struct {
	version string
	stale   bool
	res     []v2res
}

func v2doc(groups map[string][]v2ver, order ...string) []byte {
	var items []any
	for _, g := range order {
		var vs []any
		for _, v := range groups[g] {
			var rs []any
			for _, r := range v.res {
				var subs []any
				for _, s := range r.subs {
					subs = append(subs, map[string]any{"subresource": s, "verbs": []string{"get"}})
				}
				rs = append(rs, map[string]any{"resource": r.name, "responseKind": map[string]any{"kind": r.kind},
					"scope": r.scope, "singularResource": r.singular, "verbs": r.verbs, "shortNames": r.short, "subresources": subs})
			}
			f := "Current"
			if v.stale {
				f = "Stale"
			}
			vs = append(vs, map[string]any{"version": v.version, "freshness": f, "resources": rs})
		}
		items = append(items, map[string]any{"metadata": map[string]any{"name": g}, "versions": vs})
	}
	b, _ := json.Marshal(map[string]any{"kind": "APIGroupDiscoveryList", "items": items})
	return b
}

var lw = []string{"get", "list", "watch", "delete"}

// discoveryServer answers /api and /apis per the handlers; v2 answers carry
// the aggregated content type.
func discoveryServer(t *testing.T, h map[string]func(w http.ResponseWriter, r *http.Request)) getter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := h[r.URL.Path]; ok {
			f(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	get, err := httpGetter(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return get
}

func v2(body []byte) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList")
		_, _ = w.Write(body)
	}
}

func plain(body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func resNames(d discovered) []string {
	var out []string
	for _, r := range d.resources {
		out = append(out, r.Group+"/"+r.Version+"/"+r.Resource)
	}
	return out
}

func TestDiscoveryV2PrefersEachResourcesFirstVersion(t *testing.T) {
	core := v2doc(map[string][]v2ver{"": {{version: "v1", res: []v2res{{name: "pods", kind: "Pod", singular: "pod", scope: "Namespaced", verbs: lw, short: []string{"po"}}}}}}, "")
	apis := v2doc(map[string][]v2ver{
		"ocular.dev": {
			{version: "v2", res: []v2res{{name: "widgets", kind: "Widget", singular: "widget", scope: "Namespaced", verbs: lw, short: []string{"wd"}, subs: []string{"status", "scale"}}}},
			{version: "v1", res: []v2res{
				{name: "widgets", kind: "Widget", singular: "widget", scope: "Namespaced", verbs: lw},
				{name: "gadgets", kind: "Gadget", singular: "gadget", scope: "Cluster", verbs: lw},
			}},
		},
	}, "ocular.dev")
	get := discoveryServer(t, map[string]func(http.ResponseWriter, *http.Request){"/api": v2(core), "/apis": v2(apis)})
	d := discoverAPI(context.Background(), get)
	if got := strings.Join(resNames(d), " "); got != "/v1/pods ocular.dev/v1/gadgets ocular.dev/v2/widgets" {
		t.Fatalf("resources %s", got)
	}
	w := d.resources[2]
	if w.Kind != "Widget" || w.Singular != "widget" || !w.Namespaced || !w.Scale || w.ShortNames[0] != "wd" || !w.has("watch") {
		t.Fatalf("widgets %+v", w)
	}
	if d.resources[1].Namespaced || len(d.unconfirmed) != 0 {
		t.Fatalf("gadgets %+v, unconfirmed %v", d.resources[1], d.unconfirmed)
	}
}

func TestDiscoveryV2StaleVersionLeavesTheGroupUnconfirmed(t *testing.T) {
	apis := v2doc(map[string][]v2ver{
		"metrics.example": {{version: "v1beta1", stale: true}, {version: "v1alpha1", res: []v2res{{name: "things", kind: "Thing", scope: "Cluster", verbs: lw}}}},
		"ocular.dev":      {{version: "v1", res: []v2res{{name: "widgets", kind: "Widget", scope: "Namespaced", verbs: lw}}}},
	}, "metrics.example", "ocular.dev")
	get := discoveryServer(t, map[string]func(http.ResponseWriter, *http.Request){"/api": v2(v2doc(nil)), "/apis": v2(apis)})
	d := discoverAPI(context.Background(), get)
	// nothing from a less preferred version because the preferred one is stale
	if got := strings.Join(resNames(d), " "); got != "ocular.dev/v1/widgets" || !d.unconfirmed["metrics.example"] {
		t.Fatalf("resources %s, unconfirmed %v", got, d.unconfirmed)
	}
}

func TestDiscoveryV1Fallback(t *testing.T) {
	get := discoveryServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api":                plain(`{"kind":"APIVersions","versions":["v1"]}`),
		"/api/v1":             plain(`{"resources":[{"name":"pods","singularName":"pod","namespaced":true,"kind":"Pod","verbs":["list","watch"],"shortNames":["po"]},{"name":"pods/log","kind":"Pod","verbs":["get"]}]}`),
		"/apis":               plain(`{"kind":"APIGroupList","groups":[{"name":"ocular.dev","versions":[{"version":"v2"},{"version":"v1"}],"preferredVersion":{"version":"v2"}},{"name":"broken.example","versions":[{"version":"v1"}],"preferredVersion":{"version":"v1"}}]}`),
		"/apis/ocular.dev/v2": plain(`{"resources":[{"name":"widgets","singularName":"widget","namespaced":true,"kind":"Widget","verbs":["list","watch"]},{"name":"widgets/scale","kind":"Scale","verbs":["get"]}]}`),
		"/apis/ocular.dev/v1": plain(`{"resources":[{"name":"widgets","namespaced":true,"kind":"Widget","verbs":["list","watch"]},{"name":"gadgets","namespaced":false,"kind":"Gadget","verbs":["list","watch"]}]}`),
		// broken.example/v1 is 404
	})
	d := discoverAPI(context.Background(), get)
	if got := strings.Join(resNames(d), " "); got != "/v1/pods ocular.dev/v1/gadgets ocular.dev/v2/widgets" {
		t.Fatalf("resources %s", got)
	}
	if !d.resources[2].Scale || d.resources[0].ShortNames[0] != "po" || !d.unconfirmed["broken.example"] || d.unconfirmed["ocular.dev"] {
		t.Fatalf("%+v, unconfirmed %v", d.resources, d.unconfirmed)
	}
}

func TestDiscoveryRootFailureIsUnconfirmedNotEmpty(t *testing.T) {
	get := discoveryServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api":  v2(v2doc(map[string][]v2ver{"": {{version: "v1", res: []v2res{{name: "pods", kind: "Pod", scope: "Namespaced", verbs: lw}}}}}, "")),
		"/apis": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusServiceUnavailable) },
	})
	d := discoverAPI(context.Background(), get)
	if !d.unconfirmed["*"] || len(d.resources) != 1 {
		t.Fatalf("%v %v", resNames(d), d.unconfirmed)
	}
}

func TestDiscoveryMergeKeepsUnconfirmedGroupsAndDropsConfirmedAbsence(t *testing.T) {
	prev := discovered{resources: []apiResource{
		{Group: "metrics.example", Version: "v1", Resource: "things"},
		{Group: "ocular.dev", Version: "v1", Resource: "widgets"},
		{Group: "ocular.dev", Version: "v1", Resource: "gadgets"},
		{Group: "gone.example", Version: "v1", Resource: "olds"},
	}}
	cur := discovered{resources: []apiResource{{Group: "ocular.dev", Version: "v1", Resource: "widgets"}},
		unconfirmed: map[string]bool{"metrics.example": true}}
	m := merge(prev, cur)
	// metrics.example kept (unconfirmed); gadgets and gone.example gone (confirmed absent)
	if got := strings.Join(resNames(m), " "); got != "metrics.example/v1/things ocular.dev/v1/widgets" {
		t.Fatalf("merged %s", got)
	}
	// /apis failed as a whole: every named group kept, core confirmed
	cur = discovered{resources: []apiResource{{Group: "", Version: "v1", Resource: "pods"}}, unconfirmed: map[string]bool{"*": true}}
	if got := strings.Join(resNames(merge(prev, cur)), " "); got != "/v1/pods gone.example/v1/olds metrics.example/v1/things ocular.dev/v1/gadgets ocular.dev/v1/widgets" {
		t.Fatalf("merged %s", got)
	}
}

func TestDiscoveryEndsWithItsContext(t *testing.T) {
	var calls atomic.Int32
	get := discoveryServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api": func(_ http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() },
		"/apis": func(_ http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			<-r.Context().Done()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	d := discoverAPI(ctx, get)
	if time.Since(start) > 5*time.Second || !d.unconfirmed[""] || !d.unconfirmed["*"] {
		t.Fatalf("took %s, %v", time.Since(start), d.unconfirmed)
	}
}

// Hundreds of CRDs: one document, parsed quickly.
func TestDiscoveryOfManyGroups(t *testing.T) {
	groups := map[string][]v2ver{}
	var order []string
	for i := range 600 {
		g := fmt.Sprintf("g%03d.example.com", i)
		order = append(order, g)
		var rs []v2res
		for j := range 3 {
			rs = append(rs, v2res{name: fmt.Sprintf("r%ds", j), kind: fmt.Sprintf("R%d", j), scope: "Namespaced", verbs: lw, subs: []string{"status"}})
		}
		groups[g] = []v2ver{{version: "v1", res: rs}, {version: "v1beta1", res: rs}}
	}
	body := v2doc(groups, order...)
	get := discoveryServer(t, map[string]func(http.ResponseWriter, *http.Request){"/api": v2(v2doc(nil)), "/apis": v2(body)})
	start := time.Now()
	d := discoverAPI(context.Background(), get)
	if len(d.resources) != 1800 || time.Since(start) > 2*time.Second {
		t.Fatalf("%d resources in %s (%d KiB)", len(d.resources), time.Since(start), len(body)/1024)
	}
}
