package api

import (
	"context"
	"errors"
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The UI's Russian of the API's own sentences (web/src/i18n.ts
// providerTexts, keys "api.…") has every key with the same parameters.
func TestTheUIsTranslationsCoverEveryMessage(t *testing.T) {
	src, err := os.ReadFile("../../web/src/i18n.ts")
	require.NoError(t, err)
	entry := regexp.MustCompile(`'api\.([a-zA-Z.]+)':\s*'([^']*)'`)
	ui := map[string]string{}
	for _, m := range entry.FindAllStringSubmatch(string(src), -1) {
		ui[m[1]] = m[2]
	}
	params := func(s string) []string {
		out := regexp.MustCompile(`\{(\w+)\}`).FindAllString(s, -1)
		sort.Strings(out)
		return slices.Compact(out)
	}
	for key, en := range messageTexts {
		ru, ok := ui[key]
		if assert.True(t, ok, "no translation of %s", key) {
			assert.Equal(t, params(en), params(ru), key)
		}
	}
	for key := range ui {
		_, ok := messageTexts[key]
		assert.True(t, ok, "a translation of an unknown key %s", key)
	}
}

// The API's own refusals on an action's path carry their sentence by key;
// the code and the English detail stay what they were.
func TestTheAPIsOwnRefusalsAreSaid(t *testing.T) {
	ctx := context.Background()
	s, _ := newActionService(t)
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "restart"})
	require.NoError(t, err)
	good := runFor(plan, nil)
	check := func(name string, err error, code, detail, key string, params map[string]string) {
		t.Helper()
		var ce *CodedError
		require.True(t, errors.As(err, &ce), "%s: %v", name, err)
		assert.Equal(t, code, ce.Code, name)
		assert.Equal(t, detail, ce.Detail, name)
		if assert.NotNil(t, ce.Why, name) {
			assert.Equal(t, "api."+key, ce.Why.Key, name)
			assert.Equal(t, params, ce.Why.Params, name)
			assert.Equal(t, detail, ce.Why.Text, "%s: the English is the detail", name)
		}
	}

	_, err = s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "explode"})
	check("prepare: no such action", err, CodeUnsupported, `no such action: Deployments has no action "explode"`, "noAction", map[string]string{"kind": "Deployments", "action": `"explode"`})
	r := good
	r.Action = "explode"
	_, err = s.RunAction(ctx, r)
	check("run: no such action", err, CodeUnsupported, `no such action: Deployments has no action "explode"`, "noAction", map[string]string{"kind": "Deployments", "action": `"explode"`})
	r = good
	r.Ref.Kind = "nodes"
	_, err = s.RunAction(ctx, r)
	check("run: unknown kind", err, CodeUnsupported, `no such action: unknown kind "nodes"`, "unknownKind", map[string]string{"kind": `"nodes"`})
	r = good
	r.ConfigRev = "0000000000000000"
	_, err = s.RunAction(ctx, r)
	check("run: stale revision", err, CodeConflict, "the configuration of a changed since the action was reviewed; review it again", "configChanged", map[string]string{"target": "a"})

	s2, _ := newService(t, newOpenable("a"))
	_, err = s2.PrepareAction(ctx, ActionRequest{Ref: podRef, Action: "delete"})
	check("no actioner", err, CodeUnsupported, "objects of this target cannot be changed", "cannotChange", nil)
}
