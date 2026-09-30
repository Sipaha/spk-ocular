package compose

import (
	"strings"

	"github.com/spk/spk-ocular/internal/core"
)

// The provider's English texts for the UI by key; the UI says the keys it
// knows in its language (web/src/i18n.ts, providerTexts) with the same
// parameters, the rest in this English.
var messageTexts = map[string]string{
	"scope.singular":                  "Project",
	"scope.plural":                    "projects",
	"scope.all":                       "All projects",
	"notCovered.unlabelled":           "containers without Compose labels",
	"container.exited":                "exit code {code}",
	"container.exitedOOM":             "exit code {code}, killed for running out of memory",
	"container.restarting":            "restarting after exit code {code}",
	"container.unknownState":          "unknown state {state}",
	"container.restartedByPolicy":     "restarted by its restart policy ({count} restarts in total)",
	"container.restartedByPolicyOnce": "restarted by its restart policy ({count} restart in total)",
	"service.members":                 "{count} of {total} containers",
	"service.membersExample":          "{count} of {total} containers; {example}",
}

// msg builds the message key with params given as name, value pairs.
func msg(key string, kv ...string) core.Message {
	tmpl, ok := messageTexts[key]
	if !ok {
		panic("compose: no text for " + key)
	}
	m := core.Message{Key: ProviderID + "." + key, Text: tmpl}
	if len(kv) > 0 {
		m.Params = make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			m.Params[kv[i]] = kv[i+1]
			m.Text = strings.ReplaceAll(m.Text, "{"+kv[i]+"}", kv[i+1])
		}
	}
	return m
}
