package api

import (
	"strconv"

	"github.com/spk/spk-ocular/internal/core"
)

// The API's own English sentences on an action's path, by key ("api.…");
// the UI says the keys it knows in its language (web/src/i18n.ts,
// providerTexts), the rest in this English.
var messageTexts = map[string]string{
	"cannotChange":  "objects of this target cannot be changed",
	"noAction":      "no such action: {kind} has no action {action}",
	"unknownKind":   "no such action: unknown kind {kind}",
	"sessionClosed": "the session closed meanwhile; try again",
	"configChanged": "the configuration of {target} changed since the action was reviewed; review it again",
	"loginNeeded":   "a login to the cluster is needed to go on: select the target",
	"closedByUser":  "the connection to the target was closed",
	"loginByPerson": "a login to the cluster is done by a person: select the target in Ocular",
}

// apiMessage is the API's sentence key as a message (no params).
func apiMessage(key string) core.Message {
	tmpl, ok := messageTexts[key]
	if !ok {
		panic("api: no text for " + key)
	}
	return core.Message{Key: "api." + key, Text: tmpl}
}

// said is a coded error whose detail is the API's sentence key (params as
// name, value pairs).
func said(code, key string, kv ...string) *CodedError {
	tmpl, ok := messageTexts[key]
	if !ok {
		panic("api: no text for " + key)
	}
	m := core.Message{Key: "api." + key, Text: tmpl}
	if len(kv) > 0 {
		m.Params = make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			m.Params[kv[i]] = kv[i+1]
		}
		m.Text = core.Format(tmpl, m.Params)
	}
	return &CodedError{Code: code, Detail: m.Text, Why: &m}
}

// noAction: kinds (the session's) have no action id on kind — the catalog
// changed between the menu and the click.
func noAction(kinds []core.KindDescriptor, kind, id string) *CodedError {
	for _, k := range kinds {
		if k.ID == kind {
			return said(CodeUnsupported, "noAction", "kind", k.Title, "action", strconv.Quote(id))
		}
	}
	return said(CodeUnsupported, "unknownKind", "kind", strconv.Quote(kind))
}
