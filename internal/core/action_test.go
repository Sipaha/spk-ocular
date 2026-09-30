package core

import (
	"strings"
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

// A text value only for an action that takes one: required at the run,
// never empty or longer than its Max; it goes with the action's Param.
func TestCheckParamsText(t *testing.T) {
	img, empty, long := "busybox:1.36", "", strings.Repeat("x", 13)
	target := "app"
	debug := ActionDescriptor{ID: "debug", Text: &ActionText{Title: Message{Text: "Image"}, Default: "busybox:1.36", Max: 12}, Param: &ActionParam{Kind: ParamChoice}}
	plain := ActionDescriptor{ID: "restart"}
	for _, c := range []struct {
		name  string
		d     ActionDescriptor
		p     ActionParams
		final bool
		ok    bool
	}{
		{"no text taken", plain, ActionParams{Text: &img}, false, false},
		{"prepare without", debug, ActionParams{}, false, true},
		{"run without text", debug, ActionParams{Choice: &target}, true, false},
		{"run with both", debug, ActionParams{Text: &img, Choice: &target}, true, true},
		{"run without choice", debug, ActionParams{Text: &img}, true, false},
		{"empty", debug, ActionParams{Text: &empty}, false, false},
		{"too long", debug, ActionParams{Text: &long}, false, false},
	} {
		err := c.d.CheckParams(c.p, c.final)
		assert.Equal(t, c.ok, err == nil, "%s: %v", c.name, err)
	}
}
