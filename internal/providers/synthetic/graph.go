package synthetic

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"time"
)

// Graph uses actual synthetic resources so selection exercises the normal
// details, YAML, edits and live-object identity path, without external systems.
func (s *session) Graph(ctx context.Context, scope core.ScopeSel) (core.Graph, error) {
	out := core.Graph{Nodes: []core.GraphNode{}, Edges: []core.GraphEdge{}, Problems: []core.GraphProblem{}, Discovery: "ready", CapturedAt: time.Now().UnixMilli()}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	for _, c := range allCrates {
		if !scope.Contains(c.zone) {
			continue
		}
		ref := core.Ref{Provider: ID, Target: s.target, Kind: CrateKind, Scope: c.zone, Name: c.key, Title: c.title, UID: c.key}
		out.Nodes = append(out.Nodes, core.GraphNode{ID: ref.UID, Ref: ref, KindTitle: "Crate", Health: core.HealthOK})
	}
	for _, name := range s.order {
		ref := s.ref(name)
		out.Nodes = append(out.Nodes, core.GraphNode{ID: ref.UID, Ref: ref, KindTitle: "Service", Health: core.HealthOK})
	}
	for _, n := range out.Nodes {
		if n.Ref.Kind == CrateKind {
			out.Edges = append(out.Edges, core.GraphEdge{Source: "uid-api", Target: n.ID, Type: "selects"})
		}
	}
	return out, nil
}
