package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
)

func kindByID(t *testing.T, id string) core.KindDescriptor {
	t.Helper()
	for _, k := range kindDescriptors() {
		if k.ID == id {
			return k
		}
	}
	require.Failf(t, "no kind", "%s", id)
	return core.KindDescriptor{}
}

// The kinds, their navigation and capabilities (decision 5): projects are
// the scopes, containers open first, containers and services have logs,
// images have no scope; Compose has no events view in P6.
func TestKindDescriptors(t *testing.T) {
	cases := []struct {
		id, group, singular string
		scoped, def, logs   bool
		cols                []string
		scopeCol            string
	}{
		{KindProjects, "Compose", "Project", false, false, false, []string{"name", "services", "containers", "running", "workingDir", "configFiles"}, ""},
		{KindServices, "Compose", "Service", true, false, true, []string{"name", "project", "running", "status", "images", "cpu", "memory"}, "project"},
		{KindContainers, "Engine", "Container", true, true, true, []string{"name", "project", "service", "number", "status", "health", "restarts", "image", "ports", "age", "cpu", "memory"}, "project"},
		{KindNetworks, "Engine", "Network", true, false, false, []string{"name", "project", "driver", "netScope", "containers", "age"}, "project"},
		{KindVolumes, "Engine", "Volume", true, false, false, []string{"name", "project", "driver", "usedBy", "age"}, "project"},
		{KindImages, "Engine", "Image", false, false, false, []string{"tags", "id", "size", "usedBy", "age"}, ""},
	}
	all := kindDescriptors()
	require.Len(t, all, len(cases))
	for i, c := range cases {
		k := all[i]
		assert.Equal(t, c.id, k.ID, "navigation order")
		assert.Equal(t, c.group, k.Group, c.id)
		assert.Equal(t, c.singular, k.Singular, c.id)
		assert.Equal(t, c.scoped, k.Scoped, c.id)
		assert.Equal(t, c.def, k.Default, c.id)
		assert.Equal(t, c.logs, k.Logs, c.id)
		assert.Empty(t, k.EventsKind, c.id)
		assert.False(t, k.Hidden, c.id)
		assert.Equal(t, c.id == KindServices || c.id == KindContainers, k.Exec, c.id)
		assert.False(t, k.Forward, c.id)
		var acts []string
		for _, a := range k.Actions {
			acts = append(acts, a.ID)
			assert.Equal(t, a.ID == "delete", a.Destructive, "%s %s", c.id, a.ID)
		}
		switch c.id {
		case KindContainers:
			assert.Equal(t, []string{"restart", "stop", "start", "delete"}, acts)
		case KindServices:
			assert.Equal(t, []string{"restart", "stop", "start"}, acts)
		default:
			assert.Empty(t, acts, c.id)
		}
		var ids []string
		scopeCol := ""
		for _, col := range k.Columns {
			ids = append(ids, col.ID)
			if col.ScopeColumn {
				assert.Empty(t, scopeCol, "one scope column: %s", c.id)
				scopeCol = col.ID
			}
		}
		assert.Equal(t, c.cols, ids, c.id)
		assert.Equal(t, c.scopeCol, scopeCol, c.id)
	}
}

// Palette names are unique within the provider and never a scope or
// target command (":project web" selects the project, so the projects
// kind answers only to its id and title).
func TestKindAliasesAreUniqueAndDoNotShadowCommands(t *testing.T) {
	taken := map[string]string{}
	p := NewWith(func(string) string { return "" }, t.TempDir())
	for _, a := range append(p.CommandAliases().Scope, p.CommandAliases().Target...) {
		taken[a] = "command"
	}
	for _, k := range kindDescriptors() {
		for _, a := range append([]string{k.ID}, k.Aliases...) {
			prev, dup := taken[a]
			assert.False(t, dup, "%s of %s is already %s", a, k.ID, prev)
			taken[a] = k.ID
		}
	}
	assert.Equal(t, []string{"ct", "container"}, kindByID(t, KindContainers).Aliases)
}

// Standalone containers are covered alongside Compose containers.
func TestContainersNameWhatTheyDoNotCover(t *testing.T) {
	assert.Empty(t, kindByID(t, KindContainers).NotCovered)
}
