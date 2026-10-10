package kubernetes

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

const graphNodeLimit = 30000
const graphEdgeLimit = 120000

type graphRecord struct {
	node     core.GraphNode
	owners   []metav1.OwnerReference
	labels   map[string]string
	selector map[string]string
	links    []core.Relation
}
type graphJob struct {
	def       *kindDef
	namespace string
}

// Graph lists each discovered resource once per selected namespace. It never
// calls Get per node or puts YAML, annotations, environment/Secret values in the
// response. List concurrency and pages are bounded; partial coverage is explicit.
func (s *session) Graph(parent context.Context, scope core.ScopeSel) (core.Graph, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	select {
	case s.graphGate <- struct{}{}:
		defer func() { <-s.graphGate }()
	case <-ctx.Done():
		return core.Graph{}, ctx.Err()
	}
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	snap := s.cat.snap()
	out := core.Graph{Nodes: []core.GraphNode{}, Edges: []core.GraphEdge{}, Problems: []core.GraphProblem{}, Discovery: snap.state, CapturedAt: time.Now().UnixMilli()}
	jobs := make([]graphJob, 0)
	for _, def := range snap.reg.list {
		if def.virtual {
			continue
		}
		namespaces := []string{""}
		if def.namespaced && scope.Mode != core.ScopeAll {
			namespaces = scope.SelectedNames()
		}
		for _, ns := range namespaces {
			jobs = append(jobs, graphJob{def, ns})
		}
	}
	if len(jobs) > 4096 {
		jobs = jobs[:4096]
		out.Truncated = true
	}
	var mu sync.Mutex
	records := make([]graphRecord, 0)
	queue := make(chan graphJob)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for job := range queue {
				jobCtx, done := context.WithTimeout(ctx, 20*time.Second)
				token := ""
				seen := map[string]bool{}
				for {
					mu.Lock()
					full := len(records) >= graphNodeLimit
					mu.Unlock()
					if full {
						mu.Lock()
						out.Truncated = true
						mu.Unlock()
						break
					}
					var list *unstructured.UnstructuredList
					var err error
					if s.graphMetadata != nil && (job.def == secretsKind || job.def == configMapsKind) {
						var objects *metav1.PartialObjectMetadataList
						objects, err = s.graphMetadata.Resource(job.def.gvr).Namespace(job.namespace).List(jobCtx, metav1.ListOptions{Limit: 500, Continue: token})
						if err == nil {
							list = &unstructured.UnstructuredList{}
							list.SetContinue(objects.GetContinue())
							for i := range objects.Items {
								object, convertErr := runtime.DefaultUnstructuredConverter.ToUnstructured(&objects.Items[i])
								if convertErr != nil {
									err = convertErr
									break
								}
								u := unstructured.Unstructured{Object: object}
								u.SetKind(kindOf(job.def))
								u.SetAPIVersion("v1")
								list.Items = append(list.Items, u)
							}
						}
					} else if job.def.namespaced {
						list, err = s.dyn.Resource(job.def.gvr).Namespace(job.namespace).List(jobCtx, metav1.ListOptions{Limit: 500, Continue: token})
					} else {
						list, err = s.dyn.Resource(job.def.gvr).List(jobCtx, metav1.ListOptions{Limit: 500, Continue: token})
					}
					if err != nil {
						class, _ := classify(err)
						mu.Lock()
						out.Problems = append(out.Problems, core.GraphProblem{Kind: job.def.desc.ID, Scope: job.namespace, Class: string(class)})
						mu.Unlock()
						break
					}
					batch := make([]graphRecord, 0, len(list.Items))
					for i := range list.Items {
						u := &list.Items[i]
						if job.def.namespaced && !scope.Contains(u.GetNamespace()) {
							continue
						}
						if job.def == namespacesKind && !scope.Contains(u.GetName()) {
							continue
						}
						if u.GetUID() == "" {
							continue
						}
						health := core.HealthUnknown
						if job.def.project != nil {
							_, h, _ := job.def.project(u, s.now())
							health = h.State
						}
						r := graphRecord{node: core.GraphNode{ID: string(u.GetUID()), Ref: s.ref(job.def, u.GetNamespace(), u.GetName(), string(u.GetUID())), KindTitle: kindOf(job.def), Health: health}, owners: u.GetOwnerReferences(), links: s.graphLinks(job.def, u)}
						if r.node.KindTitle == "" {
							r.node.KindTitle = job.def.desc.Singular
							if r.node.KindTitle == "" {
								r.node.KindTitle = job.def.desc.Title
							}
						}
						if job.def == podsKind {
							r.labels = u.GetLabels()
						}
						if job.def == servicesKind {
							r.selector, _, _ = unstructured.NestedStringMap(u.Object, "spec", "selector")
						}
						batch = append(batch, r)
					}
					mu.Lock()
					room := graphNodeLimit - len(records)
					if len(batch) > room {
						batch = batch[:room]
						out.Truncated = true
					}
					records = append(records, batch...)
					mu.Unlock()
					next := list.GetContinue()
					if next == "" {
						break
					}
					if seen[next] {
						mu.Lock()
						out.Truncated = true
						mu.Unlock()
						break
					}
					seen[next] = true
					token = next
				}
				done()
			}
		})
	}
	for _, job := range jobs {
		select {
		case queue <- job:
		case <-ctx.Done():
			close(queue)
			wg.Wait()
			return out, ctx.Err()
		}
	}
	close(queue)
	wg.Wait()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	buildGraph(&out, records)
	sort.Slice(out.Problems, func(i, j int) bool {
		a, b := out.Problems[i], out.Problems[j]
		return a.Kind+"/"+a.Scope < b.Kind+"/"+b.Scope
	})
	return out, nil
}

func graphKey(kind, ns, name string) string { return kind + "\x00" + ns + "\x00" + name }

// buildGraph uses identity/name indices and inverted Pod labels. It avoids
// pairwise comparisons of every resource in the cluster.
func buildGraph(out *core.Graph, records []graphRecord) {
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i].node.Ref, records[j].node.Ref
		return graphKey(a.Kind, a.Scope, a.Name) < graphKey(b.Kind, b.Scope, b.Name)
	})
	byID := make(map[string]graphRecord, len(records))
	byName := make(map[string]string, len(records))
	podLabels := map[string][]string{}
	for _, r := range records {
		if _, exists := byID[r.node.ID]; exists {
			continue
		}
		byID[r.node.ID] = r
		byName[graphKey(r.node.Ref.Kind, r.node.Ref.Scope, r.node.Ref.Name)] = r.node.ID
		out.Nodes = append(out.Nodes, r.node)
		for k, v := range r.labels {
			key := r.node.Ref.Scope + "\x00" + k + "\x00" + v
			podLabels[key] = append(podLabels[key], r.node.ID)
		}
	}
	seen := make(map[string]bool)
	add := func(from, to, typ string) {
		if to == "" || from == to {
			return
		}
		if _, ok := byID[to]; !ok {
			return
		}
		key := from + "\x00" + to + "\x00" + typ
		if seen[key] {
			return
		}
		seen[key] = true
		if len(out.Edges) >= graphEdgeLimit {
			out.Truncated = true
			return
		}
		out.Edges = append(out.Edges, core.GraphEdge{Source: from, Target: to, Type: typ})
	}
	for _, r := range records {
		for _, owner := range r.owners {
			id := string(owner.UID)
			if parent, ok := byID[id]; ok && parent.node.Ref.Name == owner.Name && parent.node.KindTitle == owner.Kind && (parent.node.Ref.Scope == "" || parent.node.Ref.Scope == r.node.Ref.Scope) {
				add(id, r.node.ID, "owns")
			}
		}
		for _, link := range r.links {
			id := byName[graphKey(link.Ref.Kind, link.Ref.Scope, link.Ref.Name)]
			if link.Ref.UID != "" && id != link.Ref.UID {
				continue
			}
			if link.Type == "attached-to" {
				add(id, r.node.ID, "routes-to")
			} else {
				add(r.node.ID, id, link.Type)
			}
		}
		if len(r.selector) > 0 {
			var candidates []string
			for k, v := range r.selector {
				bucket := podLabels[r.node.Ref.Scope+"\x00"+k+"\x00"+v]
				if len(bucket) == 0 {
					candidates = nil
					break
				}
				if candidates == nil || len(bucket) < len(candidates) {
					candidates = bucket
				}
			}
			match := labels.SelectorFromSet(r.selector)
			for _, id := range candidates {
				if match.Matches(labels.Set(byID[id].labels)) {
					add(r.node.ID, id, "selects")
				}
			}
		}
	}
}

func (s *session) graphLinks(def *kindDef, u *unstructured.Unstructured) []core.Relation {
	var links []core.Relation
	ns := u.GetNamespace()
	add := func(kind, namespace, name, typ string) {
		if kind != "" && name != "" {
			links = append(links, core.Relation{Type: typ, Ref: core.Ref{Kind: kind, Scope: namespace, Name: name}})
		}
	}
	named := func(apiVersion, kind, namespace, name, typ string) {
		add(s.kindFor(apiVersion, kind), namespace, name, typ)
	}
	pod := func(spec map[string]any) {
		add("nodes", "", str(spec, "nodeName"), "runs-on")
		named("v1", "ServiceAccount", ns, str(spec, "serviceAccountName"), "uses")
		for _, x := range slice(spec, "imagePullSecrets") {
			add("secrets", ns, str(x, "name"), "uses")
		}
		for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
			for _, c := range slice(spec, key) {
				for _, x := range slice(c, "envFrom") {
					add("configmaps", ns, str(x, "configMapRef", "name"), "uses")
					add("secrets", ns, str(x, "secretRef", "name"), "uses")
				}
				for _, x := range slice(c, "env") {
					add("configmaps", ns, str(x, "valueFrom", "configMapKeyRef", "name"), "uses")
					add("secrets", ns, str(x, "valueFrom", "secretKeyRef", "name"), "uses")
				}
			}
		}
		for _, v := range slice(spec, "volumes") {
			add("configmaps", ns, str(v, "configMap", "name"), "mounts")
			add("secrets", ns, str(v, "secret", "secretName"), "mounts")
			named("v1", "PersistentVolumeClaim", ns, str(v, "persistentVolumeClaim", "claimName"), "mounts")
			for _, x := range slice(v, "projected", "sources") {
				add("configmaps", ns, str(x, "configMap", "name"), "mounts")
				add("secrets", ns, str(x, "secret", "name"), "mounts")
			}
		}
	}
	spec, _, _ := unstructured.NestedMap(u.Object, "spec")
	if def == podsKind {
		pod(spec)
	}
	if template, ok := spec["template"].(map[string]any); ok {
		if p, ok := template["spec"].(map[string]any); ok {
			pod(p)
		}
	}
	if def == ingressesKind {
		add("services", ns, str(spec, "defaultBackend", "service", "name"), "routes-to")
		for _, r := range slice(spec, "rules") {
			for _, p := range slice(r, "http", "paths") {
				add("services", ns, str(p, "backend", "service", "name"), "routes-to")
			}
		}
		for _, tls := range slice(spec, "tls") {
			add("secrets", ns, str(tls, "secretName"), "uses")
		}
	}
	kind := kindOf(def)
	if def.discovered {
		kind = def.kind
	}
	if kind == "PersistentVolumeClaim" {
		named("v1", "PersistentVolume", "", str(spec, "volumeName"), "bound-to")
		named("storage.k8s.io/v1", "StorageClass", "", str(spec, "storageClassName"), "uses")
	}
	if kind == "PersistentVolume" {
		named("v1", "PersistentVolumeClaim", str(spec, "claimRef", "namespace"), str(spec, "claimRef", "name"), "bound-to")
	}
	if kind == "HorizontalPodAutoscaler" {
		named(str(spec, "scaleTargetRef", "apiVersion"), str(spec, "scaleTargetRef", "kind"), ns, str(spec, "scaleTargetRef", "name"), "scales")
	}
	if kind == "RoleBinding" || kind == "ClusterRoleBinding" {
		roleNS := ns
		if str(u.Object, "roleRef", "kind") == "ClusterRole" {
			roleNS = ""
		}
		named("rbac.authorization.k8s.io/v1", str(u.Object, "roleRef", "kind"), roleNS, str(u.Object, "roleRef", "name"), "grants")
		for _, sub := range slice(u.Object, "subjects") {
			if str(sub, "kind") == "ServiceAccount" {
				subjectNS := str(sub, "namespace")
				if subjectNS == "" {
					subjectNS = ns
				}
				named("v1", "ServiceAccount", subjectNS, str(sub, "name"), "grants")
			}
		}
	}
	if strings.HasSuffix(kind, "Route") && def.gvr.Group == "gateway.networking.k8s.io" {
		for _, p := range slice(spec, "parentRefs") {
			parentNS := str(p, "namespace")
			if parentNS == "" {
				parentNS = ns
			}
			parentKind := str(p, "kind")
			if (parentKind == "" || parentKind == "Gateway") && (str(p, "group") == "" || str(p, "group") == "gateway.networking.k8s.io") {
				named("gateway.networking.k8s.io/"+def.gvr.Version, "Gateway", parentNS, str(p, "name"), "attached-to")
			} else if parentKind == "Service" && str(p, "group") == "" {
				add("services", parentNS, str(p, "name"), "attached-to")
			}
		}
		for _, rule := range slice(spec, "rules") {
			for _, b := range slice(rule, "backendRefs") {
				backendNS := str(b, "namespace")
				if backendNS == "" {
					backendNS = ns
				}
				if (str(b, "kind") == "" || str(b, "kind") == "Service") && str(b, "group") == "" {
					add("services", backendNS, str(b, "name"), "routes-to")
				}
			}
		}
	}
	if def == eventsKind {
		r := eventSubject(s, u)
		if !r.Inert {
			links = append(links, r)
		}
	}
	return links
}
