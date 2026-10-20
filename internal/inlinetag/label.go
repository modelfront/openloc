package inlinetag

import (
	"regexp"
	"strconv"
	"strings"
)

// Chip labels, mirroring chipLabel(attrs, 'numeric') in
// web/src/lib/xliff-tag-label.ts:
//
//	paired      <1> … </1>
//	standalone  {1}
//	placeholder %s, {count}     — a <ph>'s own content, shown verbatim
//
// A placeholder shows its content rather than a number because that is the only
// thing telling `%s` from `{count}`; a bare `{2}` hides what the reader needs in
// order to place it.

// pairedTypes are the elements whose chips read as markup rather than as
// placeholders. Matches PAIRED_TAG_TYPES in xliff-tag-label.ts.
var pairedTypes = map[string]bool{
	"g": true, "bpt": true, "ept": true, "it": true,
	"i": true, "b": true, "u": true, "a": true, "span": true,
}

// phContentRe pulls `%s` out of `<ph id="2">%s</ph>`. `it` is paired, so only
// `ph` reaches this in practice; the FE regex covers both and so does this one.
var phContentRe = regexp.MustCompile(`(?s)^<(?:ph|it)\b[^>]*>(.+)</(?:ph|it)>$`)

// Label is the chip the editor shows for a tag.
func Label(t Tag) string {
	paired := pairedTypes[t.Kind]

	// A placeholder's content outranks its number — it identifies which
	// variable this is. Shown verbatim: the content usually carries its own
	// delimiters (`{key}`, `%s`), so wrapping it again would read as `{{key}}`.
	if !paired {
		if c := phContent(t.Raw); c != "" {
			return c
		}
	}

	body := t.ID
	if body == "" {
		body = strconv.Itoa(t.Num)
	}
	if !paired {
		return "{" + body + "}"
	}
	if t.Closing {
		return "</" + body + ">"
	}
	return "<" + body + ">"
}

func phContent(raw string) string {
	m := phContentRe.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	return decodeEntities(m[1])
}

// decodeEntities undoes the XML escaping the parsers apply. `&amp;` goes LAST
// so an escaped `&amp;lt;` does not collapse into a `<`.
func decodeEntities(s string) string {
	r := strings.NewReplacer("&quot;", `"`, "&lt;", "<", "&gt;", ">")
	return strings.ReplaceAll(r.Replace(s), "&amp;", "&")
}

// numericLabelRe matches the label shapes Decode can recognize as "meant to be
// a chip" — `{4}`, `<4>`, `</4>`. Placeholder-content labels (`%s`) are not
// matchable this way and are found by exact lookup instead.
//
// The body is the id charset: XLIFF ids are usually digits but imported files
// carry arbitrary short strings. Whitespace is excluded so a real sentence in
// angle brackets cannot be mistaken for a chip.
var numericLabelRe = regexp.MustCompile(`</?[A-Za-z0-9._:-]{1,32}>|\{[A-Za-z0-9._:-]{1,32}\}`)
