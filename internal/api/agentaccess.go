package api

import (
	"context"
	"errors"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/mcpclientconfig"
	"github.com/spk/spk-ocular/internal/store"
)

// Agent access as the UI manages it (P14): the grants, the plans waiting
// for the user's confirmation, the journal, the MCP endpoint's state. The agent MCP server
// (internal/agentapi) implements AgentControl; without one (tests,
// a process that does not serve MCP) these methods say unsupported.
type AgentControl interface {
	Status(ctx context.Context) (AgentAccessStatus, error)
	Grants(ctx context.Context) ([]agentgrant.Target, error)
	SaveGrants(ctx context.Context, req SaveAgentGrantsRequest) error
	RevokeAll(ctx context.Context) error
	Reconfirm(ctx context.Context, req ReconfirmAgentTargetRequest) error
	Pending(ctx context.Context) ([]AgentPending, error)
	Decide(ctx context.Context, req DecideAgentPendingRequest) error
	Audit(ctx context.Context, f store.AuditFilter) ([]store.AuditEntry, error)
}

// Agent MCP server states (AgentAccessStatus.State).
const (
	AgentServing       = "serving"
	AgentOtherInstance = "other_instance"
	AgentFailed        = "failed"
)

type AgentAccessStatus struct {
	State string `json:"state"`
	// Endpoint is the MCP URL; Instruction describes agent connection setup.
	Endpoint string `json:"endpoint"`
	// Client configurations contain a bearer credential: show only in the trusted manual setup UI; never log them.
	CodexConfig  string `json:"codexConfig,omitempty"`
	ClaudeConfig string `json:"claudeConfig,omitempty"`
	Instruction  string `json:"instruction"`
	Error        string `json:"error,omitempty"`
	// Pending: plans waiting for the user's confirmation.
	Pending int `json:"pending"`
}

// SaveAgentGrantsRequest replaces a target's grants, groups and master switches (all empty: revoked).
type SaveAgentGrantsRequest struct {
	Provider       string             `json:"provider"`
	Target         string             `json:"target"`
	Grants         []agentgrant.Grant `json:"grants"`
	Groups         []agentgrant.Group `json:"groups,omitempty"`
	DisabledScopes []agentgrant.Scope `json:"disabledScopes,omitempty"`
}

// ReconfirmAgentTargetRequest grants a suspended target's grants for the
// identity the user was shown (Observed), not whatever it is by the click.
type ReconfirmAgentTargetRequest struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Observed string `json:"observed"`
}

// AgentPending is an agent's destructive plan waiting for the user.
type AgentPending struct {
	ID string `json:"id"`
	// Agent is the name the agent gave (not verified).
	Agent       string    `json:"agent"`
	At          time.Time `json:"at"`
	Expires     time.Time `json:"expires"`
	Provider    string    `json:"provider"`
	Target      string    `json:"target"`
	TargetTitle string    `json:"targetTitle"`
	Ref         core.Ref  `json:"ref"`
	// Action or Edit is the plan as the agent was shown it.
	Action *core.ActionPlan `json:"action,omitempty"`
	Edit   *core.EditPlan   `json:"edit,omitempty"`
	Exec   *AgentExecPlan   `json:"exec,omitempty"`
}

type DecideAgentPendingRequest struct {
	ID      string `json:"id"`
	Approve bool   `json:"approve"`
}

// Agent access events: reload the grants / the pending plans / the journal.
const (
	EventAgentGrantsChanged  = "agent_grants_changed"
	EventAgentPendingChanged = "agent_pending_changed"
	EventAgentAuditChanged   = "agent_audit_changed"
)

// MaxAuditPage bounds one ListAgentAudit page.
const MaxAuditPage = 500

// SetAgentControl connects the agent MCP server (before the UI starts).
func (s *Service) SetAgentControl(c AgentControl) { s.agent = c }

// Emit sends an event to the UI (the agent MCP server's changes).
func (s *Service) Emit(typ, key string, payload map[string]any) {
	s.em.Emit(events.Event{Type: typ, Key: key, Payload: payload})
}

func (s *Service) agentControl() (AgentControl, error) {
	if s.agent == nil {
		return nil, coded(CodeUnsupported, errors.New("agent access is not served by this process"))
	}
	return s.agent, nil
}

func (s *Service) AgentAccessStatus(ctx context.Context) (AgentAccessStatus, error) {
	c, err := s.agentControl()
	if err != nil {
		return AgentAccessStatus{}, err
	}
	return c.Status(ctx)
}

func (s *Service) ListAgentGrants(ctx context.Context) ([]agentgrant.Target, error) {
	c, err := s.agentControl()
	if err != nil {
		return nil, err
	}
	out, err := c.Grants(ctx)
	if out == nil && err == nil {
		out = []agentgrant.Target{}
	}
	return out, err
}

func (s *Service) SaveAgentGrants(ctx context.Context, req SaveAgentGrantsRequest) error {
	c, err := s.agentControl()
	if err != nil {
		return err
	}
	return c.SaveGrants(ctx, req)
}

func (s *Service) RevokeAllAgentGrants(ctx context.Context) error {
	c, err := s.agentControl()
	if err != nil {
		return err
	}
	return c.RevokeAll(ctx)
}

func (s *Service) ReconfirmAgentTarget(ctx context.Context, req ReconfirmAgentTargetRequest) error {
	c, err := s.agentControl()
	if err != nil {
		return err
	}
	return c.Reconfirm(ctx, req)
}

func (s *Service) ListAgentPending(ctx context.Context) ([]AgentPending, error) {
	c, err := s.agentControl()
	if err != nil {
		return nil, err
	}
	out, err := c.Pending(ctx)
	if out == nil && err == nil {
		out = []AgentPending{}
	}
	return out, err
}

func (s *Service) DecideAgentPending(ctx context.Context, req DecideAgentPendingRequest) error {
	c, err := s.agentControl()
	if err != nil {
		return err
	}
	return c.Decide(ctx, req)
}

func (s *Service) ListAgentAudit(ctx context.Context, f store.AuditFilter) ([]store.AuditEntry, error) {
	c, err := s.agentControl()
	if err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > MaxAuditPage {
		f.Limit = MaxAuditPage
	}
	out, err := c.Audit(ctx, f)
	if out == nil && err == nil {
		out = []store.AuditEntry{}
	}
	return out, err
}

// AgentClientSetup is UI-only; it never runs a client or grants agent access.
type AgentClientSetupRequest struct {
	Client   string `json:"client,omitempty"`
	Install  bool   `json:"install,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type AgentClientSetupView struct {
	Clients []mcpclientconfig.Status `json:"clients"`
}

func (s *Service) AgentClientSetup(ctx context.Context, req AgentClientSetupRequest) (AgentClientSetupView, error) {
	if !fromUI(ctx) {
		return AgentClientSetupView{}, coded("forbidden", errors.New("client setup is available only to the application UI"))
	}
	if (req.Client != "" && req.Client != "codex" && req.Client != "claude") || len(req.Revision) > 64 || (req.Install && req.Client == "") {
		return AgentClientSetupView{}, coded(CodeBadRequest, errors.New("invalid client setup request"))
	}
	control, err := s.agentControl()
	if err != nil {
		return AgentClientSetupView{}, err
	}
	status, err := control.Status(ctx)
	if err != nil {
		return AgentClientSetupView{}, err
	}
	if status.State != AgentServing {
		return AgentClientSetupView{}, coded(CodeUnsupported, errors.New("MCP access is not available"))
	}
	wanted := map[string]string{"codex": status.CodexConfig, "claude": status.ClaudeConfig}
	if req.Install {
		_, err = s.agentClientConfigs.Install(ctx, req.Client, wanted[req.Client], req.Revision)
		if errors.Is(err, mcpclientconfig.ErrChanged) {
			return AgentClientSetupView{}, coded(CodeConflict, err)
		}
		if err != nil {
			return AgentClientSetupView{}, coded(CodeBadRequest, mcpclientconfig.ErrUnavailable)
		}
	}
	result := AgentClientSetupView{Clients: make([]mcpclientconfig.Status, 0, 2)}
	for _, client := range []string{"codex", "claude"} {
		result.Clients = append(result.Clients, s.agentClientConfigs.Inspect(client, wanted[client]))
	}
	return result, nil
}
