package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Parameters fill the template's own placeholders once: a value is data,
// never scanned for placeholders (a server's text may contain "{name}").
func TestFormatFillsTheTemplatesPlaceholdersOnce(t *testing.T) {
	for _, tc := range []struct {
		tmpl   string
		params map[string]string
		want   string
	}{
		{"refused: {detail}; check {name}", map[string]string{"detail": "server says {name} was rejected", "name": "nightly"}, "refused: server says {name} was rejected; check nightly"},
		{"{name}: {detail}", map[string]string{"name": "{detail}", "detail": "x"}, "{detail}: x"},
		{"{count} of {countAll}", map[string]string{"count": "1", "countAll": "3"}, "1 of 3"},
		{"{name} and {name}", map[string]string{"name": "web"}, "web and web"},
		{"{detail}", map[string]string{"detail": `{"kind":"Status","message":"a \"q\" 100%` + "\nline"}, `{"kind":"Status","message":"a \"q\" 100%` + "\nline"},
		{"kept {unknown} and {name}", map[string]string{"name": "$1 ${name}"}, "kept {unknown} and $1 ${name}"},
		{"no params", nil, "no params"},
		{"{ name } {} {na-me}", map[string]string{"name": "x"}, "{ name } {} {na-me}"},
	} {
		assert.Equal(t, tc.want, Format(tc.tmpl, tc.params), tc.tmpl)
	}
}
