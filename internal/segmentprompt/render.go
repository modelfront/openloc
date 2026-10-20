// Package segmentprompt builds the per-segment user message an LLM engine
// sends — a JSON object carrying that segment's context.
//
// The shape is generated, not authored. A system prompt names which optional
// context each segment should carry (ai_prompts.segment_context) and the object
// is built from that, so the body is always valid JSON and always matches what
// the prompt page shows.
//
// Deliberately pure (no DB, no pipeline types): the caller decides where each
// value comes from, and Fields lets it skip the lookups a given prompt doesn't
// need — a prompt without terms must not cost a glossary query.
package segmentprompt

import (
	"encoding/json"
	"strings"

	"openloc/internal/jsonutil"
)

// The context keys, in the order they appear in the generated object. Source
// and translation come first because they are what the model is working on;
// the rest is supporting context.
const (
	// KeySourceText and KeyTargetText are implied by the output mode rather
	// than chosen — see FieldsFor.
	KeySourceText = "source_text"
	KeyTargetText = "target_text"

	// The optional keys, stored per prompt.
	KeyTMMatch = "tm_match"
	KeyTerms   = "terms"
)

// Optional is every key a prompt may switch on, in canonical order. Single
// source of truth for validation, for the checkbox list, and for the order of
// the generated object.
//
// Both describe the SOURCE, which is why they suit every mode: a TM match and
// the glossary terms exist before anything has been translated.
var Optional = []string{KeyTMMatch, KeyTerms}

// ModeMT is the one output mode that produces a translation from nothing.
// Mirrors ai.ModeMT, which this package must not import.
const ModeMT = "mt"

// jsonKey is the name a context key takes in the generated object. The wire
// names are shorter than the storage keys: the model reads "source", not
// "source_text".
var jsonKey = map[string]string{
	KeySourceText: "source",
	KeyTargetText: "translation",
	KeyTMMatch:    "tm_matches",
	KeyTerms:      "terms",
}

// Fields is the resolved set of context a prompt sends per segment, in
// generation order.
type Fields []string

// Has reports whether f carries key. Callers gate their (sometimes expensive)
// value lookups on this: no TM match in the set, no TM lookup.
func (f Fields) Has(key string) bool {
	for _, k := range f {
		if k == key {
			return true
		}
	}
	return false
}

// FieldsFor resolves a prompt's stored context into the full field set for an
// output mode.
//
// The source text is always sent — there is nothing to translate or judge
// without it. The translation is sent for every mode except MT, where it does
// not exist yet. Neither is a tenant choice, so neither is stored; storing them
// would let a prompt be saved in a state that cannot run.
//
// Unknown stored keys are dropped rather than passed through: segment_context
// is written by an older build's UI as easily as by this one, and a key the
// generator does not understand would reach the model as an empty member.
func FieldsFor(mode string, stored []string) Fields {
	out := Fields{KeySourceText}
	if mode != ModeMT {
		out = append(out, KeyTargetText)
	}
	for _, key := range Optional {
		for _, s := range stored {
			if s == key {
				out = append(out, key)
				break
			}
		}
	}
	return out
}

// Term is one glossary entry matching the segment's source text.
type Term struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// Status is the target term's standing — "approved", "forbidden",
	// "pending". It travels because a forbidden term is the one the model most
	// needs to know about.
	Status string `json:"status"`
}

// TMMatch is one translation memory entry close to the segment's source text.
type TMMatch struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// Score is the match percent after penalties (0–100), the number the Match
	// column shows. It travels because penalties — a distrusted TM, a regional
	// variant — leave the texts unchanged, so the model cannot see them otherwise.
	Score int `json:"score"`
}

// Values are the resolved context for one segment. A typed struct rather than a
// map because terms is not a string, and because a stringly-typed bag is how
// the next context field ends up misspelled at one of its two call sites.
type Values struct {
	SourceText string
	TargetText string
	// TMMatches is a list so more matches can be sent without changing the
	// shape the model and prompt authors rely on; today it holds the best one.
	TMMatches []TMMatch
	Terms     []Term
}

// Build renders one segment's user-message body as a JSON object.
//
// A field whose value is empty is omitted entirely: `"tm_matches": []` on every
// segment without one is noise that invites the model to fill it in.
func Build(fields Fields, v Values) json.RawMessage {
	obj := make(orderedObject, 0, len(fields))
	add := func(key string, val any) {
		obj = append(obj, jsonPair{Key: jsonKey[key], Val: val})
	}
	for _, key := range fields {
		switch key {
		case KeySourceText:
			if s := strings.TrimSpace(v.SourceText); s != "" {
				add(key, v.SourceText)
			}
		case KeyTargetText:
			if s := strings.TrimSpace(v.TargetText); s != "" {
				add(key, v.TargetText)
			}
		case KeyTMMatch:
			// Both halves travel together or neither does:
			// from the model's side an absent target is ambiguous — "identical,
			// so removed" and "no match" look the same — whereas sending both
			// lets it verify that the segment inherited this entry.
			var matches []TMMatch
			for _, m := range v.TMMatches {
				if strings.TrimSpace(m.Source) != "" || strings.TrimSpace(m.Target) != "" {
					matches = append(matches, m)
				}
			}
			if len(matches) > 0 {
				add(key, matches)
			}
		case KeyTerms:
			if len(v.Terms) > 0 {
				add(key, v.Terms)
			}
		}
	}
	body, err := obj.MarshalJSON()
	if err != nil {
		// orderedObject of strings cannot fail to marshal; a JSON null keeps
		// Blank's check honest if that ever changes.
		return json.RawMessage("null")
	}
	return body
}

// Blank reports whether a body carries nothing the model can use — an object
// whose every key dropped out, or null. Callers fall back to their built-in
// per-segment body instead of sending it.
func Blank(body json.RawMessage) bool {
	switch strings.TrimSpace(string(body)) {
	case "", "null", "{}":
		return true
	}
	return false
}

// orderedObject marshals as a JSON object with its members in slice order,
// which encoding/json's map handling cannot do (it sorts keys). Order matters
// twice over: it is what the prompt page shows the author, and a map-based
// implementation would reshuffle it per request, defeating prompt caching on
// the provider side.
type orderedObject []jsonPair

type jsonPair struct {
	Key string
	Val any
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, p := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := jsonutil.MarshalNoEscape(p.Key)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		val, err := jsonutil.MarshalNoEscape(p.Val)
		if err != nil {
			return nil, err
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}
