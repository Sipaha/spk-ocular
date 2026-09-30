package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	base, err := url.Parse(strings.TrimRight(cfg.Host, "/"))
	if err != nil {
		return nil, err
	}
	if base.Scheme == "" { // "host:port" as kubeconfig allows
		if base, err = url.Parse("https://" + strings.TrimRight(cfg.Host, "/")); err != nil {
			return nil, err
		}
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

type discoveryError struct {
	code int
	path string
	body string
}

func (e *discoveryError) Error() string { return fmt.Sprintf("%s: HTTP %d %s", e.path, e.code, e.body) }

// discoverAPI reads what the API serves.
func discoverAPI(ctx context.Context, get getter) discovered {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	out := discovered{unconfirmed: map[string]bool{}}
	for _, root := range []string{"/api", "/apis"} {
		body, ct, err := get(ctx, root, acceptDiscoveryV2)
		switch {
		case err != nil:
			out.unconfirmed[rootGroups(root)] = true
		case strings.Contains(ct, "as=APIGroupDiscoveryList"):
			out.add(parseV2(body))
		default: // no aggregated discovery: the classic documents
			out.add(discoverV1(ctx, get, root, body))
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
func parseV2(body []byte) discovered {
	out := discovered{unconfirmed: map[string]bool{}}
	var l v2List
	if err := json.Unmarshal(body, &l); err != nil {
		out.unconfirmed["*"] = true
		return out
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
		if err := json.Unmarshal(rootBody, &vs); err != nil {
			out.unconfirmed[""] = true
			return out
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
		if err := json.Unmarshal(rootBody, &gl); err != nil {
			out.unconfirmed["*"] = true
			return out
		}
		for _, grp := range gl.Groups {
			g := []gv{{grp.Name, grp.Preferred.Version}}
			for _, v := range grp.Versions {
				if v.Version != grp.Preferred.Version {
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
				if json.Unmarshal(body, &l) == nil {
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
