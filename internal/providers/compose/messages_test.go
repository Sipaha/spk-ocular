package compose

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every provider text has its Russian in the UI (providerTexts), with the
// same parameters; the UI has no translation of an unknown key.
func TestTheUIsTranslationsCoverEveryMessage(t *testing.T) {
	src, err := os.ReadFile("../../../web/src/i18n.ts")
	require.NoError(t, err)
	entry := regexp.MustCompile(`'compose\.([a-zA-Z.]+)':\s*'([^']*)'`)
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

// A value is data: another parameter's placeholder inside it stays.
func TestMessageParamsAreNotRescanned(t *testing.T) {
	m := msg("act.stop", "name", "{signal}", "signal", "SIGTERM", "timeout", "10")
	assert.Contains(t, m.Text, "{signal}")
}
