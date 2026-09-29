package kubernetes

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// getTimeout bounds one details request (the object plus its relations).
const getTimeout = 15 * time.Second

// maxRelated caps each relation list: a selector matching thousands of pods
// must not turn the details panel into a table.
const maxRelated = 200

// Get returns the full object: YAML (without managedFields and the
// last-applied annotation; Secret values masked), facts, health and
// relations. Relations are best effort: a denied or slow relation lookup is
// reported in RelationsError and never fails the resource itself.
func (s *session) Get(ctx context.Context, ref core.Ref) (*core.Resource, error) {
	def := s.kinds.byID[ref.Kind]
	if def == nil {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", ref.Kind)}
	}
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	res := s.dyn.Resource(def.gvr)
	var u *unstructured.Unstructured
	var err error
	if def.namespaced {
		u, err = res.Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
	} else {
		u, err = res.Get(ctx, ref.Name, metav1.GetOptions{})
	}
	if err != nil {
		class, msg := classify(err)
		return nil, &provider.Error{Class: class, Message: msg}
	}
	if ref.UID != "" && string(u.GetUID()) != ref.UID {
		// Same name, another object: never show it as the one clicked.
		return nil, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s was deleted and a new object took its name", ref)}
	}

	full := u.DeepCopy()
	unstructured.RemoveNestedField(full.Object, "metadata", "managedFields")
	unstructured.RemoveNestedField(full.Object, "metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration")
	if ann := full.GetAnnotations(); ann != nil && len(ann) == 0 {
		unstructured.RemoveNestedField(full.Object, "metadata", "annotations")
	}
	if def == secretsKind {
		maskSecret(full.Object)
	}
	y, err := yaml.Marshal(full.Object)
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}

	// Health and table facts come from the same projection the list uses.
	proj := u.DeepCopy()
	if def.pre != nil {
		def.pre(proj)
	}
	cells, health, _ := def.project(trim(proj, def.keep), s.now())
	out := &core.Resource{
		Ref:    core.Ref{Provider: ProviderID, Target: s.target, Scope: u.GetNamespace(), Kind: def.desc.ID, Name: u.GetName(), UID: string(u.GetUID())},
		Health: health,
		Facts:  facts(def, u, cells),
		YAML:   string(y),
	}
	rels, truncated, relErr := s.relations(ctx, def, u)
	out.Relations = rels
	out.RelationsTruncated = truncated
	if relErr != nil {
		out.RelationsError = relErr.Error()
	}
	return out, nil
}

// maskSecret replaces Secret values by their size: reading a Secret's
// details must not put credentials on screen (revealing is a separate,
// deliberate action — backlog).
func maskSecret(o map[string]any) {
	for _, field := range []string{"data", "stringData"} {
		m, ok := o[field].(map[string]any)
		if !ok {
			continue
		}
		for k, v := range m {
			s, _ := v.(string)
			n := len(s)
			if field == "data" { // base64
				if b, err := base64.StdEncoding.DecodeString(s); err == nil {
					n = len(b)
				}
			}
			m[k] = fmt.Sprintf("<%d bytes>", n)
		}
	}
}

// facts: the kind's table cells (except name/scope/metrics), then labels.
func facts(def *kindDef, u *unstructured.Unstructured, cells []core.Cell) []core.Detail {
	out := []core.Detail{{Key: "kind", Value: str(u.Object, "kind")}}
	if u.GetNamespace() != "" {
		out = append(out, core.Detail{Key: "namespace", Value: u.GetNamespace()})
	}
	out = append(out, core.Detail{Key: "created", Value: u.GetCreationTimestamp().UTC().Format(time.RFC3339)})
	for i, c := range def.desc.Columns {
		if i == 0 || c.ScopeColumn || c.Metric || c.Type == core.ColAge || i >= len(cells) {
			continue
		}
		if v := cells[i].Text; v != "" {
			out = append(out, core.Detail{Key: c.Title, Value: v})
		}
	}
	if def == podsKind {
		for _, c := range slice(u.Object, "spec", "containers") {
			out = append(out, core.Detail{Key: "container " + str(c, "name"), Value: str(c, "image")})
		}
	}
	if l := u.GetLabels(); len(l) > 0 {
		keys := make([]string, 0, len(l))
		for k := range l {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+l[k])
		}
		out = append(out, core.Detail{Key: "labels", Value: strings.Join(parts, "\n")})
	}
	return out
}

// kindFor maps an apiVersion+Kind (owner references, backends) to our
// kind id; "" when Ocular does not show that kind.
func kindFor(apiVersion, kind string) string {
	group := ""
	if i := strings.Index(apiVersion, "/"); i >= 0 {
		group = apiVersion[:i]
	}
	for _, d := range allKinds.list {
		if d.gvr.Group == group && strings.EqualFold(kindOf(d), kind) {
			return d.desc.ID
		}
	}
	return ""
}

// kindOf is the singular Kind of a kindDef (Pods → Pod).
func kindOf(d *kindDef) string {
	switch d {
	case podsKind:
		return "Pod"
	case deploymentsKind:
		return "Deployment"
	case statefulSetsKind:
		return "StatefulSet"
	case daemonSetsKind:
		return "DaemonSet"
	case replicaSetsKind:
		return "ReplicaSet"
	case servicesKind:
		return "Service"
	case ingressesKind:
		return "Ingress"
	case configMapsKind:
		return "ConfigMap"
	case secretsKind:
		return "Secret"
	case nodesKind:
		return "Node"
	case namespacesKind:
		return "Namespace"
	case eventsKind:
		return "Event"
	}
	return ""
}

func (s *session) ref(kind *kindDef, ns, name, uid string) core.Ref {
	return core.Ref{Provider: ProviderID, Target: s.target, Scope: ns, Kind: kind.desc.ID, Name: name, UID: uid}
}

// relations: owners up; owned pods down (by controller UID, not labels
// alone); service → pods by selector (none without one); ingress →
// services; pod → node.
func (s *session) relations(ctx context.Context, def *kindDef, u *unstructured.Unstructured) ([]core.Relation, bool, error) {
	var out []core.Relation
	var errs []error
	var trunc bool
	ns := u.GetNamespace()
	for _, o := range u.GetOwnerReferences() {
		r := core.Ref{Provider: ProviderID, Target: s.target, Scope: ns, Kind: kindFor(o.APIVersion, o.Kind), Name: o.Name, UID: string(o.UID)}
		if r.Kind == "" {
			r.Kind = strings.ToLower(o.Kind) // shown, not openable
		}
		out = append(out, core.Relation{Type: "owner", Ref: r})
	}
	switch def {
	case deploymentsKind:
		rss, err := s.owned(ctx, replicaSetsKind, u, &trunc)
		errs = append(errs, err)
		for _, rs := range rss {
			if desiredReplicas(rs.Object) == 0 && i64(rs.Object, "status", "replicas") == 0 {
				continue // old revisions scaled to zero
			}
			out = append(out, core.Relation{Type: "owns", Ref: s.ref(replicaSetsKind, ns, rs.GetName(), string(rs.GetUID()))})
			pods, err := s.owned(ctx, podsKind, &rs, &trunc)
			errs = append(errs, err)
			out = append(out, podRelations(s, pods)...)
		}
	case statefulSetsKind, daemonSetsKind, replicaSetsKind:
		pods, err := s.owned(ctx, podsKind, u, &trunc)
		errs = append(errs, err)
		out = append(out, podRelations(s, pods)...)
	case servicesKind:
		if sel, _, _ := unstructured.NestedStringMap(u.Object, "spec", "selector"); len(sel) > 0 {
			pods, err := s.list(ctx, podsKind, ns, labels.SelectorFromSet(sel).String(), &trunc, nil)
			errs = append(errs, err)
			for _, p := range pods {
				out = append(out, core.Relation{Type: "selects", Ref: s.ref(podsKind, ns, p.GetName(), string(p.GetUID()))})
			}
		}
	case ingressesKind:
		seen := map[string]bool{}
		add := func(name string) {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, core.Relation{Type: "routes-to", Ref: s.ref(servicesKind, ns, name, "")})
			}
		}
		add(str(u.Object, "spec", "defaultBackend", "service", "name"))
		for _, r := range slice(u.Object, "spec", "rules") {
			for _, p := range slice(r, "http", "paths") {
				add(str(p, "backend", "service", "name"))
			}
		}
	case podsKind:
		if n := str(u.Object, "spec", "nodeName"); n != "" {
			out = append(out, core.Relation{Type: "runs-on", Ref: s.ref(nodesKind, "", n, "")})
		}
	}
	return out, trunc, errors.Join(errs...)
}

func podRelations(s *session, pods []unstructured.Unstructured) []core.Relation {
	out := make([]core.Relation, 0, len(pods))
	for _, p := range pods {
		out = append(out, core.Relation{Type: "owns", Ref: s.ref(podsKind, p.GetNamespace(), p.GetName(), string(p.GetUID()))})
	}
	return out
}

// owned lists objects of kind controlled by owner: narrowed by the owner's
// label selector when it has one, then filtered by controller UID.
func (s *session) owned(ctx context.Context, kind *kindDef, owner *unstructured.Unstructured, trunc *bool) ([]unstructured.Unstructured, error) {
	selector := ""
	if m, ok, _ := unstructured.NestedMap(owner.Object, "spec", "selector"); ok {
		var ls metav1.LabelSelector
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &ls); err == nil {
			if sel, err := metav1.LabelSelectorAsSelector(&ls); err == nil {
				selector = sel.String()
			}
		}
	}
	return s.list(ctx, kind, owner.GetNamespace(), selector, trunc, func(it *unstructured.Unstructured) bool {
		c := metav1.GetControllerOfNoCopy(it)
		return c != nil && c.UID == owner.GetUID()
	})
}

// listPage is the server page size for relation lookups.
const listPage = 500

// list follows continuation pages until maxRelated objects pass keep (nil:
// all) or the list ends; *trunc is set when more may exist. An expired
// continue token ends the walk with what was found (trunc set).
func (s *session) list(ctx context.Context, kind *kindDef, ns, selector string, trunc *bool, keep func(*unstructured.Unstructured) bool) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured
	opts := metav1.ListOptions{LabelSelector: selector, Limit: listPage}
	for {
		l, err := s.dyn.Resource(kind.gvr).Namespace(ns).List(ctx, opts)
		if err != nil {
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
				*trunc = true
				return out, nil
			}
			class, msg := classify(err)
			return out, fmt.Errorf("%s: %s (%s)", kind.desc.Title, msg, class)
		}
		for i := range l.Items {
			if keep != nil && !keep(&l.Items[i]) {
				continue
			}
			if len(out) == maxRelated {
				*trunc = true
				return out, nil
			}
			out = append(out, l.Items[i])
		}
		if l.GetContinue() == "" {
			return out, nil
		}
		opts.Continue = l.GetContinue()
	}
}
