package common

import (
	"strconv"
	"strings"
)

type ChipTag struct {
	Name        string
	Id          int
	Ctype       string
	Closing     bool
	SelfClosing bool
}

// ScanChipTag parses an XLIFF chip tag starting at text[i], returning the tag
// and the index just past it. ok is false when text[i] doesn't start a chip.
func ScanChipTag(text string, i int) (ChipTag, int, bool) {
	end := strings.IndexByte(text[i:], '>')
	if end < 0 {
		return ChipTag{}, 0, false
	}
	end += i
	raw := text[i : end+1]

	body := strings.TrimSuffix(strings.TrimPrefix(raw, "<"), ">")
	tag := ChipTag{}
	if strings.HasPrefix(body, "/") {
		tag.Closing = true
		body = strings.TrimPrefix(body, "/")
	}
	if strings.HasSuffix(body, "/") {
		tag.SelfClosing = true
		body = strings.TrimSuffix(body, "/")
	}
	body = strings.TrimSpace(body)

	name, attrs, _ := strings.Cut(body, " ")
	if name != "g" && name != "x" {
		return ChipTag{}, 0, false
	}
	tag.Name = name
	tag.Id = parseChipID(attrs)
	tag.Ctype = chipAttr(attrs, "ctype")
	return tag, end + 1, true
}

// parseChipID pulls the numeric id out of a chip tag's attribute text.
func parseChipID(attrs string) int {
	id, err := strconv.Atoi(chipAttr(attrs, "id"))
	if err != nil {
		return 0
	}
	return id
}

// chipAttr returns the value of the double-quoted attribute name in a chip
// tag's attribute text, or "" when it is absent.
func chipAttr(attrs, name string) string {
	key := name + `="`
	for k := 0; k < len(attrs); {
		j := strings.Index(attrs[k:], key)
		if j < 0 {
			return ""
		}
		j += k
		// Only a whole attribute name counts: "id" must not match "ref_id".
		if j == 0 || isTagSpace(attrs[j-1]) {
			rest := attrs[j+len(key):]
			q := strings.IndexByte(rest, '"')
			if q < 0 {
				return ""
			}
			return rest[:q]
		}
		k = j + len(key)
	}
	return ""
}
