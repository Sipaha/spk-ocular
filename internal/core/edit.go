package core

// EditDoc is an object's text for the editor (Editor.EditSource).
type EditDoc struct {
	// Ref with the UID of the object read.
	Ref Ref `json:"ref"`
	// Text is the document shown: what the editor does not change is left
	// out (the provider says what in a header comment).
	Text string `json:"text"`
	// Base is the signed stand-in of the object and text read (set by the
	// API); PrepareEdit and RunEdit carry it back with the original text.
	Base string `json:"base"`
}

// EditPlan is what an edit would do, read without changing anything.
type EditPlan struct {
	Where LiveTarget `json:"where"`
	// Before is the object now and After the expected result, as the
	// editor shows objects.
	Before string `json:"before"`
	After  string `json:"after"`
	// Checked: After is the server's (a dry run) — a prediction: admission
	// may still differ at the write. Else it was computed locally.
	Checked bool `json:"checked"`
	// Changed: the edit changes something (else nothing to apply).
	Changed bool `json:"changed"`
	// Rebased: the object changed since the text was read; the edit lies
	// over its current version.
	Rebased bool `json:"rebased,omitempty"`
	// Collisions: fields the edit overwrites that changed since the text
	// was read.
	Collisions  []string  `json:"collisions,omitempty"`
	Destructive bool      `json:"destructive,omitempty"`
	Warnings    []Message `json:"warnings,omitempty"`
	Rights      Rights    `json:"rights"`
	// Unavailable: why this edit cannot be written (the server refused it).
	Unavailable *Message `json:"unavailable,omitempty"`
	// Token lets RunEdit write exactly this plan; empty when nothing can
	// be written (no change, unavailable, denied). Set by the API.
	Token string `json:"token,omitempty"`
}

// EditResult: the edit was written.
type EditResult struct {
	Message string `json:"message"`
	// Version is the object's new version; Actual the object as written,
	// as the editor shows objects (it may differ from the plan's After).
	Version string `json:"version,omitempty"`
	Actual  string `json:"actual,omitempty"`
}
