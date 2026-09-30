package synthetic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func TestCratesAreTheGenericUIsRegression(t *testing.T) {
	ctx := context.Background()
	p := New()
	d, err := p.Discover(ctx)
	require.NoError(t, err)
	assert.Equal(t, "blue", d.Targets[0].DefaultScope)
	assert.Equal(t, "Zone", p.ScopeNames().Singular.Text)
	sess, err := p.Open(ctx, Target)
	require.NoError(t, err)
	var defaults []string
	for _, k := range sess.Kinds() {
		if k.Default {
			defaults = append(defaults, k.ID)
		}
		assert.Empty(t, k.EventsKind, "no events here")
	}
	assert.Equal(t, []string{CrateKind}, defaults)
	_, err = sess.Scopes(ctx)
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class)

	blue, all := &rowSink{}, &rowSink{}
	stop1, err := sess.Watch(provider.Query{Kind: CrateKind, Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}}, blue)
	require.NoError(t, err)
	defer stop1()
	stop2, err := sess.Watch(provider.Query{Kind: CrateKind, Scope: core.ScopeSel{Mode: core.ScopeAll}}, all)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "blue", "0"}, blue.cells("crate-7f3a"), "shown by title, keyed by name")
	assert.Nil(t, blue.cells("crate-03de"), "green is not blue")
	assert.Len(t, all.rows, 3)

	require.NoError(t, sess.(provider.Resyncer).Resync(provider.Query{Kind: CrateKind}))
	assert.Equal(t, "1", blue.cells("crate-7f3a")[2], "read again: every open view redelivers")
	stop2()
	require.NoError(t, sess.(provider.Resyncer).Resync(provider.Query{Kind: CrateKind}))
	assert.Equal(t, "1", all.cells("crate-7f3a")[2], "a stopped view gets nothing")

	info, err := sess.(provider.Execer).ExecInfo(ctx, core.Ref{Kind: CrateKind, Name: "crate-7f3a"})
	require.NoError(t, err)
	assert.Empty(t, info.Instances)
}
