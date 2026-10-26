package common

// ImportString splits a string read from a file into its plain and rich text,
// as DetectInlineTags does; a plain-text string has no chips.
func ImportString(text string, plainText bool) (plain, rich string) {
	if plainText {
		return text, ""
	}
	return DetectInlineTags(text)
}

// ExportString is what the export writes for translated, whose source string
// in the file is source.
func ExportString(translated, source string, plainText bool) string {
	if plainText {
		return translated
	}
	return SourceInlineMarkup(translated, source)
}
