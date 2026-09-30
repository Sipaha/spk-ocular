package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// CheckParams: a parameter only for an action that takes it, of its kind;
// a run needs the value, a plan may be read before it is chosen.
func TestCheckParams(t *testing.T) {
	n, big := 3, 11
	v, empty := "web-6d4b", ""
	none := ActionDescriptor{ID: "restart"}
	count := ActionDescriptor{ID: "scale", Param: &ActionParam{Kind: ParamCount, Min: 0, Max: 10}}
	choice := ActionDescriptor{ID: "undo", Param: &ActionParam{Kind: ParamChoice}}
	for _, c := range []struct {
		name  string
		d     ActionDescriptor
		p     ActionParams
		final bool
		ok    bool
	}{
		{"none, empty", none, ActionParams{}, true, true},
		{"none, count", none, ActionParams{Count: &n}, false, false},
		{"none, choice", none, ActionParams{Choice: &v}, false, false},
		{"count, prepare without", count, ActionParams{}, false, true},
		{"count, run without", count, ActionParams{}, true, false},
		{"count, in range", count, ActionParams{Count: &n}, true, true},
		{"count, out of range", count, ActionParams{Count: &big}, true, false},
		{"count, choice", count, ActionParams{Count: &n, Choice: &v}, true, false},
		{"choice, prepare without", choice, ActionParams{}, false, true},
		{"choice, run without", choice, ActionParams{}, true, false},
		{"choice, run with", choice, ActionParams{Choice: &v}, true, true},
		{"choice, prepare with", choice, ActionParams{Choice: &v}, false, true},
		{"choice, empty value", choice, ActionParams{Choice: &empty}, false, false},
		{"choice, count", choice, ActionParams{Choice: &v, Count: &n}, false, false},
		{"choice, count only", choice, ActionParams{Count: &n}, false, false},
	} {
		err := c.d.CheckParams(c.p, c.final)
		assert.Equal(t, c.ok, err == nil, "%s: %v", c.name, err)
	}
}
