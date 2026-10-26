package common

import "context"

// Segment represents a single translatable unit extracted from a file.
type Segment struct {
	Key            string // e.g. JSON key path, XLIFF unit ID
	Text           string // source text (plain, for display/search/word count)
	RichText       string // source text with inline tags (XLIFF bpt/ept/ph); empty if no tags
	Target         string // pre-existing translation (e.g. MT already in XLIFF), empty if none
	TargetRichText string // target with inline tags (XLIFF bpt/ept/ph); empty if no tags
	Position       int    // order within the file
	Context        string // surrounding context if available
}

// Parser extracts translatable segments from a file and can reconstruct the file with translations.
type Parser interface {
	// Parse extracts segments from file content.
	Parse(content []byte) ([]Segment, error)

	// Reconstruct rebuilds the file with translated text.
	// translations maps segment key → translated text.
	Reconstruct(original []byte, translations map[string]string) ([]byte, error)
}

// ExportLangs are the languages of the job an export writes.
type ExportLangs struct {
	Source string
	Target string
}

// LangAwareReconstructor is a Parser whose export depends on the job's
// languages: an XLSX workbook with one column per language writes only the
// target's column, and a PowerPoint deck marks translated text with the
// target language. The export calls ReconstructForLang instead of
// Reconstruct.
type LangAwareReconstructor interface {
	ReconstructForLang(ctx context.Context, original []byte, translations map[string]string, langs ExportLangs) ([]byte, error)
}

// ExportError is a Reconstruct failure the user can fix, such as an
// import-only format or a workbook without a column for the job's language.
// Msg is written by the parser, never carries file content, and is shown to
// the user with a 400.
type ExportError struct {
	Msg string
}

func (e *ExportError) Error() string { return e.Msg }
