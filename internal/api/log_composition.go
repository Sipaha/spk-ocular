package api

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
	"k8s.io/apimachinery/pkg/util/validation"
)

// LogComposition is an immutable, memory-only set bound to one explicitly
// admitted connection. It does not extend the agent log catalogue or grants.
type LogComposition struct {
	Refs         []core.Ref `json:"refs"`
	ConfigRev    string     `json:"configRev"`
	ConnectionID uint64     `json:"connectionId"`
}
type ComposedLogStreamRequest struct {
	Composition LogComposition    `json:"composition"`
	Query       provider.LogQuery `json:"query"`
}

func validateLogComposition(c LogComposition) error {
	bad := func() error {
		return coded(CodeBadRequest, errors.New("composed logs require 2 to 8 unique UID-pinned Kubernetes Pod or workload roots on one explicit connection"))
	}
	if len(c.Refs) < 2 || len(c.Refs) > 8 || c.ConfigRev == "" || c.ConnectionID == 0 {
		return bad()
	}
	seen := map[string]bool{}
	for _, r := range c.Refs {
		if r.Provider != "kubernetes" || r.Target == "" || r.Target != c.Refs[0].Target || r.Scope == "" || r.Name == "" || r.UID == "" {
			return bad()
		}
		switch r.Kind {
		case "pods", "apps/deployments", "apps/replicasets", "apps/statefulsets", "apps/daemonsets":
		default:
			return bad()
		}
		for _, v := range []string{r.Target, r.Scope, r.Name, r.UID, r.Kind, c.ConfigRev} {
			if len(v) > 4096 || !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
				return bad()
			}
		}
		if len(validation.IsDNS1123Label(r.Scope)) > 0 || len(validation.IsDNS1123Subdomain(r.Name)) > 0 || len(r.UID) > 128 || strings.ContainsAny(r.UID, "/\\") {
			return bad()
		}
		key := r.Scope + "\x00" + r.UID
		if seen[key] {
			return bad()
		}
		seen[key] = true
	}
	return nil
}
func (s *Service) composedLogSource(ctx context.Context, c LogComposition) (*sessionEntry, provider.ComposedLogSource, error) {
	if !fromUI(ctx) {
		return nil, nil, coded("forbidden", errors.New("composed logs are UI-only"))
	}
	if err := validateLogComposition(c); err != nil {
		return nil, nil, err
	}
	e, err := s.sessionFor(ctx, c.Refs[0].Provider, c.Refs[0].Target)
	if err != nil {
		return nil, nil, err
	}
	s.sessMu.Lock()
	err = s.checkLogCompositionLocked(e, c)
	s.sessMu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	src, ok := e.sess.(provider.ComposedLogSource)
	if !ok {
		return nil, nil, coded(CodeUnsupported, errors.New("this target has no composed logs"))
	}
	return e, src, nil
}
func (s *Service) checkLogCompositionLocked(e *sessionEntry, c LogComposition) error {
	a := s.connections[ownerKey(e.provider, e.target)]
	if s.sessions[ownerKey(e.provider, e.target)] != e || a == nil || a.status.ID != c.ConnectionID || a.owner != e.owner || a.status.State != "connected" || s.configRev(e.hash) != c.ConfigRev {
		return coded(CodeConflict, errors.New("the composed log connection was replaced; select sources again"))
	}
	return nil
}
func (s *Service) OpenComposedLogStream(ctx context.Context, req ComposedLogStreamRequest) (LogStreamInfo, error) {
	q := req.Query
	if q.Channel != provider.ChannelAll || q.Previous || q.TailLines == 0 || q.TailLines < provider.TailAll || len(q.Filter) > 2000 {
		return LogStreamInfo{}, coded(CodeBadRequest, errors.New("composed logs require all containers and a valid tail/filter"))
	}
	c := req.Composition
	c.Refs = slices.Clone(c.Refs)
	e, src, err := s.composedLogSource(ctx, c)
	if err != nil {
		return LogStreamInfo{}, err
	}
	id, err := s.registerStreamChecked(e, func() error { return s.checkLogCompositionLocked(e, c) }, func(ctx context.Context, out *streams.Writer) error {
		return src.StreamComposedLogs(ctx, c.Refs, q, streams.LogSink{W: out})
	})
	return LogStreamInfo{StreamID: id}, err
}
func (s *Service) OpenComposedLogHistory(ctx context.Context, c LogComposition) (LogStreamInfo, error) {
	c.Refs = slices.Clone(c.Refs)
	e, src, err := s.composedLogSource(ctx, c)
	if err != nil {
		return LogStreamInfo{}, err
	}
	id, err := s.registerStreamChecked(e, func() error { return s.checkLogCompositionLocked(e, c) }, func(ctx context.Context, out *streams.Writer) error {
		ctx, cancel := context.WithTimeout(ctx, logHistoryDuration)
		defer cancel()
		check := func() error {
			if err := src.CheckComposedLogRoots(ctx, c.Refs); err != nil {
				return logHistoryReadError(err)
			}
			return nil
		}
		if err := check(); err != nil {
			return err
		}
		sink := &historyLogSink{ctx: ctx, out: streams.LogSink{W: out}, labels: map[int]string{}, maxBytes: LogHistoryMaxBytes, maxLines: LogHistoryMaxLines}
		if err := src.StreamComposedLogs(ctx, c.Refs, provider.LogQuery{Archive: true, Channel: provider.ChannelAll, TailLines: provider.TailAll}, sink); err != nil {
			return logHistoryReadError(err)
		}
		if err := ctx.Err(); err != nil {
			return logHistoryReadError(err)
		}
		if err := check(); err != nil {
			return err
		}
		s.sessMu.Lock()
		err := s.checkLogCompositionLocked(e, c)
		s.sessMu.Unlock()
		if err != nil {
			return err
		}
		if len(sink.labels) <= len(c.Refs) {
			return incompleteLogHistory()
		}
		return nil
	})
	return LogStreamInfo{StreamID: id}, err
}
