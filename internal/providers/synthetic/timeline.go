package synthetic

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"time"
)

// Timeline uses the same selectable resources as the synthetic graph fixture.
func (s *session) Timeline(ctx context.Context, scope core.ScopeSel) (core.Timeline, error) {
	graph, err := s.Graph(ctx, scope)
	if err != nil {
		return core.Timeline{}, err
	}
	now := time.Now().UnixMilli()
	out := core.Timeline{Events: []core.TimelineEvent{}, Resources: []core.TimelineResource{}, Problems: []core.GraphProblem{}, Discovery: "ready", CapturedAt: now}
	for i, n := range graph.Nodes {
		out.Resources = append(out.Resources, core.TimelineResource{Ref: n.Ref, KindTitle: n.KindTitle})
		typ, reason, message := "Normal", "Started", "Container started"
		if i%2 == 0 {
			typ, reason, message = "Warning", "BackOff", "Back-off restarting failed container"
		}
		out.Events = append(out.Events, core.TimelineEvent{ID: "event-" + n.ID, Subject: n.Ref, SubjectKind: n.KindTitle, Openable: true, Type: typ, Reason: reason, Message: message, Count: int64(i + 1), FirstAt: now - int64(i+2)*60000, LastAt: now - int64(i)*10000})
	}
	return out, nil
}
