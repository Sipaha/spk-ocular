package kubernetes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// docGetter answers GETs from a map (path → body); others are 404s. It
// records the paths asked.
type docGetter struct {
	mu    sync.Mutex
	docs  map[string]string
	asked []string
}

func (g *docGetter) get(_ context.Context, path, _ string) ([]byte, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, path)
	if b, ok := g.docs[path]; ok {
		return []byte(b), "application/json", nil
	}
	return nil, "", errors.New("404 not found")
}

func routeDef(group, version, resource string, namespaced bool) *kindDef {
	return &kindDef{discovered: true, gvr: schema.GroupVersionResource{Group: group, Version: version, Resource: resource}, namespaced: namespaced, verbs: []string{"get", "list", "watch", "patch"}}
}

const remoteAPIService = `{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"v99.networking.k8s.io"},"spec":{"group":"networking.k8s.io","version":"v99","service":{"name":"agg","namespace":"kube-system"}}}`

func TestDryRunIsProvenPerRouteNeverPerGroup(t *testing.T) {
	ctx := context.Background()
	// A described kind's exact route: assumed served by kube-apiserver.
	var p dryRunProofs
	if !p.proven(ctx, (&docGetter{}).get, podsKind) {
		t.Fatal("a described kind's route is proven")
	}
	// A resource of a built-in group through an aggregated APIService whose
	// OpenAPI does not show dryRun: not proven, whatever the group.
	g := &docGetter{docs: map[string]string{
		"/apis/apiregistration.k8s.io/v1/apiservices/v99.networking.k8s.io": remoteAPIService,
		"/openapi/v3/apis/networking.k8s.io/v99":                            `{"paths":{"/apis/networking.k8s.io/v99/namespaces/{namespace}/widgets/{name}":{"patch":{"parameters":[{"name":"fieldManager","in":"query"}]}}}}`,
	}}
	if p.proven(ctx, g.get, routeDef("networking.k8s.io", "v99", "widgets", true)) {
		t.Fatal("an aggregated route without dryRun in its OpenAPI must not be proven")
	}
	// Nothing readable: not proven.
	if p.proven(ctx, (&docGetter{}).get, routeDef("x.io", "v1", "things", true)) {
		t.Fatal("unreadable proof must not be proven")
	}
}

func TestDryRunProofByLocalAPIServiceOrOpenAPI(t *testing.T) {
	ctx := context.Background()
	var p dryRunProofs
	g := &docGetter{docs: map[string]string{
		"/apis/apiregistration.k8s.io/v1/apiservices/v1.ocular.dev": localAPIService("ocular.dev", "v1"),
	}}
	if !p.proven(ctx, g.get, routeDef("ocular.dev", "v1", "widgets", true)) {
		t.Fatal("a local APIService proves it")
	}
	// Once per group-version.
	p.proven(ctx, g.get, routeDef("ocular.dev", "v1", "gadgets", false))
	if n := len(g.asked); n != 1 {
		t.Fatalf("asked %d times: %v", n, g.asked)
	}

	// Aggregated, with dryRun through a $ref (path-level) on the exact path.
	g = &docGetter{docs: map[string]string{
		"/apis/apiregistration.k8s.io/v1/apiservices/v1beta1.agg.io": `{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"v1beta1.agg.io"},"spec":{"group":"agg.io","version":"v1beta1","service":{"name":"s","namespace":"n"}}}`,
		"/openapi/v3/apis/agg.io/v1beta1": `{"paths":{
			"/apis/agg.io/v1beta1/things/{name}":{"parameters":[{"$ref":"#/components/parameters/dryRun-x"}],"patch":{}},
			"/apis/agg.io/v1beta1/namespaces/{namespace}/others/{name}":{"patch":{"parameters":[{"name":"force","in":"query"}]}}},
			"components":{"parameters":{"dryRun-x":{"name":"dryRun","in":"query"}}}}`,
	}}
	if !p.proven(ctx, g.get, routeDef("agg.io", "v1beta1", "things", false)) {
		t.Fatal("dryRun through a $ref proves it")
	}
	if p.proven(ctx, g.get, routeDef("agg.io", "v1beta1", "others", true)) {
		t.Fatal("another path of the same group-version without dryRun is not proven")
	}
	for _, path := range g.asked {
		if !strings.HasPrefix(path, "/apis/apiregistration") && !strings.HasPrefix(path, "/openapi/") {
			t.Fatalf("unexpected request %s", path)
		}
	}
}

func TestDryRunProofTakesOnlyTheGroupVersionsOwnAPIService(t *testing.T) {
	ctx := context.Background()
	const at = "/apis/apiregistration.k8s.io/v1/apiservices/v1.ocular.dev"
	for name, body := range map[string]string{
		"a Deployment":       `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"v1.ocular.dev"},"spec":{}}`,
		"no envelope":        `{"spec":{"group":"ocular.dev","version":"v1"}}`,
		"another name":       `{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"v2.ocular.dev"},"spec":{"group":"ocular.dev","version":"v1"}}`,
		"another group":      `{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"v1.ocular.dev"},"spec":{"group":"x.io","version":"v1"}}`,
		"another version":    `{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"v1.ocular.dev"},"spec":{"group":"ocular.dev","version":"v2"}}`,
		"an old API version": `{"apiVersion":"apiregistration.k8s.io/v1beta1","kind":"APIService","metadata":{"name":"v1.ocular.dev"},"spec":{"group":"ocular.dev","version":"v1"}}`,
		"not an object":      `[1]`,
	} {
		t.Run(name, func(t *testing.T) {
			var p dryRunProofs
			g := &docGetter{docs: map[string]string{at: body}}
			if p.proven(ctx, g.get, routeDef("ocular.dev", "v1", "widgets", true)) {
				t.Fatal("proven by an answer that is not the group-version's APIService")
			}
			// Not a definite answer: asked again, never kept as local.
			g.docs[at] = localAPIService("ocular.dev", "v1")
			if !p.proven(ctx, g.get, routeDef("ocular.dev", "v1", "widgets", true)) {
				t.Fatal("a later valid answer must prove it")
			}
		})
	}
}

func localAPIService(group, version string) string {
	return `{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"` + version + "." + group +
		`"},"spec":{"group":"` + group + `","version":"` + version + `"}}`
}
