package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"
)

// Discovery: which resources the API serves, read the way kubectl does —
// aggregated discovery (v2: /api and /apis, two requests) with the classic
// per group-version documents as the fallback for servers without it. Our
// own parser: client-go's discovery client would pull in the OpenAPI stack.
// Discovery says what is served, never what the user may do (verbs are not
// rights).

const (
	// discoveryTimeout bounds one whole discovery (all requests).
	discoveryTimeout = 30 * time.Second
	// discoveryMaxBody bounds one discovery document (hundreds of CRDs fit).
	discoveryMaxBody = 32 << 20
	// discoveryFanout: classic discovery's parallel group-version requests.
	discoveryFanout = 8

	acceptDiscoveryV2 = "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList,application/json"
)

// apiResource is one served resource at its preferred version.
type apiResource struct {
	Group, Version, Resource string
	Kind, Singular           string
	ShortNames               []string
	Namespaced               bool
	Verbs                    []string
	Scale                    bool // has the scale subresource
}

func (r apiResource) has(verb string) bool {
	for _, v := range r.Verbs {
		if v == verb {
			return true
		}
	}
	return false
}

// discovered is one discovery's answer: resources at their preferred
// version, and the groups it could not confirm ("" is the core group;
// "*" — every named group, when /apis itself failed).
type discovered struct {
	resources   []apiResource
	unconfirmed map[string]bool
}

// getter fetches one API document: body, content type.
type getter func(ctx context.Context, path, accept string) ([]byte, string, error)

// httpGetter reads documents with cfg's transport (auth, TLS, proxy).
func httpGetter(cfg *rest.Config) (getter, error) {
	hc, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	base, err := serverBase(cfg)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, path, accept string) ([]byte, string, error) {
		u := *base
		u.Path = strings.TrimRight(u.Path, "/") + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Accept", accept)
		if cfg.UserAgent != "" {
			req.Header.Set("User-Agent", cfg.UserAgent)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, discoveryMaxBody+1))
		if err != nil {
			return nil, "", err
		}
		if len(body) > discoveryMaxBody {
			return nil, "", fmt.Errorf("%s: the answer is over %d bytes", path, discoveryMaxBody)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, "", &discoveryError{code: resp.StatusCode, path: path, body: strings.TrimSpace(string(body[:min(len(body), 300)]))}
		}
		return body, resp.Header.Get("Content-Type"), nil
	}, nil
}

// serverBase is cfg's server the way client-go resolves it: a URL or
// host[:port] (HTTPS by default only with TLS settings), the path prefix of
// a fronting proxy kept.
func serverBase(cfg *rest.Config) (*url.URL, error) {
	u, _, err := rest.DefaultServerUrlFor(cfg)
	return u, err
}

type discoveryError struct {
	code int
	path string
	body string
}

func (e *discoveryError) Error() string { return fmt.Sprintf("%s: HTTP %d %s", e.path, e.code, e.body) }

// discoverAPI reads what the API serves. The two roots are read at once:
// a hanging one must not use up the other's time.
func discoverAPI(ctx context.Context, get getter) discovered {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	roots := []string{"/api", "/apis"}
	parts := make([]discovered, len(roots))
	var wg sync.WaitGroup
	for i, root := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parts[i] = discoverRoot(ctx, get, root)
		}()
	}
	wg.Wait()
	out := discovered{unconfirmed: map[string]bool{}}
	for _, p := range parts {
		out.add(p)
	}
	sort.Slice(out.resources, func(i, j int) bool {
		a, b := out.resources[i], out.resources[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Resource < b.Resource
	})
	return out
}

func discoverRoot(ctx context.Context, get getter, root string) discovered {
	body, ct, err := get(ctx, root, acceptDiscoveryV2)
	switch {
	case err != nil:
		return uncertain(rootGroups(root))
	case aggregated(ct):
		return parseV2(body, root)
	}
	return discoverV1(ctx, get, root, body) // no aggregated discovery: the classic documents
}

// aggregated: the answer is an aggregated discovery document (parameters
// may be quoted).
func aggregated(contentType string) bool {
	_, p, err := mime.ParseMediaType(contentType)
	return err == nil && p["as"] == "APIGroupDiscoveryList" && p["g"] == "apidiscovery.k8s.io" && p["v"] == "v2"
}

func uncertain(group string) discovered {
	return discovered{unconfirmed: map[string]bool{group: true}}
}

// envelope: the document is the one asked for — its kind, or (a server may
// omit it) its list field present. Anything else (a Status, {}) says
// nothing about what is served.
func envelope(body []byte, kind, field string) bool {
	var e map[string]json.RawMessage
	if json.Unmarshal(body, &e) != nil {
		return false
	}
	var k string
	if raw, ok := e["kind"]; ok && json.Unmarshal(raw, &k) != nil {
		return false
	}
	if k != "" {
		return k == kind
	}
	raw, ok := e[field]
	return ok && string(raw) != "null"
}

func rootGroups(root string) string {
	if root == "/api" {
		return ""
	}
	return "*"
}

func (d *discovered) add(p discovered) {
	d.resources = append(d.resources, p.resources...)
	for g := range p.unconfirmed {
		d.unconfirmed[g] = true
	}
}

// v2 (apidiscovery.k8s.io/v2 APIGroupDiscoveryList).
type v2List struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Versions []struct {
			Version   string `json:"version"`
			Freshness string `json:"freshness"`
			Resources []struct {
				Resource     string `json:"resource"`
				ResponseKind *struct {
					Kind string `json:"kind"`
				} `json:"responseKind"`
				Scope            string   `json:"scope"`
				SingularResource string   `json:"singularResource"`
				Verbs            []string `json:"verbs"`
				ShortNames       []string `json:"shortNames"`
				Subresources     []struct {
					Subresource string `json:"subresource"`
				} `json:"subresources"`
			} `json:"resources"`
		} `json:"versions"`
	} `json:"items"`
}

// parseV2: versions come in preference order; a resource is taken at the
// first version serving it. A stale version (its aggregated API did not
// answer) leaves the group unconfirmed, and nothing is taken from versions
// after it: a resource must not move to a less preferred version because
// the preferred one could not be read.
func parseV2(body []byte, root string) discovered {
	out := discovered{unconfirmed: map[string]bool{}}
	var l v2List
	if !envelope(body, "APIGroupDiscoveryList", "items") || json.Unmarshal(body, &l) != nil {
		return uncertain(rootGroups(root))
	}
	for _, g := range l.Items {
		group := g.Metadata.Name
		seen := map[string]bool{}
		for _, v := range g.Versions {
			if v.Freshness == "Stale" {
				out.unconfirmed[group] = true
				break
			}
			for _, r := range v.Resources {
				if seen[r.Resource] {
					continue
				}
				seen[r.Resource] = true
				res := apiResource{Group: group, Version: v.Version, Resource: r.Resource, Singular: r.SingularResource,
					ShortNames: r.ShortNames, Namespaced: r.Scope == "Namespaced", Verbs: r.Verbs}
				if r.ResponseKind != nil {
					res.Kind = r.ResponseKind.Kind
				}
				for _, s := range r.Subresources {
					if s.Subresource == "scale" {
						res.Scale = true
					}
				}
				out.resources = append(out.resources, res)
			}
		}
	}
	return out
}

// v1 (classic) documents.
type v1ResourceList struct {
	Resources []struct {
		Name         string   `json:"name"`
		SingularName string   `json:"singularName"`
		Namespaced   bool     `json:"namespaced"`
		Kind         string   `json:"kind"`
		Verbs        []string `json:"verbs"`
		ShortNames   []string `json:"shortNames"`
	} `json:"resources"`
}

// discoverV1 reads the classic documents under root, given root's own
// answer (APIVersions for /api, APIGroupList for /apis).
func discoverV1(ctx context.Context, get getter, root string, rootBody []byte) discovered {
	out := discovered{unconfirmed: map[string]bool{}}
	type gv struct{ group, version string }
	var groups [][]gv // per group, versions in preference order
	if root == "/api" {
		var vs struct {
			Versions []string `json:"versions"`
		}
		if !envelope(rootBody, "APIVersions", "versions") || json.Unmarshal(rootBody, &vs) != nil {
			return uncertain("")
		}
		var g []gv
		for _, v := range vs.Versions {
			g = append(g, gv{"", v})
		}
		groups = append(groups, g)
	} else {
		var gl struct {
			Groups []struct {
				Name     string `json:"name"`
				Versions []struct {
					Version string `json:"version"`
				} `json:"versions"`
				Preferred struct {
					Version string `json:"version"`
				} `json:"preferredVersion"`
			} `json:"groups"`
		}
		if !envelope(rootBody, "APIGroupList", "groups") || json.Unmarshal(rootBody, &gl) != nil {
			return uncertain("*")
		}
		for _, grp := range gl.Groups {
			// preferredVersion first when given (it is optional), then the
			// served versions in their order
			var g []gv
			if p := grp.Preferred.Version; p != "" {
				g = append(g, gv{grp.Name, p})
			}
			for _, v := range grp.Versions {
				if v.Version != "" && v.Version != grp.Preferred.Version {
					g = append(g, gv{grp.Name, v.Version})
				}
			}
			groups = append(groups, g)
		}
	}
	// Every group-version document, a bounded number at a time.
	docs := make([][]*v1ResourceList, len(groups))
	sem := make(chan struct{}, discoveryFanout)
	var wg sync.WaitGroup
	for i, g := range groups {
		docs[i] = make([]*v1ResourceList, len(g))
		for j, x := range g {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				path := "/apis/" + x.group + "/" + x.version
				if x.group == "" {
					path = "/api/" + x.version
				}
				body, _, err := get(ctx, path, "application/json")
				if err != nil {
					return
				}
				var l v1ResourceList
				if envelope(body, "APIResourceList", "resources") && json.Unmarshal(body, &l) == nil {
					docs[i][j] = &l
				}
			}()
		}
	}
	wg.Wait()
	for i, g := range groups {
		seen := map[string]bool{}
		for j, x := range g {
			l := docs[i][j]
			if l == nil { // not read: the group is unconfirmed, later versions not used
				out.unconfirmed[x.group] = true
				break
			}
			scale := map[string]bool{}
			for _, r := range l.Resources {
				if name, sub, ok := strings.Cut(r.Name, "/"); ok && sub == "scale" {
					scale[name] = true
				}
			}
			for _, r := range l.Resources {
				if strings.Contains(r.Name, "/") || seen[r.Name] {
					continue
				}
				seen[r.Name] = true
				out.resources = append(out.resources, apiResource{Group: x.group, Version: x.version, Resource: r.Name, Kind: r.Kind,
					Singular: r.SingularName, ShortNames: r.ShortNames, Namespaced: r.Namespaced, Verbs: r.Verbs, Scale: scale[r.Name]})
			}
		}
	}
	return out
}

// merge keeps what an unconfirmed group had before: a failed read is not
// evidence that its resources are gone. Resources of confirmed groups are
// exactly the new answer's.
func merge(prev, cur discovered) discovered {
	unconfirmed := func(g string) bool {
		return cur.unconfirmed[g] || (g != "" && cur.unconfirmed["*"])
	}
	out := discovered{unconfirmed: cur.unconfirmed}
	kept := map[string]bool{}
	for _, r := range prev.resources {
		if unconfirmed(r.Group) {
			out.resources = append(out.resources, r)
			kept[r.Group] = true
		}
	}
	for _, r := range cur.resources {
		if !kept[r.Group] {
			out.resources = append(out.resources, r)
		}
	}
	sort.Slice(out.resources, func(i, j int) bool {
		a, b := out.resources[i], out.resources[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Resource < b.Resource
	})
	return out
}
