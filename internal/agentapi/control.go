package agentapi

import (
	"context"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/store"
)

// What the UI manages (api.AgentControl).

func (s *Server) Grants(ctx context.Context) ([]agentgrant.Target, error) {
	out, err := s.o.Store.AgentTargets(ctx)
	if err != nil {
		return nil, &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	for i := range out {
		if out[i].Grants == nil {
			out[i].Grants = []agentgrant.Grant{}
		}
	}
	return out, nil
}

// SaveGrants sets a target's grants whole. A target granted anew is bound
// to the identity it has now (Docker: its daemon's id — asked of it).
func (s *Server) SaveGrants(ctx context.Context, req api.SaveAgentGrantsRequest) error {
	for _, g := range req.Grants {
		if err := g.Validate(); err != nil {
			return badRequest("%v", err)
		}
	}
	t := agentgrant.Target{Provider: req.Provider, Target: req.Target, Grants: req.Grants}
	if len(req.Grants) > 0 {
		call, err := s.o.Service.AgentCall(ctx, req.Provider, req.Target)
		if err != nil {
			return err
		}
		id, err := call.Identity()
		t.Title = call.Title()
		call.Done()
		if err != nil {
			return err
		}
		t.Identity = id
	}
	if err := s.o.Store.ReplaceAgentGrants(ctx, t); err != nil {
		return badRequest("%v", err)
	}
	s.o.Service.Emit(api.EventAgentGrantsChanged, "", nil)
	return nil
}

func (s *Server) RevokeAll(ctx context.Context) error {
	if err := s.o.Store.RevokeAllAgentGrants(ctx); err != nil {
		return &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	s.o.Service.Emit(api.EventAgentGrantsChanged, "", nil)
	return nil
}

func (s *Server) Reconfirm(ctx context.Context, req api.ReconfirmAgentTargetRequest) error {
	ok, err := s.o.Store.ReconfirmAgentTarget(ctx, req.Provider, req.Target, req.Observed)
	if err != nil {
		return &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	if !ok {
		s.o.Service.Emit(api.EventAgentGrantsChanged, "", nil) // show what it is now
		return &api.CodedError{Code: api.CodeConflict, Detail: "the target changed again since it was shown: look at what it points at now"}
	}
	s.o.Service.Emit(api.EventAgentGrantsChanged, "", nil)
	return nil
}

func (s *Server) Pending(context.Context) ([]api.AgentPending, error) { return s.pend.list(), nil }

func (s *Server) Decide(_ context.Context, req api.DecideAgentPendingRequest) error {
	return s.pend.decide(req.ID, req.Approve)
}

func (s *Server) Audit(ctx context.Context, f store.AuditFilter) ([]store.AuditEntry, error) {
	out, err := s.o.Store.ListAudit(ctx, f)
	if err != nil {
		return nil, &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	return out, nil
}
