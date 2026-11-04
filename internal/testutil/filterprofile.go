package testutil

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
)

// FilterChoices is a settings row's filter profile pick per format; a format
// left out uses its Default.
func FilterChoices(t testing.TB, q db.Querier, settingsProfileID int64) map[string]int64 {
	t.Helper()
	choices, err := filterprofile.Choices(Ctx(), q, settingsProfileID)
	if err != nil {
		t.Fatalf("filter profile choices: %v", err)
	}
	return ChoiceIDs(choices)
}

// ChoiceIDs is choices as a format → id map.
func ChoiceIDs(choices []filterprofile.Choice) map[string]int64 {
	out := make(map[string]int64, len(choices))
	for _, c := range choices {
		out[c.Format] = c.ID
	}
	return out
}

// BuiltinFilterProfile is the id of the built-in filter profile with key,
// such as "builtin:json_i18n-plain@1".
func BuiltinFilterProfile(t testing.TB, q db.Querier, key string) int64 {
	t.Helper()
	row, err := q.GetBuiltinFilterProfileByKey(Ctx(), &key)
	if err != nil {
		t.Fatalf("built-in filter profile %s: %v", key, err)
	}
	return row.ID
}
