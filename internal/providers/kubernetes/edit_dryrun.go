package kubernetes

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// dryRunProofs says whether a route's PATCH honours dryRun — proven before
// any such request: an endpoint that ignores the parameter would write
// (cli-runtime helper.go). Proof, per route, never per group name (an
// APIService may serve any group-version from another server):
//   - a described kind's exact route (served by kube-apiserver: assumed);
//   - a Local APIService of the group-version (kube-apiserver serves it,
//     CRDs included; dry run is GA since 1.18);
//   - the group-version's OpenAPI v3 listing dryRun for the route's PATCH.
//
// Definite answers are kept for the session, per group-version.
type dryRunProofs struct {
	mu sync.Mutex
	// local: the group-version's APIService is Local (read).
	local map[schema.GroupVersion]bool
	// patchDryRun: per group-version, the paths whose PATCH lists dryRun.
	patchDryRun map[schema.GroupVersion]map[string]bool
}

func (p *dryRunProofs) proven(ctx context.Context, get getter, def *kindDef) bool {
	if !def.discovered {
		return true
	}
	if get == nil {
		return false
	}
	gv := def.gvr.GroupVersion()
	if local, ok := p.apiServiceLocal(ctx, get, gv); ok && local {
		return true
	}
	paths, ok := p.openAPI(ctx, get, gv)
	return ok && paths[resourcePath(def)]
}

func (p *dryRunProofs) apiServiceLocal(ctx context.Context, get getter, gv schema.GroupVersion) (bool, bool) {
	p.mu.Lock()
	local, ok := p.local[gv]
	p.mu.Unlock()
	if ok {
		return local, true
	}
	body, _, err := get(ctx, "/apis/apiregistration.k8s.io/v1/apiservices/"+gv.Version+"."+gv.Group, "application/json")
	if err != nil {
		return false, false
	}
	var doc struct {
		Spec *struct {
			Service *json.RawMessage `json:"service"`
		} `json:"spec"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Spec == nil {
		return false, false
	}
	local = doc.Spec.Service == nil || string(*doc.Spec.Service) == "null"
	p.mu.Lock()
	if p.local == nil {
		p.local = map[schema.GroupVersion]bool{}
	}
	p.local[gv] = local
	p.mu.Unlock()
	return local, true
}

type openAPIParam struct {
	Ref  string `json:"$ref"`
	Name string `json:"name"`
	In   string `json:"in"`
}

func (p *dryRunProofs) openAPI(ctx context.Context, get getter, gv schema.GroupVersion) (map[string]bool, bool) {
	p.mu.Lock()
	paths, ok := p.patchDryRun[gv]
	p.mu.Unlock()
	if ok {
		return paths, true
	}
	path := "/openapi/v3/apis/" + gv.Group + "/" + gv.Version
	if gv.Group == "" {
		path = "/openapi/v3/api/" + gv.Version
	}
	body, _, err := get(ctx, path, "application/json")
	if err != nil {
		return nil, false
	}
	var doc struct {
		Paths map[string]struct {
			Parameters []openAPIParam `json:"parameters"`
			Patch      *struct {
				Parameters []openAPIParam `json:"parameters"`
			} `json:"patch"`
		} `json:"paths"`
		Components struct {
			Parameters map[string]openAPIParam `json:"parameters"`
		} `json:"components"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, false
	}
	isDryRun := func(q openAPIParam) bool {
		if q.Ref != "" {
			name, ok := strings.CutPrefix(q.Ref, "#/components/parameters/")
			if !ok {
				return false
			}
			q = doc.Components.Parameters[name]
		}
		return q.Name == "dryRun" && q.In == "query"
	}
	paths = map[string]bool{}
	for p, item := range doc.Paths {
		if item.Patch == nil {
			continue
		}
		for _, q := range append(append([]openAPIParam{}, item.Parameters...), item.Patch.Parameters...) {
			if isDryRun(q) {
				paths[p] = true
				break
			}
		}
	}
	p.mu.Lock()
	if p.patchDryRun == nil {
		p.patchDryRun = map[schema.GroupVersion]map[string]bool{}
	}
	p.patchDryRun[gv] = paths
	p.mu.Unlock()
	return paths, true
}

// resourcePath is the OpenAPI path of one object of def.
func resourcePath(def *kindDef) string {
	base := "/apis/" + def.gvr.Group + "/" + def.gvr.Version
	if def.gvr.Group == "" {
		base = "/api/" + def.gvr.Version
	}
	if def.namespaced {
		base += "/namespaces/{namespace}"
	}
	return base + "/" + def.gvr.Resource + "/{name}"
}
