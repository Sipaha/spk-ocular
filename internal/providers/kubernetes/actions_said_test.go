package kubernetes

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// saidKey is the key of the provider's sentence err carries ("": none —
// a server's words), after checking that its English is the error's text.
func saidKey(t *testing.T, err error) string {
	t.Helper()
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	if pe.Why == nil {
		return ""
	}
	assert.Equal(t, pe.Why.Text, pe.Message, "the English sentence is the error's text")
	return strings.TrimPrefix(pe.Why.Key, ProviderID+".")
}

// assertSaid: err is of class and says the provider's sentence key.
func assertSaid(t *testing.T, err error, class provider.ErrorClass, key string) {
	t.Helper()
	assertClass(t, err, class)
	assert.Equal(t, key, saidKey(t, err))
}

// assertDone: a result says the sentence key with params.
func assertDone(t *testing.T, res core.ActionResult, key string, params map[string]string) {
	t.Helper()
	assert.Equal(t, ProviderID+"."+key, res.Message.Key)
	assert.Equal(t, params, res.Message.Params)
}

// What a run names is checked before any read: each refusal is said.
func TestARunsOwnRefusalsAreSaid(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-web", "7", nil))
	noUID := deployWebRef
	noUID.UID = ""
	_, err := s.RunAction(t.Context(), provider.ActionRun{Ref: noUID, Action: "restart", Expect: routeOf(deploymentsKind) + "-x"})
	assertSaid(t, err, provider.ClassInvalid, "error.noUID")
	other := deployWebRef
	other.Kind = "example.com/gadgets"
	_, err = s.RunAction(t.Context(), provider.ActionRun{Ref: other, Action: "restart", Expect: "x"})
	assertSaid(t, err, provider.ClassUnsupported, "error.unknownKind")
	_, err = s.RunAction(t.Context(), provider.ActionRun{Ref: deployWebRef, Action: "explode", Expect: "x"})
	assertSaid(t, err, provider.ClassUnsupported, "error.noAction")
	_, err = s.PrepareAction(t.Context(), deployWebRef, "explode", core.ActionParams{})
	assertSaid(t, err, provider.ClassUnsupported, "error.noAction")
	assert.Empty(t, writes(c))
}

// Our sentences on an action's path are built by key (provider.Said), so
// the UI says them in its language; only a server's words go as text.
// Supplementary to the behaviour tests above: it catches a new English
// literal, a formatted one, a sentence whose key is dropped (.Text) and
// invalid(...) with our words.
func TestActionErrorsAreSaidByKey(t *testing.T) {
	bad := regexp.MustCompile(`provider\.Error\{[^}]*Message:\s*(fmt\.Sprintf|"|[\w.]+\.Text\b)|\binvalid\("`)
	for _, f := range []string{"actions.go", "drain.go", "cronjob.go", "cronjob_run.go"} {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for i, line := range strings.Split(string(src), "\n") {
			assert.False(t, bad.MatchString(line), "%s:%d: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}

// Every reason a drain's pods cannot be listed has its "nothing was
// written" form for the run.
func TestEveryDrainPodsReasonHasItsRunForm(t *testing.T) {
	for _, m := range []core.Message{msg("drain.podsUnreadable", "reason", "x"), msg("drain.tooMany", "max", maxDrainPods)} {
		n := notWritten(m)
		assert.Equal(t, m.Text+"; nothing was written", n.Text)
		assert.Equal(t, m.Params, n.Params)
	}
}

// Every action a kind offers says its result by key: write builds the key
// from the action's ID ("done."+action), and msg panics on a key without
// a text.
func TestEveryActionHasItsDoneText(t *testing.T) {
	acts := discoveredActions(apiResource{Group: "batch", Version: "v1", Resource: "cronjobs", Namespaced: true, Verbs: []string{"get", "patch", "delete"}})
	for _, as := range kindActions {
		acts = append(acts, as...)
	}
	ids := map[string]bool{}
	for _, a := range acts {
		ids[a.ID] = true
		_, ok := messageTexts["done."+a.ID]
		assert.True(t, ok, "no done.%s", a.ID)
	}
	// Every action write and the drain, run paths handle is enumerated.
	assert.Len(t, ids, 9)
	for _, id := range []string{"restart", "scale", "delete", "cordon", "uncordon", "drain", "suspend", "resume", "run"} {
		assert.True(t, ids[id], id)
	}
}

// A reason without its "nothing was written" form is said as text rather
// than panicking in the run.
func TestNotWrittenOfAnUnknownReasonIsText(t *testing.T) {
	var n core.Message
	require.NotPanics(t, func() {
		n = notWritten(core.Message{Key: ProviderID + ".drain.other", Text: "the pods are elsewhere", Params: map[string]string{"x": "1"}})
	})
	assert.Equal(t, core.Message{Text: "the pods are elsewhere; nothing was written"}, n)
}
