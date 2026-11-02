package mcp

import (
	"context"

	"openloc/internal/segmentation"
)

// segmentationTools is the Backend slice for segmentation profiles: every
// member reads, previews and exports; managers write and read SRX files.
type segmentationTools interface {
	ListSegmentationProfiles(ctx context.Context, in ListSegmentationProfilesIn) (SegmentationProfileList, error)
	GetSegmentationProfile(ctx context.Context, in GetSegmentationProfileIn) (SegmentationProfileDetail, error)
	PreviewSegmentation(ctx context.Context, in PreviewSegmentationIn) (SegmentationPreview, error)
	ExportSegmentationSRX(ctx context.Context, in GetSegmentationProfileIn) (SegmentationSRX, error)
	ReadSegmentationSRX(ctx context.Context, in ReadSegmentationSRXIn) (segmentation.SRXImport, error)
	CreateSegmentationProfile(ctx context.Context, in CreateSegmentationProfileIn) (SegmentationProfileDetail, error)
	PreviewUpdateSegmentationProfile(ctx context.Context, in UpdateSegmentationProfileIn) (Preview, error)
	UpdateSegmentationProfile(ctx context.Context, in UpdateSegmentationProfileIn) (SegmentationProfileDetail, error)
	PreviewDeleteSegmentationProfile(ctx context.Context, in DeleteSegmentationProfileIn) (Preview, error)
	DeleteSegmentationProfile(ctx context.Context, in DeleteSegmentationProfileIn) (DeletedConfig, error)
}

type ListSegmentationProfilesIn struct {
	SourceLanguage string `json:"source_language,omitempty" jsonschema:"keep the profiles that suit this language: its own, its base language's and those for any language"`
}

type SegmentationProfileSummary struct {
	ProfileID int64  `json:"profile_id"`
	Name      string `json:"name"`
	// SourceLanguages is empty on a profile that suits any language.
	SourceLanguages []string `json:"source_languages"`
	Builtin         bool     `json:"builtin"`
	BuiltinKey      string   `json:"builtin_key,omitempty"`
	// Suggested marks, in a list for one language, the profile a new project
	// in that language starts with.
	Suggested     bool   `json:"suggested,omitempty"`
	Mode          string `json:"mode"`
	Rules         int    `json:"rules"`
	Abbreviations int    `json:"abbreviations"`
}

type SegmentationProfileList struct {
	Profiles []SegmentationProfileSummary `json:"profiles"`
}

type GetSegmentationProfileIn struct {
	ProfileID int64 `json:"profile_id" jsonschema:"the profile, from openloc_list_segmentation_profiles"`
}

type SegmentationProfileDetail struct {
	SegmentationProfileSummary
	Description      string               `json:"description,omitempty"`
	BasedOnProfileID *int64               `json:"based_on_profile_id,omitempty"`
	Definition       segmentation.RuleSet `json:"definition"`
}

// SegmentationDefinitionIn is a profile's rules as a tool takes them.
type SegmentationDefinitionIn struct {
	Mode                string              `json:"mode,omitempty" jsonschema:"sentence (default) or paragraph: each block one segment"`
	Rules               []segmentation.Rule `json:"rules" jsonschema:"tried in order; the first whose before pattern matches the text before a position and whose after pattern matches the text after it decides (break true breaks, false keeps whole). Patterns are RE2 regular expressions: no look-around or back-references; empty matches anything"`
	Abbreviations       []string            `json:"abbreviations,omitempty" jsonschema:"words after which a full stop never ends a sentence, without the full stop; case-sensitive"`
	NumberAbbreviations []string            `json:"number_abbreviations,omitempty" jsonschema:"words that are abbreviations only before a number, as in p. 12"`
}

func (d *SegmentationDefinitionIn) ruleSet() *segmentation.RuleSet {
	if d == nil {
		return nil
	}
	return &segmentation.RuleSet{Mode: segmentation.Mode(d.Mode), Rules: d.Rules, Abbreviations: d.Abbreviations, NumberAbbreviations: d.NumberAbbreviations}
}

type CreateSegmentationProfileIn struct {
	Name            string                    `json:"name"`
	SourceLanguages []string                  `json:"source_languages" jsonschema:"language codes, such as fr and fr-CA; an empty list suits any language"`
	Definition      *SegmentationDefinitionIn `json:"definition,omitempty" jsonschema:"the rules; leave out to copy from_profile_id"`
	FromProfileID   *int64                    `json:"from_profile_id,omitempty" jsonschema:"copy this profile (a built-in or the organization's)"`
}

type UpdateSegmentationProfileIn struct {
	ProfileID       int64                     `json:"profile_id"`
	Name            *string                   `json:"name,omitempty"`
	SourceLanguages *[]string                 `json:"source_languages,omitempty" jsonschema:"replaces the language list; an empty list suits any language"`
	Definition      *SegmentationDefinitionIn `json:"definition,omitempty" jsonschema:"replaces the whole definition: mode, rules and both word lists"`
	Confirm
}

type DeleteSegmentationProfileIn struct {
	ProfileID int64 `json:"profile_id"`
	Confirm
}

type PreviewSegmentationIn struct {
	ProfileID  *int64                    `json:"profile_id,omitempty" jsonschema:"split with this stored profile"`
	Definition *SegmentationDefinitionIn `json:"definition,omitempty" jsonschema:"or with these unsaved rules"`
	Text       string                    `json:"text" jsonschema:"sample text, at most 20000 characters"`
}

// SegmentationPreview is how the rules split the sample, and what decided
// each position (rule numbers start at 1).
type SegmentationPreview struct {
	Segments  []string                      `json:"segments"`
	Decisions []SegmentationPreviewDecision `json:"decisions"`
}

type SegmentationPreviewDecision struct {
	// After is the text up to the position, from the previous decision.
	After  string `json:"after"`
	Break  bool   `json:"break"`
	Source string `json:"source"`
	Rule   *int   `json:"rule,omitempty"`
	Word   string `json:"word,omitempty"`
}

type ReadSegmentationSRXIn struct {
	SRX string `json:"srx" jsonschema:"the SRX file's XML"`
}

type SegmentationSRX struct {
	FileName string `json:"file_name"`
	SRX      string `json:"srx"`
}
