package core

import "regexp"

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// Format fills tmpl's own {name} placeholders with params in one pass: a
// value is data and is never scanned (a server's text may hold "{name}");
// a placeholder without a param stays as it is.
func Format(tmpl string, params map[string]string) string {
	if len(params) == 0 {
		return tmpl
	}
	return placeholder.ReplaceAllStringFunc(tmpl, func(p string) string {
		if v, ok := params[p[1:len(p)-1]]; ok {
			return v
		}
		return p
	})
}
