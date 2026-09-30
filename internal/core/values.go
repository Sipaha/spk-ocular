package core

// ValueKey is one key of an object's protected values, never its value.
type ValueKey struct {
	Key string `json:"key"`
	// Size in bytes (decoded).
	Size int `json:"size"`
	// Text: the value is UTF-8 without control characters but \n and \t
	// (no \r: a browser's text field turns CRLF into LF), shown and edited
	// as text; else as base64.
	Text bool `json:"text,omitempty"`
}

// ValueList is an object's keys, as read now.
type ValueList struct {
	// Ref with the UID of the object read.
	Ref     Ref        `json:"ref"`
	Version string     `json:"version,omitempty"`
	Keys    []ValueKey `json:"keys"`
	// Base is the signed stand-in of what was read (set by the API): an
	// edit's review and write carry it back.
	Base string `json:"base"`
}

// Value is one key's value, read on request (ValueHolder.RevealValue).
type Value struct {
	Key  string `json:"key"`
	Size int    `json:"size"`
	Text bool   `json:"text,omitempty"`
	// Value: the text itself when Text, else base64 of the bytes.
	Value string `json:"value"`
	// UID and Version of the object it was read from: the UI shows it only
	// for the object and revision it asked about.
	UID     string `json:"uid"`
	Version string `json:"version,omitempty"`
}

// Value edit operations.
const (
	ValueSet    = "set"
	ValueDelete = "delete"
)

// ValueConsumers: what reads the object's values, as far as could be seen.
type ValueConsumers struct {
	// Known: the lookup finished; else Why says why the list may be
	// incomplete (never read as "nothing uses it").
	Known bool     `json:"known"`
	Why   string   `json:"why,omitempty"`
	Items []string `json:"items,omitempty"`
}

// ValuePlan is what a value edit would do, read without changing
// anything and without any value.
type ValuePlan struct {
	Where LiveTarget `json:"where"`
	Key   string     `json:"key"`
	Op    string     `json:"op"`
	// Before and After: the key's size in bytes; -1 is absent.
	Before int `json:"before"`
	After  int `json:"after"`
	// Checked: After is the server's (a dry run), else computed locally.
	Checked bool `json:"checked"`
	Changed bool `json:"changed"`
	// Rebased: the object changed since its keys were listed; Collision:
	// this key did.
	Rebased     bool `json:"rebased,omitempty"`
	Collision   bool `json:"collision,omitempty"`
	Destructive bool `json:"destructive,omitempty"`
	// ServerChanges: keys the server's review leaves otherwise than the
	// edit asks (admission), named, never shown.
	ServerChanges []string        `json:"serverChanges,omitempty"`
	Consumers     *ValueConsumers `json:"consumers,omitempty"`
	Warnings      []Message       `json:"warnings,omitempty"`
	Rights        Rights          `json:"rights"`
	Unavailable   *Message        `json:"unavailable,omitempty"`
	// Token lets RunValueEdit write exactly this plan (set by the API).
	Token string `json:"token,omitempty"`
}

// ValueResult: the value edit was written.
type ValueResult struct {
	Message string `json:"message"`
	Version string `json:"version,omitempty"`
	// Differs: keys written otherwise than the review expected.
	Differs []string `json:"differs,omitempty"`
	// ServerChanges: as reviewed (the server changes what was typed).
	ServerChanges []string `json:"serverChanges,omitempty"`
}
