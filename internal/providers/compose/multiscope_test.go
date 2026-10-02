package compose

import (
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestMultipleProjectsKeepSharedNetworksUntilLastUserLeaves(t *testing.T) {
	e := newTestEnv(t)
	e.s.proj = defaultProjections
	a := ctr(id(1), "a-web", "a", "web", onNet("shared", "shared", "10.0.0.1"))
	b := ctr(id(2), "b-web", "b", "web", onNet("shared", "shared", "10.0.0.2"))
	e.fe.PutContainer(*a)
	e.fe.PutContainer(*b)
	e.fe.PutContainer(*ctr(id(3), "outside", "outside", "web", onNet("private", "private", "10.0.0.3")))
	e.fe.PutNetwork(*network("shared", "shared", ""))
	e.fe.PutNetwork(*network("private", "private", "outside"))
	sk := newSink()
	stop, err := e.s.Watch(provider.Query{Kind: KindNetworks, Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"a", "b"}}}, sk)
	require.NoError(t, err)
	defer stop()
	sk.waitFor(t, "shared network only", hasRows("shared"))
	e.fe.RemoveContainer(a.ID)
	e.fe.Emit(containerEvent("destroy", *a))
	require.NoError(t, e.s.Resync(provider.Query{Kind: KindNetworks}))
	sk.waitFor(t, "remaining project retains the network", hasRows("shared"))
	e.fe.RemoveContainer(b.ID)
	e.fe.Emit(containerEvent("destroy", *b))
	sk.waitFor(t, "last user removed", hasRows())
}
