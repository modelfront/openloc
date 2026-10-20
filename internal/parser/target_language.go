package parser

// FileTargetLanguage returns the target language a bilingual file names for
// its translations, as written in the file: an XLIFF <file target-language>,
// a PO header's Language field. "" when the format carries none or the file
// leaves it out.
func FileTargetLanguage(format string, content []byte) string {
	switch format {
	case "xliff":
		lang, err := XLIFFTargetLanguage(content)
		if err != nil {
			return ""
		}
		return lang
	case "po", "pot":
		return POTargetLanguage(content)
	}
	return ""
}
