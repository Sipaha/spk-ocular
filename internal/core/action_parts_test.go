package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A later done or refused part never hides an earlier unknown one; done
// only when every part is.
func TestPartsOutcomePrecedence(t *testing.T) {
	p := func(os ...ActionOutcome) []ActionPart {
		out := make([]ActionPart, len(os))
		for i, o := range os {
			out[i] = ActionPart{Outcome: o}
		}
		return out
	}
	assert.Equal(t, OutcomeDone, PartsOutcome(nil))
	assert.Equal(t, OutcomeDone, PartsOutcome(p(OutcomeDone, OutcomeDone)))
	assert.Equal(t, OutcomeSkipped, PartsOutcome(p(OutcomeDone, OutcomeSkipped)))
	assert.Equal(t, OutcomeRefused, PartsOutcome(p(OutcomeSkipped, OutcomeRefused, OutcomeDone)))
	assert.Equal(t, OutcomeUnknown, PartsOutcome(p(OutcomeUnknown, OutcomeRefused, OutcomeDone)))
	assert.Equal(t, OutcomeUnknown, PartsOutcome(p(OutcomeDone, OutcomeRefused, OutcomeUnknown, OutcomeSkipped)))
}
