package synthetic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func pref(name, zone string) core.Ref {
	return core.Ref{Provider: ID, Target: Target, Scope: zone, Kind: ParcelKind, Name: name}
}

// Parcels are a scoped kind with actions (agent access e2e: grants are
// per scope): stamp changes a parcel, delete removes it (destructive);
// the reset brings them back.
func TestParcelsAreScopedAndHaveActions(t *testing.T) {
	p, s := open(t)
	ctx := context.Background()
	var k core.KindDescriptor
	for _, x := range s.Kinds() {
		if x.ID == ParcelKind {
			k = x
		}
	}
	require.Equal(t, ParcelKind, k.ID)
	assert.True(t, k.Scoped)
	assert.Equal(t, []core.ActionDescriptor{actStamp, actDelete}, k.Actions)

	blue := &rowSink{}
	stop, err := s.Watch(provider.Query{Kind: ParcelKind, Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}}, blue)
	require.NoError(t, err)
	defer stop()
	assert.Equal(t, []string{"parcel-1", "blue", "0"}, blue.cells("parcel-1"))
	assert.Nil(t, blue.cells("parcel-3"), "green is not blue")

	plan, err := s.PrepareAction(ctx, pref("parcel-1", "blue"), "stamp", core.ActionParams{})
	require.NoError(t, err)
	assert.False(t, plan.Destructive)
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "stamp", Expect: plan.Expect})
	require.NoError(t, err)
	assert.Equal(t, "1", blue.cells("parcel-1")[2])
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "stamp", Expect: plan.Expect})
	assert.Equal(t, provider.ClassConflict, class(t, err), "changed since the review")

	plan, err = s.PrepareAction(ctx, pref("parcel-2", "blue"), "delete", core.ActionParams{})
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.Equal(t, "blue", plan.Where.Ref.Scope)
	res, err := s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "delete", Expect: plan.Expect})
	require.NoError(t, err)
	assert.Equal(t, "parcel parcel-2: deleted", res.Message.Text)
	assert.Nil(t, blue.cells("parcel-2"))
	_, err = s.Get(ctx, pref("parcel-2", "blue"))
	assert.Equal(t, provider.ClassNotFound, class(t, err))

	p.ResetActions()
	assert.Equal(t, []string{"parcel-2", "blue", "0"}, blue.cells("parcel-2"))
	assert.Equal(t, "0", blue.cells("parcel-1")[2])
	r, err := s.Get(ctx, pref("parcel-3", "green"))
	require.NoError(t, err)
	assert.Equal(t, "green", r.Ref.Scope)
}

// Agent grants bind to what a target points at: the synthetic one says it.
func TestTheSyntheticTargetHasAnIdentity(t *testing.T) {
	d, err := New().Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, d.Targets, 1)
	assert.Equal(t, "synthetic.local", d.Targets[0].Identity)
}
