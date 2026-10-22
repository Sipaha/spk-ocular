package synthetic

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"time"
)

// Synthetic findings have selectable fixture references and no external scans.
func (s *session) Security(ctx context.Context, scope core.ScopeSel, options core.SecurityOptions) (core.SecuritySnapshot, error) {
	if options.IncludeClusterReports || options.PolicyReportAPI != "" {
		return core.SecuritySnapshot{}, errors.New("cluster audit reports are not available in this synthetic fixture")
	}
	graph, err := s.Graph(ctx, scope)
	if err != nil {
		return core.SecuritySnapshot{}, err
	}
	out := core.SecuritySnapshot{Findings: []core.SecurityFinding{}, Problems: []core.GraphProblem{}, Discovery: "ready", CVEState: "missing", CapturedAt: time.Now().UnixMilli(), Resources: len(graph.Nodes)}
	for i, node := range graph.Nodes {
		out.Findings = append(out.Findings, core.SecurityFinding{ID: node.ID + "/privileged", Ref: node.Ref, KindTitle: node.KindTitle, Category: "configuration", Severity: "critical", Rule: "privileged", Container: "main", Evidence: "securityContext.privileged=true"})
		if i == 0 {
			out.Findings = append(out.Findings, core.SecurityFinding{ID: node.ID + "/root", Ref: node.Ref, KindTitle: node.KindTitle, Category: "configuration", Severity: "high", Rule: "root", Container: "main", Evidence: "runAsNonRoot / runAsUser"})
		}
	}
	if len(graph.Nodes) == 0 {
		out.CVEState = "not-requested"
	}
	return out, nil
}
