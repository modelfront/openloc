package segmentation

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

// The built-in rule sets ship embedded and read-only. A released set never
// changes: an improvement is a new file with the next version in its id.
// builtin_test.go pins every file's hash.

//go:embed builtin/*.json
var builtinFS embed.FS

const (
	// ConservativeID is the set every file stored without a snapshot is
	// parsed with, so its exports keep their sentence keys.
	ConservativeID = "builtin:conservative@1"
	// DefaultID is the set for a language with no other set; a later
	// version keeps DefaultKeyPrefix.
	DefaultID        = "builtin:default@1"
	DefaultKeyPrefix = "builtin:default@"

	builtinPrefix = "builtin:"
)

// Builtin is a read-only rule set that ships with OpenLoc. SourceLanguage is
// empty for a set that suits any language.
type Builtin struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	SourceLanguage string  `json:"source_language"`
	Description    string  `json:"description"`
	RuleSet        RuleSet `json:"rule_set"`
}

var builtins = sync.OnceValue(func() []Builtin {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		panic(err)
	}
	var out []Builtin
	for _, e := range entries {
		raw, err := builtinFS.ReadFile(path.Join("builtin", e.Name()))
		if err != nil {
			panic(err)
		}
		var b Builtin
		if err := json.Unmarshal(raw, &b); err != nil {
			panic(fmt.Sprintf("built-in %s: %v", e.Name(), err))
		}
		if want := builtinPrefix + strings.TrimSuffix(e.Name(), ".json"); b.ID != want {
			panic(fmt.Sprintf("built-in %s has id %q, want %q", e.Name(), b.ID, want))
		}
		if err := Validate(b.RuleSet); err != nil {
			panic(fmt.Sprintf("built-in %s: %v", b.ID, err))
		}
		out = append(out, b)
	}
	// Language-neutral sets first (Default before Conservative), then by
	// language.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceLanguage != out[j].SourceLanguage {
			return out[i].SourceLanguage < out[j].SourceLanguage
		}
		if out[i].ID == DefaultID || out[j].ID == DefaultID {
			return out[i].ID == DefaultID
		}
		return out[i].ID < out[j].ID
	})
	return out
})

// Builtins returns every built-in set.
func Builtins() []Builtin {
	return append([]Builtin(nil), builtins()...)
}

// BuiltinByID returns the built-in set with id.
func BuiltinByID(id string) (Builtin, bool) {
	for _, b := range builtins() {
		if b.ID == id {
			return b, true
		}
	}
	return Builtin{}, false
}

// Conservative returns the compiled ConservativeID set.
var Conservative = sync.OnceValue(func() *Segmenter {
	b, ok := BuiltinByID(ConservativeID)
	if !ok {
		panic("missing built-in " + ConservativeID)
	}
	return MustCompile(b.RuleSet)
})
