package segmentationapi

import (
	"time"

	"openloc/internal/segmentation"
)

// The wire types of /api/segmentation-profiles, exported to the frontend by
// tygo (web/src/types/segmentationapi.ts).

// Profile is a segmentation profile as the API returns it: a built-in or one
// of the org's.
type Profile struct {
	ID int64 `json:"id"`
	// BuiltinKey names a built-in and its version ("builtin:en@1"); empty
	// on an org profile.
	BuiltinKey string `json:"builtin_key,omitempty"`
	Name       string `json:"name"`
	// SourceLanguages are normalized codes; none means the profile suits
	// any language.
	SourceLanguages []string `json:"source_languages"`
	Builtin         bool     `json:"builtin"`
	// Suggested marks, in a list for one source language, the profile a new
	// project or template in that language starts with.
	Suggested   bool   `json:"suggested,omitempty"`
	Description string `json:"description"`
	// BasedOn is the id of the profile this one was copied from.
	BasedOn    *int64               `json:"based_on"`
	Definition segmentation.RuleSet `json:"definition"`
	CreatedAt  time.Time            `json:"created_at"`
	// UpdatedAt is what a save sends back to detect a concurrent edit.
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is the writable shape of a profile. On create, From copies the
// definition of another profile (a built-in or the org's) when Definition is
// nil.
type Input struct {
	Name string `json:"name"`
	// SourceLanguages are the languages the profile suits; none means any.
	SourceLanguages []string              `json:"source_languages"`
	Definition      *segmentation.RuleSet `json:"definition"`
	From            *int64                `json:"from,omitempty"`
	// UpdatedAt must equal the stored value on update.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// PreviewInput is sample text and the rules to split it with: an unsaved
// rule set, or the ID of a stored or built-in profile.
type PreviewInput struct {
	RuleSet *segmentation.RuleSet `json:"rule_set"`
	ID      *int64                `json:"id,omitempty"`
	Text    string                `json:"text"`
}

// Preview is how the rules split the sample: the segments, and the text cut
// at every decided position with what decided it.
type Preview struct {
	Segments []string       `json:"segments"`
	Pieces   []PreviewPiece `json:"pieces"`
}

// PreviewPiece is a run of the sample text. Decision is about the position
// right after it; nil for the last piece.
type PreviewPiece struct {
	Text     string           `json:"text"`
	Decision *PreviewDecision `json:"decision"`
}

// PreviewDecision says whether a position breaks and why: Rule is the
// deciding rule's number (from 1), nil when a word list decided, and Word is
// the abbreviation that matched.
type PreviewDecision struct {
	Break  bool                        `json:"break"`
	Source segmentation.DecisionSource `json:"source"`
	Rule   *int                        `json:"rule"`
	Word   string                      `json:"word,omitempty"`
}
