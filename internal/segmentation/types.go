package segmentation

// The wire types of rule sets, exported to the frontend by tygo
// (web/src/types/segmentation.ts).

// Mode says whether a rule set splits a block into sentences or keeps it as
// one segment.
type Mode string

const (
	ModeSentence  Mode = "sentence"
	ModeParagraph Mode = "paragraph"
)

// Rule decides a position between two characters: Before must match the text
// that ends at the position and After the text that starts there. An empty
// pattern matches everywhere.
type Rule struct {
	Before string `json:"before"`
	After  string `json:"after"`
	// Break is false for a no-break rule (an exception).
	Break bool `json:"break"`
}

// RuleSet is a complete set of segmentation rules. Rules are tried in order and
// the first that matches a position decides it; the abbreviation lists are
// tried before any rule.
type RuleSet struct {
	Mode  Mode   `json:"mode"`
	Rules []Rule `json:"rules"`
	// Abbreviations are words after which a full stop never ends a sentence,
	// written without the full stop ("Dr", "e.g"). Case-sensitive.
	Abbreviations []string `json:"abbreviations"`
	// NumberAbbreviations are words that are abbreviations only before a
	// number ("p" in "p. 12", "No" in "No. 5").
	NumberAbbreviations []string `json:"number_abbreviations"`
}

// DecisionSource says what decided a position.
type DecisionSource string

const (
	SourceRule               DecisionSource = "rule"
	SourceAbbreviation       DecisionSource = "abbreviation"
	SourceNumberAbbreviation DecisionSource = "number_abbreviation"
)

// SRXDraft is one <languagerule> read from an SRX file: an unsaved rule set,
// the languages its <languagemap>s name (only maps whose pattern names a
// single language count), and what could not be imported.
type SRXDraft struct {
	Name            string   `json:"name"`
	SourceLanguages []string `json:"source_languages"`
	RuleSet         RuleSet  `json:"rule_set"`
	Warnings        []string `json:"warnings"`
}

// SRXImport is what an SRX file holds: one draft per language rule, and
// warnings about the file as a whole.
type SRXImport struct {
	Drafts   []SRXDraft `json:"drafts"`
	Warnings []string   `json:"warnings"`
}
