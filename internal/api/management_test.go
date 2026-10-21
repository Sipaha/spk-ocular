package api

import (
	"encoding/json"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

func TestManagementReviewContextIsUIOnlyAndDoesNotChangeEditToken(t *testing.T) {
	s, k := newEditService(t)
	ui := UIContext(t.Context())
	_, err := s.ConnectTarget(ui, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	doc, err := s.GetEditSource(ui, cfgRef)
	require.NoError(t, err)
	k.management = &core.ManagementContext{Ref: doc.Ref, Version: "8", CapturedAt: 1000, ArgoTracking: &core.ArgoTracking{Application: "app", Method: "label"}}
	req := EditPrepareRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "edited"}
	pl, err := s.PrepareEdit(ui, req)
	require.NoError(t, err)
	require.Equal(t, k.management, pl.Management)
	require.NotEmpty(t, pl.Token)
	// AgentCall strips a UI marker even when its parent was created by the UI.
	agent, err := s.AgentCall(ui, "k", "a")
	require.NoError(t, err)
	defer agent.Done()
	hidden, err := s.PrepareEdit(agent.Context(), req)
	require.NoError(t, err)
	require.Nil(t, hidden.Management)
	b, err := json.Marshal(hidden)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"management"`)
	// Advisory display data is never an input to RunEdit or its signed grant.
	pl.Management.ArgoTracking.Application = "changed-display"
	_, err = s.RunEdit(ui, EditRunRequest{Ref: req.Ref, Base: req.Base, Original: req.Original, Edited: req.Edited, Token: pl.Token})
	require.NoError(t, err)
	require.Len(t, k.runs, 1)
}

func TestManagementReviewContextOnlyForUIScale(t *testing.T) {
	s, k := newActionService(t)
	k.management = &core.ManagementContext{Ref: deployRef, Version: "8", CapturedAt: 1000, FluxTracking: &core.FluxTracking{Candidates: []core.FluxManagerHint{{Kind: "Kustomization", Namespace: "gitops", Name: "delivery"}}}}
	ui := UIContext(t.Context())
	_, err := s.ConnectTarget(ui, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	req := ActionRequest{Ref: deployRef, Action: "scale", Params: core.ActionParams{Count: intp(3)}}
	pl, err := s.PrepareAction(ui, req)
	require.NoError(t, err)
	require.Equal(t, k.management, pl.Management)
	agent, err := s.AgentCall(ui, "k", "a")
	require.NoError(t, err)
	defer agent.Done()
	hidden, err := s.PrepareAction(agent.Context(), req)
	require.NoError(t, err)
	require.Nil(t, hidden.Management)
	b, err := json.Marshal(hidden)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"management"`)
	req.Action = "restart"
	req.Params = core.ActionParams{}
	other, err := s.PrepareAction(ui, req)
	require.NoError(t, err)
	require.Nil(t, other.Management)
	require.Empty(t, k.ran(), "preparing and filtering metadata never sends a run")
}
