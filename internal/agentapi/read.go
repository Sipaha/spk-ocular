package agentapi

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// callTimeout bounds an agent's read as a whole (a var for tests);
// fanOut bounds the scopes read at once.
var callTimeout = 30 * time.Second

const fanOut = 4

// Limits of a list.
const (
	defaultLimit = 200
	maxLimit     = 500
	maxRefs      = 100
)

// TargetRef names a target (Access lists them).
type TargetRef struct {
	Provider string `json:"provider" jsonschema:"required" jsonschema_description:"kubernetes or compose (Access lists the targets)."`
	Target   string `json:"target" jsonschema:"required" jsonschema_description:"The target's id (a kube context, a Docker context)."`
}

type AccessRequest struct{}

type AccessView struct {
	Targets []AccessTarget `json:"targets"`
	// Confirmation says how destructive plans are confirmed.
	Confirmation string `json:"confirmation"`
}

type AccessTarget struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Title    string `json:"title"`
	// State: active, or suspended (the target points elsewhere than when
	// granted: the user must confirm it again in Ocular).
	State  string             `json:"state"`
	Grants []agentgrant.Grant `json:"grants" jsonschema_description:"Each grant: a verb (read, logs, edit, action:<id>) in a scope (one namespace, all namespaces, or cluster = objects outside namespaces, read only) for kinds (null: all kinds; editing Secrets, ServiceAccounts and RBAC and anything destructive need the kind named). noConfirm: destructive plans run without the user's confirmation."`
}

func (s *Server) readMethods() {
	register(s, "Access", "none", false, "What the user granted agents: per target, the scopes, verbs and kinds. Empty until the user grants something in SPK Ocular.", s.access)
	register(s, "ListKinds", agentgrant.VerbRead, false, "The kinds of a target an agent may use, with their columns and the actions granted.", s.listKinds)
	register(s, "ListObjects", agentgrant.VerbRead, false, "The objects of a kind, read once: in one namespace, or (without scope) in every namespace granted. Rows are sorted by namespace and name; at most limit (≤ 500), truncated says there were more.", s.listObjects)
	register(s, "GetObject", agentgrant.VerbRead, false, "One object's details: facts, YAML (Secret values are never shown), relations (those to objects not granted are hidden and counted).", s.getObject)
	register(s, "Problems", agentgrant.VerbRead, false, "What is wrong in the namespaces granted (and outside namespaces with a cluster grant): failing pods, unavailable workloads, recent warning events.", s.problems)
	register(s, "GetMetrics", agentgrant.VerbRead, false, "CPU and memory usage of objects (pods, nodes, containers) by their refs (≤ 100).", s.getMetrics)
	register(s, "GetLogs", agentgrant.VerbLogs, false, "The last lines of an object's logs (never followed): tailLines (default 200, ≤ 5000), since a time, or the previous container's.", s.getLogs)
}

func (s *Server) access(ctx context.Context, _ caller, _ *AccessRequest) (*AccessView, error) {
	all, err := s.o.Store.AgentTargets(ctx)
	if err != nil {
		return nil, &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	out := &AccessView{Targets: []AccessTarget{}, Confirmation: "A destructive plan (delete, scale to 0, a destructive edit) waits for the user's confirmation in the SPK Ocular window unless its grant has noConfirm; ask GetRun for the outcome and tell your user to confirm."}
	for _, t := range all {
		st := "active"
		if t.Suspended() {
			st = "suspended"
		}
		out.Targets = append(out.Targets, AccessTarget{Provider: t.Provider, Target: t.Target, Title: t.Title, State: st, Grants: t.Grants})
	}
	return out, nil
}

type ListKindsRequest struct {
	TargetRef
}

type KindView struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Singular string       `json:"singular,omitempty"`
	Scoped   bool         `json:"scoped" jsonschema_description:"Objects live in namespaces (projects)."`
	Columns  []ColumnView `json:"columns"`
	Logs     bool         `json:"logs,omitempty"`
	Editable bool         `json:"editable,omitempty"`
	Verbs    []string     `json:"verbs" jsonschema_description:"The verbs granted for this kind in some scope."`
	Actions  []ActionView `json:"actions,omitempty"`
}

type ColumnView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type ActionView struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Destructive bool              `json:"destructive,omitempty" jsonschema_description:"Always destructive: granted only with the kind named, confirmed by the user unless noConfirm."`
	Param       *core.ActionParam `json:"param,omitempty"`
}

type KindsView struct {
	Kinds []KindView `json:"kinds"`
}

// applies: verb means something for kind k.
func applies(k core.KindDescriptor, verb string) bool {
	switch verb {
	case agentgrant.VerbRead:
		return true
	case agentgrant.VerbLogs:
		return k.Logs
	case agentgrant.VerbEdit:
		return k.Editable
	}
	id := strings.TrimPrefix(verb, agentgrant.ActionPrefix)
	return slices.ContainsFunc(k.Actions, func(a core.ActionDescriptor) bool { return a.ID == id })
}

// verbsOf: the verbs granted for kind k (in any scope) that apply to it.
func verbsOf(gs []agentgrant.Grant, k core.KindDescriptor) []string {
	var out []string
	for _, g := range gs {
		if !applies(k, g.Verb) {
			continue
		}
		if g.Kinds != nil && !slices.Contains(g.Kinds, k.ID) {
			continue
		}
		if k.Scoped == (g.Scope.Mode == agentgrant.ScopeCluster) {
			continue
		}
		if g.Kinds == nil && g.Verb == agentgrant.VerbEdit && k.Sensitive {
			continue
		}
		if !slices.Contains(out, g.Verb) {
			out = append(out, g.Verb)
		}
	}
	sort.Strings(out)
	return out
}

func columnsOf(k core.KindDescriptor) []ColumnView {
	out := []ColumnView{}
	for _, c := range k.Columns {
		if !c.Metric {
			out = append(out, ColumnView{ID: c.ID, Title: c.Title, Type: string(c.Type)})
		}
	}
	return out
}

func (s *Server) listKinds(ctx context.Context, c caller, req *ListKindsRequest) (*KindsView, error) {
	x, err := s.open(ctx, c, "ListKinds", req.Provider, req.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	out := &KindsView{Kinds: []KindView{}}
	for _, k := range x.call.Kinds() {
		verbs := verbsOf(x.grants.Grants, k)
		if len(verbs) == 0 {
			continue
		}
		kv := KindView{ID: k.ID, Title: k.Title, Singular: k.Singular, Scoped: k.Scoped, Columns: columnsOf(k), Logs: k.Logs, Editable: k.Editable, Verbs: verbs}
		for _, a := range k.Actions {
			if slices.Contains(verbs, agentgrant.ActionVerb(a.ID)) && k.Scoped && !a.NoAgents {
				kv.Actions = append(kv.Actions, ActionView{ID: a.ID, Title: a.Title, Destructive: a.Destructive, Param: a.Param})
			}
		}
		out.Kinds = append(out.Kinds, kv)
	}
	s.read(c, "ListKinds", req.Provider, req.Target, "", "")
	return out, nil
}

type ListObjectsRequest struct {
	TargetRef
	Kind  string `json:"kind" jsonschema:"required" jsonschema_description:"A kind id from ListKinds (pods, apps/deployments, ...)."`
	Scope string `json:"scope,omitempty" jsonschema_description:"A namespace (project); empty: every namespace granted. Kinds outside namespaces take none."`
	Name  string `json:"name,omitempty" jsonschema_description:"Only objects whose name contains this (case-insensitive)."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"At most this many rows (default 200, ≤ 500)."`
}

type RowView struct {
	Ref    core.Ref    `json:"ref"`
	Cells  []string    `json:"cells" jsonschema_description:"Values in the order of columns."`
	Health core.Health `json:"health"`
}

// ScopeState says how one scope was read.
type ScopeState struct {
	Name string `json:"name"`
	// State: ready, stale (the last known rows: the connection was lost),
	// timeout (not read in time; the rows are what came), error.
	State   string `json:"state"`
	Class   string `json:"class,omitempty"`
	Message string `json:"message,omitempty"`
}

type ObjectsView struct {
	Kind      string       `json:"kind"`
	Columns   []ColumnView `json:"columns"`
	Rows      []RowView    `json:"rows"`
	Truncated bool         `json:"truncated,omitempty"`
	Scopes    []ScopeState `json:"scopes"`
	// Coverage: a view built from several sources says how each was seen.
	Coverage []provider.SourceCoverage `json:"coverage,omitempty"`
}

// cellText renders a cell for an agent.
func cellText(c core.Cell) string {
	switch {
	case c.Text != "":
		return c.Text
	case c.Num != nil:
		return strconv.FormatFloat(*c.Num, 'f', -1, 64)
	case c.Time != 0:
		return time.UnixMilli(c.Time).UTC().Format(time.RFC3339)
	}
	return ""
}

func rowView(k core.KindDescriptor, r core.Row) RowView {
	cells := make([]string, 0, len(r.Cells))
	for i, c := range r.Cells {
		if i < len(k.Columns) && k.Columns[i].Metric {
			continue
		}
		cells = append(cells, cellText(c))
	}
	return RowView{Ref: r.Ref, Cells: cells, Health: r.Health}
}

// scopeRead is one scope's snapshot.
type scopeRead struct {
	state ScopeState
	snap  api.Snapshot
}

func stateOf(name string, snap api.Snapshot, err error) ScopeState {
	st := ScopeState{Name: name}
	switch {
	case err != nil:
		ce := errCoded(err)
		st.State, st.Class, st.Message = "error", ce.Code, ce.Detail
		if ce.Code == api.CodeLimit {
			st.State = "timeout"
		}
	case snap.Status.State == provider.StatusLoading:
		st.State = "timeout"
	default:
		st.State = string(snap.Status.State)
		st.Class, st.Message = string(snap.Status.Class), snap.Status.Message
	}
	return st
}

// readScopes snapshots kind in each of scopes (at most fanOut at once,
// all within the call's time); a scope that errs or runs late is said in
// its state, the rest answer.
func readScopes(x *session, kind string, scopes []core.ScopeSel) []scopeRead {
	out := make([]scopeRead, len(scopes))
	sem := make(chan struct{}, fanOut)
	var wg sync.WaitGroup
	for i, sc := range scopes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-x.call.Context().Done():
				out[i].state = ScopeState{Name: sc.Name, State: "timeout"}
				return
			}
			snap, err := x.call.Snapshot(api.SnapshotRequest{Kind: kind, Scope: sc})
			out[i] = scopeRead{state: stateOf(sc.Name, snap, err), snap: snap}
		}()
	}
	wg.Wait()
	return out
}

// selectors are the scopes to read of a scoped kind: the one asked (its
// grant checked), or those granted (all: one ScopeAll).
func (s *Server) selectors(c caller, x *session, method string, k core.KindDescriptor, scope string, readable func(string) ([]string, bool)) ([]core.ScopeSel, bool, error) {
	if !k.Scoped {
		if scope != "" {
			return nil, false, badRequest("%s objects live outside namespaces: leave scope empty", k.ID)
		}
		if _, err := s.check(c, x, method, k, core.Ref{Provider: x.grants.Provider, Target: x.grants.Target, Kind: k.ID}, agentgrant.VerbRead, false); err != nil {
			return nil, false, err
		}
		return []core.ScopeSel{{Mode: core.ScopeNone}}, false, nil
	}
	if scope != "" {
		if _, err := s.check(c, x, method, k, core.Ref{Provider: x.grants.Provider, Target: x.grants.Target, Scope: scope, Kind: k.ID}, agentgrant.VerbRead, false); err != nil {
			return nil, false, err
		}
		return []core.ScopeSel{{Mode: core.ScopeOne, Name: scope}}, false, nil
	}
	names, all := readable(k.ID)
	if all {
		return []core.ScopeSel{{Mode: core.ScopeAll}}, true, nil
	}
	if len(names) == 0 {
		err := forbidden("reading %s is granted in no namespace of this target", k.ID)
		s.refused(c, method, x.grants.Provider, x.grants.Target, "", k.ID, err)
		return nil, false, err
	}
	sels := make([]core.ScopeSel, 0, len(names))
	for _, n := range names {
		sels = append(sels, core.ScopeSel{Mode: core.ScopeOne, Name: n})
	}
	return sels, false, nil
}

// allScopesRefused: reading every namespace needs a cluster-wide list.
func allScopesRefused(reads []scopeRead) error {
	if len(reads) == 1 && reads[0].state.Class == string(provider.ClassForbidden) {
		return forbidden("the target refused to list every namespace (%s): grant the namespaces by name instead of all", reads[0].state.Message)
	}
	return nil
}

func limitOf(n int) (int, error) {
	switch {
	case n == 0:
		return defaultLimit, nil
	case n < 0 || n > maxLimit:
		return 0, badRequest("limit must be 1..%d", maxLimit)
	}
	return n, nil
}

func (s *Server) listObjects(ctx context.Context, c caller, req *ListObjectsRequest) (*ObjectsView, error) {
	limit, err := limitOf(req.Limit)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	x, err := s.open(ctx, c, "ListObjects", req.Provider, req.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(req.Kind)
	if err != nil {
		return nil, err
	}
	sels, all, err := s.selectors(c, x, "ListObjects", k, req.Scope, func(kind string) ([]string, bool) {
		return agentgrant.ReadableScopes(x.grants.Grants, kind)
	})
	if err != nil {
		return nil, err
	}
	reads := readScopes(x, k.ID, sels)
	if all {
		if err := allScopesRefused(reads); err != nil {
			return nil, err
		}
	}
	out := &ObjectsView{Kind: k.ID, Columns: columnsOf(k), Rows: []RowView{}, Scopes: []ScopeState{}}
	if len(sels) == 1 && reads[0].state.State == "error" && reads[0].snap.Rows == nil {
		return nil, &api.CodedError{Code: reads[0].state.Class, Detail: reads[0].state.Message}
	}
	name := trimLower(req.Name)
	var rows []core.Row
	for _, r := range reads {
		out.Scopes = append(out.Scopes, r.state)
		if r.snap.Kind.ID != "" {
			k = r.snap.Kind // the described columns
			out.Columns = columnsOf(k)
			out.Coverage = r.snap.Status.Coverage
		}
		for _, row := range r.snap.Rows {
			if !x.readable(row.Ref) {
				continue
			}
			if name != "" && !strings.Contains(strings.ToLower(row.Ref.Name), name) && !strings.Contains(strings.ToLower(row.Ref.Title), name) {
				continue
			}
			rows = append(rows, row)
		}
	}
	sortRows(rows)
	for i, row := range rows {
		if i == limit {
			out.Truncated = true
			break
		}
		out.Rows = append(out.Rows, rowView(k, row))
	}
	s.read(c, "ListObjects", req.Provider, req.Target, req.Scope, k.ID)
	return out, nil
}

func sortRows(rows []core.Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Ref, rows[j].Ref
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Name < b.Name
	})
}

type GetObjectRequest struct {
	Ref core.Ref `json:"ref" jsonschema:"required" jsonschema_description:"The object's ref as ListObjects gave it (provider, target, scope, kind, name; uid pins the incarnation)."`
}

type ObjectView struct {
	Ref                core.Ref        `json:"ref"`
	Health             core.Health     `json:"health"`
	Facts              []core.Detail   `json:"facts"`
	YAML               string          `json:"yaml"`
	Relations          []core.Relation `json:"relations"`
	HiddenRelations    int             `json:"hiddenRelations,omitempty" jsonschema_description:"Relations to objects not granted, left out."`
	RelationsError     string          `json:"relationsError,omitempty"`
	RelationsTruncated bool            `json:"relationsTruncated,omitempty"`
}

func (s *Server) getObject(ctx context.Context, c caller, req *GetObjectRequest) (*ObjectView, error) {
	ref := req.Ref
	x, err := s.open(ctx, c, "GetObject", ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(ref.Kind)
	if err != nil {
		return nil, err
	}
	if _, err := s.check(c, x, "GetObject", k, ref, agentgrant.VerbRead, false); err != nil {
		return nil, err
	}
	res, err := s.o.Service.GetResource(x.call.Context(), ref)
	if err != nil {
		return nil, err
	}
	// The object found must be the one judged: a provider must not answer
	// a ref with an object of another scope, and if one did, it is not shown.
	if res.Ref.Scope != ref.Scope || !x.readable(res.Ref) {
		err := forbidden("%s is not an object of %q", objectOf(ref), ref.Scope)
		s.refused(c, "GetObject", ref.Provider, ref.Target, ref.Scope, objectOf(ref), err)
		return nil, err
	}
	out := &ObjectView{Ref: res.Ref, Health: res.Health, Facts: res.Facts, YAML: res.YAML, Relations: []core.Relation{}, RelationsError: res.RelationsError, RelationsTruncated: res.RelationsTruncated}
	if out.Facts == nil {
		out.Facts = []core.Detail{}
	}
	for _, rel := range res.Relations {
		if x.readable(rel.Ref) {
			out.Relations = append(out.Relations, rel)
		} else {
			out.HiddenRelations++
		}
	}
	s.read(c, "GetObject", ref.Provider, ref.Target, ref.Scope, objectOf(ref))
	return out, nil
}

type ProblemsRequest struct {
	TargetRef
	Scope string `json:"scope,omitempty" jsonschema_description:"A namespace; empty: every namespace granted."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"At most this many rows (default 200, ≤ 500)."`
}

// problemsKind is the kind of the Problems view (kubernetes, synthetic).
const problemsKind = "problems"

// anyReadScopes: namespaces where anything may be read.
func anyReadScopes(gs []agentgrant.Grant) ([]string, bool) {
	var names []string
	all := false
	for _, g := range gs {
		if g.Verb != agentgrant.VerbRead {
			continue
		}
		switch g.Scope.Mode {
		case agentgrant.ScopeAll:
			all = true
		case agentgrant.ScopeOne:
			if !slices.Contains(names, g.Scope.Name) {
				names = append(names, g.Scope.Name)
			}
		}
	}
	sort.Strings(names)
	return names, all
}

func (s *Server) problems(ctx context.Context, c caller, req *ProblemsRequest) (*ObjectsView, error) {
	limit, err := limitOf(req.Limit)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	x, err := s.open(ctx, c, "Problems", req.Provider, req.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(problemsKind)
	if err != nil {
		return nil, err
	}
	var sels []core.ScopeSel
	all := false
	switch {
	case !k.Scoped:
		sels = []core.ScopeSel{{Mode: core.ScopeNone}}
	case req.Scope != "":
		if names, all := anyReadScopes(x.grants.Grants); !all && !slices.Contains(names, req.Scope) {
			err := forbidden("reading in %s is not granted", req.Scope)
			s.refused(c, "Problems", req.Provider, req.Target, req.Scope, "", err)
			return nil, err
		}
		sels = []core.ScopeSel{{Mode: core.ScopeOne, Name: req.Scope}}
	default:
		var names []string
		names, all = anyReadScopes(x.grants.Grants)
		switch {
		case all:
			sels = []core.ScopeSel{{Mode: core.ScopeAll}}
		case len(names) > 0:
			for _, n := range names {
				sels = append(sels, core.ScopeSel{Mode: core.ScopeOne, Name: n})
			}
		case slices.ContainsFunc(x.grants.Grants, func(g agentgrant.Grant) bool {
			return g.Verb == agentgrant.VerbRead && g.Scope.Mode == agentgrant.ScopeCluster
		}):
			// Only objects outside namespaces: their sources alone.
			sels = []core.ScopeSel{{Mode: core.ScopeNone}}
		default:
			err := forbidden("reading is not granted on this target")
			s.refused(c, "Problems", req.Provider, req.Target, "", "", err)
			return nil, err
		}
	}
	reads := readScopes(x, k.ID, sels)
	if all {
		if err := allScopesRefused(reads); err != nil {
			return nil, err
		}
	}
	out := &ObjectsView{Kind: k.ID, Columns: columnsOf(k), Rows: []RowView{}, Scopes: []ScopeState{}}
	seen := map[string]bool{}
	var rows []core.Row
	for _, r := range reads {
		out.Scopes = append(out.Scopes, r.state)
		if r.snap.Kind.ID != "" {
			out.Coverage = r.snap.Status.Coverage
		}
		for _, row := range r.snap.Rows {
			if seen[row.ID] || !x.readable(row.Ref) {
				continue // a node's row comes with every namespace
			}
			seen[row.ID] = true
			rows = append(rows, row)
		}
	}
	sortRows(rows)
	for i, row := range rows {
		if i == limit {
			out.Truncated = true
			break
		}
		out.Rows = append(out.Rows, rowView(k, row))
	}
	s.read(c, "Problems", req.Provider, req.Target, req.Scope, "")
	return out, nil
}

type GetMetricsRequest struct {
	Refs []core.Ref `json:"refs" jsonschema:"required" jsonschema_description:"Objects of one target (≤ 100) as ListObjects gave them."`
}

type MetricItem struct {
	Ref   core.Ref       `json:"ref"`
	Usage provider.Usage `json:"usage"`
}

type MetricsView struct {
	Items []MetricItem `json:"items"`
	// Missing: refs without a sample (no such object now, or no usage).
	Missing []core.Ref `json:"missing,omitempty"`
	// Errors: per kind and namespace, why usage could not be read.
	Errors []string `json:"errors,omitempty"`
	Window string   `json:"window,omitempty"`
}

func (s *Server) getMetrics(ctx context.Context, c caller, req *GetMetricsRequest) (*MetricsView, error) {
	if len(req.Refs) == 0 || len(req.Refs) > maxRefs {
		return nil, badRequest("refs: 1..%d objects", maxRefs)
	}
	p, t := req.Refs[0].Provider, req.Refs[0].Target
	for _, r := range req.Refs {
		if r.Provider != p || r.Target != t {
			return nil, badRequest("refs of one target only")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	x, err := s.open(ctx, c, "GetMetrics", p, t)
	if err != nil {
		return nil, err
	}
	defer x.done()
	type group struct {
		kind  core.KindDescriptor
		scope string
		refs  []core.Ref
	}
	groups := map[[2]string]*group{}
	var order [][2]string
	for _, r := range req.Refs {
		k, err := x.kind(r.Kind)
		if err != nil {
			return nil, err
		}
		if _, err := s.check(c, x, "GetMetrics", k, r, agentgrant.VerbRead, false); err != nil {
			return nil, err
		}
		key := [2]string{r.Kind, r.Scope}
		if groups[key] == nil {
			groups[key] = &group{kind: k, scope: r.Scope}
			order = append(order, key)
		}
		groups[key].refs = append(groups[key].refs, r)
	}
	out := &MetricsView{Items: []MetricItem{}}
	for _, key := range order {
		g := groups[key]
		sel := core.ScopeSel{Mode: core.ScopeNone}
		if g.kind.Scoped {
			sel = core.ScopeSel{Mode: core.ScopeOne, Name: g.scope}
		}
		snap, err := x.call.Snapshot(api.SnapshotRequest{Kind: g.kind.ID, Scope: sel})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s %s: %s", g.kind.ID, g.scope, detailOf(err)))
			out.Missing = append(out.Missing, g.refs...)
			continue
		}
		byRef := map[int]string{}
		var ids []string
		for i, r := range g.refs {
			for _, row := range snap.Rows {
				if row.Ref.Name == r.Name && (r.UID == "" || row.Ref.UID == r.UID) {
					byRef[i] = row.ID
					ids = append(ids, row.ID)
					break
				}
			}
		}
		mv, err := x.call.Metrics(snap, ids)
		if err != nil {
			return nil, err
		}
		if mv.Status != "ok" {
			out.Errors = append(out.Errors, fmt.Sprintf("%s %s: %s %s", g.kind.ID, g.scope, mv.Status, mv.Message))
		}
		out.Window = mv.Window
		for i, r := range g.refs {
			u, ok := mv.Values[byRef[i]]
			if !ok {
				out.Missing = append(out.Missing, r)
				continue
			}
			out.Items = append(out.Items, MetricItem{Ref: r, Usage: u})
		}
	}
	s.read(c, "GetMetrics", p, t, "", "")
	return out, nil
}

type GetLogsRequest struct {
	Ref       core.Ref  `json:"ref" jsonschema:"required" jsonschema_description:"A pod, workload or container (its kind has logs in ListKinds)."`
	Channel   string    `json:"channel,omitempty" jsonschema_description:"A container of a pod; empty: the default one."`
	Previous  bool      `json:"previous,omitempty" jsonschema_description:"The previous (crashed) container's logs."`
	TailLines int       `json:"tailLines,omitempty" jsonschema_description:"Lines per source (default 200, ≤ 5000)."`
	SinceTime time.Time `json:"sinceTime,omitzero" jsonschema_description:"Only lines since this time (RFC 3339)."`
}

func (s *Server) getLogs(ctx context.Context, c caller, req *GetLogsRequest) (*api.Tail, error) {
	ref := req.Ref
	x, err := s.open(ctx, c, "GetLogs", ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	defer x.done()
	k, err := x.kind(ref.Kind)
	if err != nil {
		return nil, err
	}
	if !k.Logs {
		return nil, &api.CodedError{Code: api.CodeUnsupported, Detail: fmt.Sprintf("%s have no logs", k.ID)}
	}
	if _, err := s.check(c, x, "GetLogs", k, ref, agentgrant.VerbLogs, false); err != nil {
		return nil, err
	}
	n := req.TailLines
	if n == 0 {
		n = defaultLimit
	}
	t, err := x.call.TailLogs(api.TailRequest{Ref: ref, Channel: req.Channel, Previous: req.Previous, TailLines: n, SinceTime: req.SinceTime})
	if err != nil {
		return nil, err
	}
	s.read(c, "GetLogs", ref.Provider, ref.Target, ref.Scope, objectOf(ref))
	return &t, nil
}
