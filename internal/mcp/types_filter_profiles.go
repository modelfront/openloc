package mcp

import (
	"context"

	"openloc/internal/filterprofile"
	"openloc/internal/parser"
)

// filterProfileTools is the Backend slice for filter profiles: every member
// reads; managers write.
type filterProfileTools interface {
	ListFilterProfiles(ctx context.Context, in ListFilterProfilesIn) (FilterProfileList, error)
	GetFilterProfile(ctx context.Context, in GetFilterProfileIn) (FilterProfileDetail, error)
	CreateFilterProfile(ctx context.Context, in CreateFilterProfileIn) (FilterProfileDetail, error)
	PreviewUpdateFilterProfile(ctx context.Context, in UpdateFilterProfileIn) (Preview, error)
	UpdateFilterProfile(ctx context.Context, in UpdateFilterProfileIn) (FilterProfileDetail, error)
	PreviewDeleteFilterProfile(ctx context.Context, in DeleteFilterProfileIn) (Preview, error)
	DeleteFilterProfile(ctx context.Context, in DeleteFilterProfileIn) (DeletedConfig, error)
	SetFilterProfileDefault(ctx context.Context, in SetFilterProfileDefaultIn) (FilterProfileDefaults, error)
}

type ListFilterProfilesIn struct {
	Format string `json:"format,omitempty" jsonschema:"keep one format's profiles: html, xlsx, json_i18n, po, txt or xml"`
}

type FilterProfileDetail struct {
	ProfileID        int64                 `json:"profile_id"`
	Name             string                `json:"name"`
	Format           string                `json:"format"`
	Builtin          bool                  `json:"builtin"`
	OrgDefault       bool                  `json:"org_default"`
	BuiltinKey       string                `json:"builtin_key,omitempty"`
	Description      string                `json:"description,omitempty"`
	BasedOnProfileID *int64                `json:"based_on_profile_id,omitempty"`
	Options          filterprofile.Options `json:"options"`
}

type FilterProfileList struct {
	Profiles []FilterProfileDetail `json:"profiles"`
}

type GetFilterProfileIn struct {
	ProfileID int64 `json:"profile_id" jsonschema:"the profile, from openloc_list_filter_profiles"`
}

// FilterOptionsIn are a profile's options as a tool takes them; only the
// fields of the profile's format count.
type FilterOptionsIn struct {
	InlineHTML *bool `json:"inline_html,omitempty" jsonschema:"json_i18n, po, txt and xlsx: tags in strings, such as <b>, become protected tags (default true). Tags only: attribute text is not translated and HTML profile options do not apply. false reads strings as plain text"`
	// XLSX: the rules for a workbook without language codes in row 1.
	XLSX *XLSXRulesIn        `json:"xlsx,omitempty" jsonschema:"xlsx: sheets (names; empty = every visible sheet), header_row (row 1 is not translated), columns (letters; empty = every text cell) and target_columns ([{language, column}]: each target language written into its own column, such as fr in C, next to exactly one source column in columns, which keeps its text; empty = each translation replaces its source cell; export refuses a language with no column). Empty rules ask the uploader"`
	HTML *parser.HTMLOptions `json:"html,omitempty" jsonschema:"html: attributes (element name to the attributes translated; only text attributes: alt, title, placeholder, abbr, label, summary, text aria-*, and value on input), meta_description and skip_pre (leave <pre> untranslated). Left out on create: the defaults"`
	XML  *parser.XMLOptions  `json:"xml,omitempty" jsonschema:"xml: inline_elements, no_translate ([{name, value}] attribute markers, each with a value), ref_elements, ref_attributes (cross-references kept as one tag) and skip_elements (left out, kept as is). Left out on create: the defaults"`
}

func (o *FilterOptionsIn) options() *filterprofile.Options {
	if o == nil {
		return nil
	}
	out := &filterprofile.Options{InlineHTML: o.InlineHTML, HTML: o.HTML, XML: o.XML}
	if o.XLSX != nil {
		out.XLSX = &filterprofile.XLSXRules{Sheets: o.XLSX.Sheets, HeaderRow: o.XLSX.HeaderRow, Columns: o.XLSX.Columns, TargetColumns: o.XLSX.TargetColumns}
	}
	return out
}

type CreateFilterProfileIn struct {
	Name          string           `json:"name"`
	Format        string           `json:"format,omitempty" jsonschema:"html, xlsx, json_i18n, po (also .pot), txt or xml; left out with from_profile_id, the copied profile's"`
	Options       *FilterOptionsIn `json:"options,omitempty" jsonschema:"the options; leave out to copy from_profile_id"`
	FromProfileID *int64           `json:"from_profile_id,omitempty" jsonschema:"copy this profile (a built-in or the organization's)"`
}

type UpdateFilterProfileIn struct {
	ProfileID int64            `json:"profile_id"`
	Name      *string          `json:"name,omitempty"`
	Options   *FilterOptionsIn `json:"options,omitempty" jsonschema:"replaces the whole options"`
	Confirm
}

type DeleteFilterProfileIn struct {
	ProfileID int64 `json:"profile_id"`
	Confirm
}

// XLSXRulesIn are filterprofile.XLSXRules with every field optional.
type XLSXRulesIn struct {
	Sheets        []string                  `json:"sheets,omitempty"`
	HeaderRow     bool                      `json:"header_row,omitempty"`
	Columns       []string                  `json:"columns,omitempty"`
	TargetColumns []parser.XLSXTargetColumn `json:"target_columns,omitempty"`
}

type SetFilterProfileDefaultIn struct {
	Format    string `json:"format" jsonschema:"html, xlsx, json_i18n, po (also .pot), txt or xml"`
	ProfileID *int64 `json:"profile_id" jsonschema:"a built-in or one of the organization's profiles of that format; null goes back to the built-in Default"`
}

type FilterProfileDefault struct {
	Format    string `json:"format"`
	ProfileID int64  `json:"profile_id"`
	Name      string `json:"name"`
}

type FilterProfileDefaults struct {
	Defaults []FilterProfileDefault `json:"defaults"`
}
