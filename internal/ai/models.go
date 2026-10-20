package ai

// Model is a selectable provider model. ID is the provider's API model id;
// Label is the human name shown in the dropdown; Params is the tunable-request
// parameter set this model accepts — carried per model
// because Anthropic's generations disagree about which knobs exist, and sending
// the wrong one is a 400 rather than a soft degradation.
//
// The set of models is fetched from each vendor's API per tenant
// — see catalog.go. An extension has no model list.
type Model struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Params []ParamDef `json:"params"`

	// family selects Params. Unexported: it's an internal classification, and
	// the FE only ever needs the resolved list.
	family paramFamily

	// deprecated marks a model the vendor has retired (Azure's
	// lifecycle_status). It is kept out of the picker but stays valid, so a
	// system already pinned to one can still be saved.
	deprecated bool
}

// Anthropic model ids referenced by name where a rule is model-specific.
const modelClaudeOpus5 = "claude-opus-5"
