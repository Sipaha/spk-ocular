package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/offlineaudit"
)

type OfflineAuditRequest struct {
	Command   string `json:"command"`
	ID        string `json:"id,omitempty"`
	Version   string `json:"version,omitempty"`
	Target    string `json:"target,omitempty"`
	ConfigRev string `json:"configRev,omitempty"`
	Data      []byte `json:"data,omitempty"`
}
type OfflineAuditResponse struct {
	Reports  []offlineaudit.Summary `json:"reports"`
	Selected *offlineaudit.Stored   `json:"selected,omitempty"`
}

// OfflineAudit accepts only explicit local report uploads. Target metadata is
// an operator assertion of context, never a source identity or read authority.
func (s *Service) OfflineAudit(ctx context.Context, r OfflineAuditRequest) (OfflineAuditResponse, error) {
	if !fromUI(ctx) {
		return OfflineAuditResponse{}, coded("forbidden", errors.New("offline audits are available only to the application UI"))
	}
	bad := func() (OfflineAuditResponse, error) {
		return OfflineAuditResponse{}, coded(CodeBadRequest, offlineaudit.ErrInvalid)
	}
	if len(r.ID) > 32 || len(r.Data) > offlineaudit.MaxBytes || len(r.Version) > 16 || len(r.Target) > 1024 || len(r.ConfigRev) > 256 {
		return bad()
	}
	var selected *offlineaudit.Stored
	var err error
	switch r.Command {
	case "import":
		if r.ID != "" || !offlineaudit.ValidContext(r.Target, r.ConfigRev) || !s.offlineAuditContext(ctx, r.Target, r.ConfigRev) {
			return bad()
		}
		var report offlineaudit.Stored
		report, err = s.offlineAudits.Import(ctx, r.Data, r.Version, r.Target, r.ConfigRev)
		if err == nil {
			if !s.offlineAuditContext(ctx, r.Target, r.ConfigRev) {
				_ = s.offlineAudits.Forget(report.Summary.ID)
				return bad()
			}
			selected = &report
		}
	case "status", "get", "forget":
		if len(r.Data) != 0 || r.Version != "" || r.Target != "" || r.ConfigRev != "" {
			return bad()
		}
		switch r.Command {
		case "status":
			if r.ID != "" {
				return bad()
			}
		case "get":
			var report offlineaudit.Stored
			report, err = s.offlineAudits.Get(r.ID)
			if err == nil {
				selected = &report
			}
		case "forget":
			err = s.offlineAudits.Forget(r.ID)
		}
	default:
		return bad()
	}
	if err != nil {
		return OfflineAuditResponse{}, coded(CodeBadRequest, err)
	}
	return OfflineAuditResponse{Reports: s.offlineAudits.List(), Selected: selected}, nil
}
func (s *Service) offlineAuditContext(ctx context.Context, target, revision string) bool {
	p, ok := s.reg.Get("kubernetes")
	if !ok {
		return false
	}
	discovered, err := p.Discover(ctx)
	if err != nil {
		return false
	}
	for _, t := range discovered.Targets {
		if t.ID == target && s.configRev(t.ConfigHash) == revision {
			return true
		}
	}
	return false
}
