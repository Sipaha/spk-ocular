package api

import (
	"errors"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// AgentExecInfo uses the call's admitted session; it does not borrow UI handles.
func (c *AgentCall) AgentExecInfo(ref core.Ref) (core.ExecInfo, error) {
	ex, ok := c.e.sess.(provider.Execer)
	if !ok {
		return core.ExecInfo{}, coded(CodeUnsupported, errors.New("commands cannot run on this target"))
	}
	info, err := ex.ExecInfo(c.ctx, ref)
	if err != nil {
		return core.ExecInfo{}, c.fail(err)
	}
	if info.Instances == nil {
		info.Instances = []core.ExecInstance{}
	}
	for i := range info.Instances {
		if info.Instances[i].Channels == nil {
			info.Instances[i].Channels = []core.ExecChannel{}
		}
	}
	return info, nil
}

// PrepareAgentExec pins an explicitly selected instance on this call's session.
// The owner closes the handle. The caller must check grants before invoking it.
func (c *AgentCall) PrepareAgentExec(ref core.Ref, req provider.ExecRequest, revision string) (provider.ExecHandle, core.LiveTarget, error) {
	if c.ctx.Err() != nil {
		return nil, core.LiveTarget{}, c.fail(c.ctx.Err())
	}
	rev := c.s.configRev(c.e.sess.ConfigHash())
	if rev == "" {
		return nil, core.LiveTarget{}, coded(CodeUnsupported, errors.New("this target has no connection revision for reviewed exec"))
	}
	if revision != "" && revision != rev {
		return nil, core.LiveTarget{}, coded(CodeConflict, errors.New("the connection changed; prepare exec again"))
	}
	ex, ok := c.e.sess.(provider.Execer)
	if !ok {
		return nil, core.LiveTarget{}, coded(CodeUnsupported, errors.New("commands cannot run on this target"))
	}
	h, err := ex.PrepareExec(c.ctx, ref, req)
	if err != nil {
		return nil, core.LiveTarget{}, c.fail(err)
	}
	where := c.s.live(h.Describe())
	if where.Provider != ref.Provider || where.Target != ref.Target || where.Ref.Provider != ref.Provider || where.Ref.Target != ref.Target || where.Ref.Scope != ref.Scope || where.Ref.Kind != ref.Kind || where.Ref.Name != ref.Name || where.Ref.UID != ref.UID {
		h.Close()
		return nil, core.LiveTarget{}, coded(CodeConflict, errors.New("the prepared command resolved to a different object"))
	}
	if where.ConfigRev != rev || c.ctx.Err() != nil {
		h.Close()
		return nil, core.LiveTarget{}, coded(CodeConflict, errors.New("the connection changed while preparing exec"))
	}
	return h, where, nil
}

// AgentExecPlan is the exact in-memory command shown for human confirmation.
// It is not stored in the audit journal or returned as a UI terminal handle.
type AgentExecPlan struct {
	Where      core.LiveTarget `json:"where"`
	Instance   string          `json:"instance"`
	Channel    string          `json:"channel,omitempty"`
	Command    []string        `json:"command"`
	Stdin      string          `json:"stdin,omitempty"`
	MaxBytes   int             `json:"maxBytes"`
	MaxSeconds int             `json:"maxSeconds"`
}
