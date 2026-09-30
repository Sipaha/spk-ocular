package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// A provider's reason reaches the coded error, and travels in the JSON the
// Wails runtime puts on the rejected call's cause: its binding marshals the
// returned error as json.Marshal(&err) (defaultMarshalError, v3 beta.26).
func TestAProvidersWhyTravelsWithTheCodedError(t *testing.T) {
	why := core.Message{Key: "kubernetes.error.changed", Params: map[string]string{"name": "web"}, Text: "web changed"}
	ce := fromProvider(fmt.Errorf("run: %w", provider.Said(provider.ClassConflict, why)))
	assert.Equal(t, "conflict", ce.Code)
	assert.Equal(t, "web changed", ce.Detail)
	require.NotNil(t, ce.Why)
	assert.Equal(t, why, *ce.Why)

	var err error = ce
	b, jerr := json.Marshal(&err)
	require.NoError(t, jerr)
	var cause struct {
		Code   string        `json:"code"`
		Detail string        `json:"detail"`
		Why    *core.Message `json:"why"`
	}
	require.NoError(t, json.Unmarshal(b, &cause))
	assert.Equal(t, "conflict", cause.Code)
	assert.Equal(t, "web changed", cause.Detail)
	require.NotNil(t, cause.Why)
	assert.Equal(t, why, *cause.Why)
}

// Without a reason nothing new is said: the JSON has no "why".
func TestAnErrorWithoutWhyHasNoWhy(t *testing.T) {
	ce := fromProvider(&provider.Error{Class: provider.ClassForbidden, Message: "server text"})
	assert.Nil(t, ce.Why)
	b, err := json.Marshal(ce)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "why")
}
