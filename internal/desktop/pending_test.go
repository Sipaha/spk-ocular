package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The window's title counts the agents' plans waiting for the user; a
// desktop notification goes out only when more are waiting than before.
func TestPendingNoticeTitleAndWhenToNotify(t *testing.T) {
	var p pendingNotice
	steps := []struct {
		n      int
		title  string
		notify bool
	}{
		{0, "SPK Ocular", false},
		{1, "SPK Ocular — ждёт подтверждения (1)", true},
		{1, "SPK Ocular — ждёт подтверждения (1)", false},
		{3, "SPK Ocular — ждёт подтверждения (3)", true},
		{2, "SPK Ocular — ждёт подтверждения (2)", false},
		{0, "SPK Ocular", false},
		{1, "SPK Ocular — ждёт подтверждения (1)", true},
	}
	for i, s := range steps {
		title, notify := p.update(s.n, "ru")
		assert.Equal(t, s.title, title, "step %d", i)
		assert.Equal(t, s.notify, notify, "step %d", i)
	}
	title, _ := p.update(2, "en")
	assert.Equal(t, "SPK Ocular — waiting for confirmation (2)", title)
	summary, body := notification(2, "ru")
	assert.Equal(t, "SPK Ocular: агент ждёт подтверждения", summary)
	assert.Equal(t, "Ждут подтверждения: 2. Решите в окне SPK Ocular.", body)
	summary, body = notification(1, "en")
	assert.Equal(t, "SPK Ocular: an agent waits for confirmation", summary)
	assert.Equal(t, "Waiting: 1. Decide in the SPK Ocular window.", body)
}
