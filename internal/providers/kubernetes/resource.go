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
	def := s.kind(ref.Kind)
	if def == nil || def.virtual {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", ref.Kind)}
	}
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	var u *unstructured.Unstructured
	var fs []core.Detail
	var health core.Health
	var err error
	if def.discovered && s.tables != nil {
		u, fs, err = s.getTable(ctx, def, ref)
		if u != nil {
			health = genericHealth(u)
			delete(u.Object, cellsField)
		}
	}
	if u == nil && err == nil { // a described kind, or no Tables for the resource
		res := s.dyn.Resource(def.gvr)
		if def.namespaced {
			u, err = res.Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
		} else {
			u, err = res.Get(ctx, ref.Name, metav1.GetOptions{})
		}
		if err == nil {
			// Health and table facts come from the same projection the list uses.
			proj := u.DeepCopy()
			if def.pre != nil {
				def.pre(proj)
			}
			var cells []core.Cell
			cells, health, _ = def.project(trim(proj, def.keep), s.now())
			fs = facts(def, u, cells)
		}
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

	out := &core.Resource{
		Ref:    core.Ref{Provider: ProviderID, Target: s.target, Scope: u.GetNamespace(), Kind: def.desc.ID, Name: u.GetName(), UID: string(u.GetUID())},
		Health: health,
		Facts:  fs,
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

// factsHead: what every object's facts begin with.
func factsHead(u *unstructured.Unstructured) []core.Detail {
	out := []core.Detail{{Key: "kind", Value: str(u.Object, "kind")}}
	if u.GetNamespace() != "" {
		out = append(out, core.Detail{Key: "namespace", Value: u.GetNamespace()})
	}
	return append(out, core.Detail{Key: "created", Value: u.GetCreationTimestamp().UTC().Format(time.RFC3339)})
}

// labelFacts: the labels as one fact.
func labelFacts(u *unstructured.Unstructured) []core.Detail {
	l := u.GetLabels()
	if len(l) == 0 {
		return nil
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+l[k])
	}
	return []core.Detail{{Key: "labels", Value: strings.Join(parts, "\n")}}
}

// facts: the kind's table cells (except name/scope/metrics), then labels.
func facts(def *kindDef, u *unstructured.Unstructured, cells []core.Cell) []core.Detail {
	out := factsHead(u)
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
	return append(out, labelFacts(u)...)
}

// getTable reads one object of a discovered kind as a Table: facts are all
// the server's columns, the wide ones too. The answer's columns are checked
// against the kind's current schema (other columns end its epoch; the
// facts themselves follow the answer). nil, nil: no Table for the resource
// (the plain read follows).
func (s *session) getTable(ctx context.Context, def *kindDef, ref core.Ref) (*unstructured.Unstructured, []core.Detail, error) {
	ns := ""
	if def.namespaced {
		ns = ref.Scope
	}
	cols, row, err := s.tables.get(ctx, def.gvr, ns, ref.Name)
	s.schemas.mu.Lock()
	cur := s.schemas.entry(def.gvr).cur
	s.schemas.mu.Unlock()
	switch {
	case servesNoTables(err):
		if cur != nil && cur.table {
			go s.tablesUnsupported(cur)
		}
		return nil, nil, nil
	case errors.Is(err, errNotTable):
		return nil, nil, nil
	case err != nil:
		return nil, nil, err
	}
	if cur != nil && cur.table && !cur.matches(cols) {
		go s.schemaChanged(cur)
	}
	age := map[int]bool{}
	if cur != nil && cur.matches(cols) {
		age = cur.age
	}
	fs := factsHead(row)
	named := false
	cells, _ := row.Object[cellsField].([]any)
	for i, c := range cols {
		if c.Format == "name" && !named {
			named = true
			continue
		}
		if age[i] || i >= len(cells) {
			continue // the creation time is a fact already
		}
		if v := tableCell(c.Type, cells[i]).Text; v != "" {
			fs = append(fs, core.Detail{Key: c.Name, Value: v})
		}
	}
	return row, append(fs, labelFacts(row)...), nil
}

// kindFor maps an apiVersion+Kind (owner references, involved objects) to
// a kind of the session's catalog; "" when Ocular does not show that kind.
func (s *session) kindFor(apiVersion, kind string) string {
	group := ""
	if i := strings.Index(apiVersion, "/"); i >= 0 {
		group = apiVersion[:i]
	}
	for _, d := range s.cat.snap().reg.list {
		if d.virtual || d.gvr.Group != group {
			continue
		}
		k := kindOf(d)
		if d.discovered {
			k = d.kind
		}
		if strings.EqualFold(k, kind) {
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
// services; pod → node; event → its object (by UID).
func (s *session) relations(ctx context.Context, def *kindDef, u *unstructured.Unstructured) ([]core.Relation, bool, error) {
	var out []core.Relation
	var errs []error
	var trunc bool
	ns := u.GetNamespace()
	for _, o := range u.GetOwnerReferences() {
		r := core.Ref{Provider: ProviderID, Target: s.target, Scope: ns, Kind: s.kindFor(o.APIVersion, o.Kind), Name: o.Name, UID: string(o.UID)}
		inert := r.Kind == ""
		if inert {
			r.Kind = strings.ToLower(o.Kind) // shown, not openable
		}
		out = append(out, core.Relation{Type: "owner", Ref: r, Inert: inert})
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
	case eventsKind:
		out = append(out, eventSubject(s, u))
	case podsKind:
		if n := str(u.Object, "spec", "nodeName"); n != "" {
			out = append(out, core.Relation{Type: "runs-on", Ref: s.ref(nodesKind, "", n, "")})
		}
	}
	return out, trunc, errors.Join(errs...)
}

// eventSubject: what the event is about, by UID — opening it shows that
// object or "no longer exists", never a same-named replacement. Without a
// UID, or of a kind Ocular does not show, it is named, not openable.
func eventSubject(s *session, u *unstructured.Unstructured) core.Relation {
	o := u.Object
	kind := str(o, "involvedObject", "kind")
	r := core.Ref{Provider: ProviderID, Target: s.target, Scope: str(o, "involvedObject", "namespace"),
		Kind: s.kindFor(str(o, "involvedObject", "apiVersion"), kind), Name: str(o, "involvedObject", "name"), UID: str(o, "involvedObject", "uid")}
	inert := r.Kind == "" || r.UID == ""
	if r.Kind == "" {
		r.Kind = strings.ToLower(kind)
	}
	return core.Relation{Type: "about", Ref: r, Inert: inert}
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
