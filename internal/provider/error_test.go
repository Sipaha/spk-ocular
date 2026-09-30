package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-ocular/internal/core"
)

// Said keeps the English sentence as the error's text (logs, HTTP clients)
// and carries the sentence itself for the UI to say in its language.
func TestSaidKeepsTheTextAndCarriesTheWhy(t *testing.T) {
	m := core.Message{Key: "kubernetes.error.changed", Params: map[string]string{"name": "web"}, Text: "web changed"}
	err := Said(ClassConflict, m)
	assert.Equal(t, "conflict: web changed", err.Error())
	assert.Equal(t, "web changed", err.Message)
	if assert.NotNil(t, err.Why) {
		assert.Equal(t, m, *err.Why)
	}
	m.Params["name"] = "other"
	assert.Equal(t, "web", err.Why.Params["name"], "the error does not share the caller's parameters")
}
