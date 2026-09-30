package api

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var cfgRef = core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "configmaps", Name: "cfg"}

// editSession edits configmaps and records what the API hands it.
type editSession struct {
	*fakeSession
	o *editOpenable
}

func (e *editSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{{ID: "configmaps", Title: "ConfigMaps", Scoped: true, Editable: true}}
}

func (e *editSession) EditSource(_ context.Context, ref core.Ref) (core.EditDoc, provider.EditBase, error) {
	ref.UID = "uid-cfg"
	return core.EditDoc{Ref: ref, Text: "text"}, provider.EditBase{Route: "r", Name: ref.Name, Namespace: ref.Scope, UID: "uid-cfg", Version: "7", DocHash: "h", Format: 1}, nil
}

func (e *editSession) PrepareEdit(_ context.Context, req provider.EditRequest) (core.EditPlan, *provider.EditGrant, error) {
	e.o.mu.Lock()
	e.o.prepared = append(e.o.prepared, req)
	noGrant := e.o.noGrant
	e.o.mu.Unlock()
	plan := core.EditPlan{Where: core.LiveTarget{Provider: "k", Target: e.target, ConfigHash: e.hash, Ref: req.Ref}, Changed: true}
	if noGrant {
		return plan, nil, nil
	}
	return plan, &provider.EditGrant{Route: "r", Name: "cfg", Namespace: "ns", UID: "uid-cfg", Version: "8", PatchHash: "p", Mode: "checked"}, nil
}

func (e *editSession) RunEdit(_ context.Context, run provider.EditRun) (core.EditResult, error) {
	e.o.mu.Lock()
	defer e.o.mu.Unlock()
	e.o.runs = append(e.o.runs, run)
	return core.EditResult{Message: "written"}, nil
}

type editOpenable struct {
	*openable
	mu       sync.Mutex
	prepared []provider.EditRequest
	runs     []provider.EditRun
	noGrant  bool
}

func (o *editOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	return &editSession{fakeSession: s.(*fakeSession), o: o}, nil
}

func newEditService(t *testing.T) (*Service, *editOpenable) {
	t.Helper()
	k := &editOpenable{openable: newOpenable("a", "b")}
	k.setHash("a", "ha")
	s, _ := newService(t, k)
	return s, k
}

func TestEditTokensAreSignedAndBindTheReview(t *testing.T) {
	s, k := newEditService(t)
	ctx := context.Background()
	doc, err := s.GetEditSource(ctx, cfgRef)
	require.NoError(t, err)
	require.NotEmpty(t, doc.Base)
	assert.NotContains(t, doc.Base, "uid-cfg\"", "opaque to the UI is fine, but it must be signed, not trusted")

	prep := EditPrepareRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "edited"}
	plan, err := s.PrepareEdit(ctx, prep)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Token)
	require.Len(t, k.prepared, 1)
	assert.Equal(t, provider.EditBase{Route: "r", Name: "cfg", Namespace: "ns", UID: "uid-cfg", Version: "7", DocHash: "h", Format: 1}, k.prepared[0].Base)
	assert.NotEmpty(t, plan.Where.ConfigRev)

	// A forged base or token is refused before the provider sees it.
	forged := prep
	forged.Base = flip(doc.Base)
	_, err = s.PrepareEdit(ctx, forged)
	assertCode(t, err, CodeBadRequest)
	assert.Len(t, k.prepared, 1)

	runReq := EditRunRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "edited", Token: plan.Token}
	for name, bad := range map[string]EditRunRequest{
		"no token":            {Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "edited"},
		"forged token":        {Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "edited", Token: flip(plan.Token)},
		"the base as a token": {Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "edited", Token: doc.Base},
		"another target":      {Ref: core.Ref{Provider: "k", Target: "b", Scope: "ns", Kind: "configmaps", Name: "cfg"}, Base: doc.Base, Original: doc.Text, Edited: "edited", Token: plan.Token},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.RunEdit(ctx, bad)
			require.Error(t, err)
			assert.Empty(t, k.runs)
		})
	}
	// Another process's key: its tokens mean nothing here.
	other, _ := newEditService(t)
	_, err = other.RunEdit(ctx, runReq)
	assertCode(t, err, CodeBadRequest)

	res, err := s.RunEdit(ctx, runReq)
	require.NoError(t, err)
	assert.Equal(t, "written", res.Message)
	require.Len(t, k.runs, 1)
	assert.Equal(t, provider.EditGrant{Route: "r", Name: "cfg", Namespace: "ns", UID: "uid-cfg", Version: "8", PatchHash: "p", Mode: "checked"}, k.runs[0].Grant)
	assert.Equal(t, "edited", k.runs[0].Edited)
}

func TestEditWithoutAGrantHasNoToken(t *testing.T) {
	s, k := newEditService(t)
	k.noGrant = true
	doc, err := s.GetEditSource(context.Background(), cfgRef)
	require.NoError(t, err)
	plan, err := s.PrepareEdit(context.Background(), EditPrepareRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "x"})
	require.NoError(t, err)
	assert.Empty(t, plan.Token)
}

func TestEditAfterAReconfigurationIsAConflict(t *testing.T) {
	s, k := newEditService(t)
	ctx := context.Background()
	doc, err := s.GetEditSource(ctx, cfgRef)
	require.NoError(t, err)
	plan, err := s.PrepareEdit(ctx, EditPrepareRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "x"})
	require.NoError(t, err)
	k.setHash("a", "ha2")
	_, err = s.RunEdit(ctx, EditRunRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "x", Token: plan.Token})
	assertCode(t, err, CodeConflict)
	_, err = s.PrepareEdit(ctx, EditPrepareRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: "x"})
	assertCode(t, err, CodeConflict)
	assert.Empty(t, k.runs)
}

func TestEditLimitsAndUnsupportedTargets(t *testing.T) {
	s, _ := newEditService(t)
	ctx := context.Background()
	doc, err := s.GetEditSource(ctx, cfgRef)
	require.NoError(t, err)
	_, err = s.PrepareEdit(ctx, EditPrepareRequest{Ref: doc.Ref, Base: doc.Base, Original: doc.Text, Edited: strings.Repeat("x", maxEditText+1)})
	assertCode(t, err, CodeBadRequest)

	plain, _ := newActionService(t) // sessions that are not Editors
	_, err = plain.GetEditSource(ctx, core.Ref{Provider: "k", Target: "a", Kind: "deployments", Name: "web"})
	assertCode(t, err, CodeUnsupported)
}

// flip changes one character of s (a forged value).
func flip(s string) string {
	b := []byte(s)
	i := len(b) / 2
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	assert.True(t, IsCoded(err, code), "want %s, got %v", code, err)
}
