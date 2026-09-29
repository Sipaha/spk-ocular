package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fake struct{ id string }

func (f fake) ID() string                                  { return f.id }
func (f fake) Title() string                               { return f.id }
func (f fake) Discover(context.Context) (Discovery, error) { return Discovery{}, nil }

func TestRegistryKeepsOrderAndLooksUp(t *testing.T) {
	r, err := NewRegistry(fake{"b"}, fake{"a"})
	require.NoError(t, err)
	ids := []string{}
	for _, p := range r.All() {
		ids = append(ids, p.ID())
	}
	assert.Equal(t, []string{"b", "a"}, ids)
	p, ok := r.Get("a")
	assert.True(t, ok)
	assert.Equal(t, "a", p.ID())
	_, ok = r.Get("zzz")
	assert.False(t, ok)
}

func TestRegistryRejectsDuplicateIDs(t *testing.T) {
	_, err := NewRegistry(fake{"a"}, fake{"a"})
	assert.Error(t, err)
}
