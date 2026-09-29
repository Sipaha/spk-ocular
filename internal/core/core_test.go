package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthFromPicksWorstAndOrdersIssues(t *testing.T) {
	assert.Equal(t, Health{State: HealthOK}, HealthFrom(nil))
	h := HealthFrom([]Issue{
		{State: HealthProgressing, Reason: "ContainerCreating"},
		{State: HealthError, Reason: "CrashLoopBackOff", Message: "back-off 5m"},
		{State: HealthWarning, Reason: "Restarts"},
	})
	assert.Equal(t, HealthError, h.State)
	assert.Equal(t, "CrashLoopBackOff", h.Reason)
	assert.Equal(t, "back-off 5m", h.Message)
	require.Len(t, h.Issues, 3)
	assert.Equal(t, []HealthState{HealthError, HealthWarning, HealthProgressing},
		[]HealthState{h.Issues[0].State, h.Issues[1].State, h.Issues[2].State})
}

func TestScopeSelValid(t *testing.T) {
	assert.True(t, ScopeSel{Mode: ScopeAll}.Valid())
	assert.True(t, ScopeSel{Mode: ScopeOne, Name: "web"}.Valid())
	assert.True(t, ScopeSel{Mode: ScopeNone}.Valid())
	assert.False(t, ScopeSel{Mode: ScopeOne}.Valid())
	assert.False(t, ScopeSel{Mode: ScopeAll, Name: "x"}.Valid())
	assert.False(t, ScopeSel{}.Valid())
}

// The UI (web/src/api/types.ts) depends on these shapes.
func TestJSONShapes(t *testing.T) {
	b, err := json.Marshal(Row{
		ID:     "u1",
		Ref:    Ref{Provider: "kubernetes", Target: "t", Scope: "ns", Kind: "pods", Name: "p", UID: "u1"},
		Cells:  []Cell{TextCell("p"), {}, NumCell(0.5, "1/2"), TimeCell(1000)},
		Health: HealthFrom(nil),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"u1","ref":{"provider":"kubernetes","target":"t","scope":"ns","kind":"pods","name":"p","uid":"u1"},
		"cells":[{"text":"p"},{},{"text":"1/2","num":0.5},{"time":1000}],"health":{"state":"ok"}}`, string(b))
}
