package mcp

type TMHitList struct {
	Matches []TMHit `json:"matches"`
	Count   int     `json:"count"`
}

type TermHitList struct {
	Terms []TermHit `json:"terms"`
	Count int       `json:"count"`
}

type SearchTMIn struct {
	ProjectID      int64  `json:"project_id" jsonschema:"the project whose assigned translation memories to search; determines which TMs are in scope"`
	Query          string `json:"query" jsonschema:"the text to search for; matches as a substring of stored source or target text"`
	SourceLanguage string `json:"source_language,omitempty" jsonschema:"BCP-47 source language, e.g. en or en-US. Defaults to the project's source language."`
	TargetLanguage string `json:"target_language" jsonschema:"BCP-47 target language to search translations for, e.g. de or pt-BR"`
	Limit          int    `json:"limit,omitempty" jsonschema:"max matches to return (default 50, max 200)"`
}

// LookupConceptIn looks up concepts inside ONE term base by exact term text — a
// project-independent way to resolve a concept_id before editing it. Unlike
// openloc_lookup_term (which is project-scoped and tokenizes source text for QA
// matching), this is an EXACT, case-sensitive match on a whole term's text.
type LookupConceptIn struct {
	TermBaseID int64  `json:"term_base_id" jsonschema:"the term base to search within"`
	Text       string `json:"text" jsonschema:"exact term text to match (case-sensitive, whole term — not a substring). Matches a term in any language of the base unless language is set."`
	Language   string `json:"language,omitempty" jsonschema:"optional BCP-47 language to restrict the text match to, e.g. en or de. Omit to match the text in any language."`
	// Pointer so 'omit' (any domain) is distinct from '' (only concepts with no
	// domain) — homographs are separated by domain, so this is how you target one.
	Domain *string `json:"domain,omitempty" jsonschema:"optional exact domain filter. Omit to match any domain; send \"\" to match only concepts with no domain; send e.g. \"finance\" for that domain."`
}

// ConceptMatchTerm is one term of a matched concept.
type ConceptMatchTerm struct {
	TermID   int64  `json:"term_id"`
	Language string `json:"language"`
	Text     string `json:"text"`
	Status   string `json:"status"`
}

// ConceptMatch is one concept that matched, with all its terms so a model can tell
// homographs apart (same text, different meaning/domain) before picking an id.
type ConceptMatch struct {
	ConceptID  int64              `json:"concept_id"`
	Definition string             `json:"definition"`
	Domain     string             `json:"domain"`
	Terms      []ConceptMatchTerm `json:"terms"`
}

type ConceptMatchList struct {
	Concepts []ConceptMatch `json:"concepts"`
	Count    int            `json:"count"`
	Hint     string         `json:"hint,omitempty"`
}

type LookupTermIn struct {
	ProjectID      int64  `json:"project_id" jsonschema:"the project whose assigned term bases to search"`
	SourceText     string `json:"source_text" jsonschema:"the source text to find approved terminology in; tokenized server-side into word n-grams"`
	SourceLanguage string `json:"source_language,omitempty" jsonschema:"BCP-47 source language. Defaults to the project's source language."`
	TargetLanguage string `json:"target_language" jsonschema:"BCP-47 target language to return terms for"`
}

type CreateTermBaseIn struct {
	Name           string `json:"name" jsonschema:"the term base name"`
	SourceLanguage string `json:"source_language" jsonschema:"source language code"`
	Description    string `json:"description,omitempty"`
}

type CreateTermBaseResult struct {
	TermBaseID      int64    `json:"term_base_id"`
	Name            string   `json:"name"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"` // derived from terms; empty at creation
}

type AddTermIn struct {
	TermBaseID     int64  `json:"term_base_id" jsonschema:"the term base to add to, from openloc_lookup_term or the term bases list"`
	SourceTerm     string `json:"source_term" jsonschema:"the source-language term (in the base's source language)"`
	TargetTerm     string `json:"target_term" jsonschema:"the approved translation"`
	TargetLanguage string `json:"target_language" jsonschema:"BCP-47 language of target_term, e.g. de or pt-BR"`
	Status         string `json:"status,omitempty" jsonschema:"approved (default), pending, or forbidden (forbidden = this translation must NOT be used)"`
	Notes          string `json:"notes,omitempty"`
	Domain         string `json:"domain,omitempty"`
}

type AddTermResult struct {
	TermBaseID int64 `json:"term_base_id"`
	// ConceptID is the concept the pair landed in — a NEW one, or the existing
	// concept it merged into (same source term + domain). Returned so a model can
	// chain straight into openloc_update_concept or openloc_add_term_to_concept
	// without a separate lookup.
	ConceptID  int64  `json:"concept_id"`
	TermID     int64  `json:"term_id"` // the target term's id
	SourceTerm string `json:"source_term"`
	TargetTerm string `json:"target_term"`
	Status     string `json:"status"`
}

// ConceptTermIn is one term of a concept, in one language, as supplied by a model.
// Unlike the bilingual add_term, status is REQUIRED here: a model authoring
// terminology must state approved/pending/forbidden explicitly rather than fall
// through to a silent default.
type ConceptTermIn struct {
	Language     string `json:"language" jsonschema:"BCP-47 language of this term, e.g. en, de, or pt-BR"`
	Text         string `json:"text" jsonschema:"the term text in this language"`
	Status       string `json:"status" jsonschema:"required: approved, pending, or forbidden (forbidden = this term must NOT be used and QA will flag it)"`
	PartOfSpeech string `json:"part_of_speech,omitempty" jsonschema:"optional part of speech, e.g. noun, verb"`
	Notes        string `json:"notes,omitempty"`
}

// CreateConceptIn adds a full multilingual concept to a term base. A concept is
// one meaning; its terms are that meaning expressed in each language (and
// synonyms within a language).
type CreateConceptIn struct {
	TermBaseID int64           `json:"term_base_id" jsonschema:"the term base to add the concept to"`
	Definition string          `json:"definition,omitempty" jsonschema:"optional concept-level definition/gloss shared by all terms"`
	Domain     string          `json:"domain,omitempty" jsonschema:"optional subject domain, e.g. legal, medical"`
	Terms      []ConceptTermIn `json:"terms" jsonschema:"the concept's terms across languages; at least one required"`
}

type CreateConceptResult struct {
	TermBaseID int64 `json:"term_base_id"`
	ConceptID  int64 `json:"concept_id"`
	Terms      int   `json:"terms"` // number of terms inserted (after de-duplication)
}

// AddTermToConceptIn appends one term (in one language) to an existing concept —
// e.g. a synonym or a newly-decided target-language term for a meaning that
// already exists. Use openloc_create_concept to make a new concept.
type AddTermToConceptIn struct {
	TermBaseID   int64  `json:"term_base_id" jsonschema:"the term base the concept belongs to"`
	ConceptID    int64  `json:"concept_id" jsonschema:"the concept to add the term to, from openloc_lookup_term (hits sharing a concept_id) or openloc_create_concept"`
	Language     string `json:"language" jsonschema:"BCP-47 language of the term, e.g. de or pt-BR"`
	Text         string `json:"text" jsonschema:"the term text in this language"`
	Status       string `json:"status" jsonschema:"required: approved, pending, or forbidden (forbidden = this term must NOT be used and QA will flag it)"`
	PartOfSpeech string `json:"part_of_speech,omitempty" jsonschema:"optional part of speech, e.g. noun, verb"`
	Notes        string `json:"notes,omitempty"`
}

type AddTermToConceptResult struct {
	TermBaseID int64  `json:"term_base_id"`
	ConceptID  int64  `json:"concept_id"`
	TermID     int64  `json:"term_id"`
	Language   string `json:"language"`
	Text       string `json:"text"`
	Status     string `json:"status"`
}

// UpdateConceptIn edits an existing concept's definition and/or domain. Fields are
// pointers so a model can change one without wiping the other: omit a field to
// leave it unchanged, or send "" to clear it. Term text/status are edited via the
// term tools, not here.
type UpdateConceptIn struct {
	TermBaseID int64   `json:"term_base_id" jsonschema:"the term base the concept belongs to"`
	ConceptID  int64   `json:"concept_id" jsonschema:"the concept to edit, from openloc_lookup_term or openloc_create_concept"`
	Definition *string `json:"definition,omitempty" jsonschema:"new concept-level definition/gloss; omit to leave unchanged, send \"\" to clear"`
	Domain     *string `json:"domain,omitempty" jsonschema:"new subject domain; omit to leave unchanged, send \"\" to clear"`
}

type UpdateConceptResult struct {
	TermBaseID int64  `json:"term_base_id"`
	ConceptID  int64  `json:"concept_id"`
	Definition string `json:"definition"`
	Domain     string `json:"domain"`
}

// UpdateTermIn edits one existing term: its text, status, part of speech, and/or
// notes. Fields are pointers for partial update — omit to leave unchanged. A
// term's language is immutable (change it by removing the term and adding a new
// one), matching the web editor.
type UpdateTermIn struct {
	TermBaseID   int64   `json:"term_base_id" jsonschema:"the term base the term belongs to"`
	ConceptID    int64   `json:"concept_id" jsonschema:"the concept the term belongs to"`
	TermID       int64   `json:"term_id" jsonschema:"the term to edit, from openloc_lookup_concept, openloc_add_term, or openloc_add_term_to_concept"`
	Text         *string `json:"text,omitempty" jsonschema:"new term text; omit to leave unchanged (it cannot be set empty)"`
	Status       *string `json:"status,omitempty" jsonschema:"new status: approved, pending, or forbidden; omit to leave unchanged"`
	PartOfSpeech *string `json:"part_of_speech,omitempty" jsonschema:"new part of speech, e.g. noun; omit to leave unchanged, send \"\" to clear"`
	Notes        *string `json:"notes,omitempty" jsonschema:"new notes; omit to leave unchanged, send \"\" to clear"`
}

type UpdateTermResult struct {
	TermBaseID   int64  `json:"term_base_id"`
	ConceptID    int64  `json:"concept_id"`
	TermID       int64  `json:"term_id"`
	Language     string `json:"language"`
	Text         string `json:"text"`
	Status       string `json:"status"`
	PartOfSpeech string `json:"part_of_speech"`
	Notes        string `json:"notes"`
}

type TMHit struct {
	EntryID        int64  `json:"entry_id"`
	SourceText     string `json:"source_text"`
	TargetText     string `json:"target_text"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	TMName         string `json:"tm_name"`
}

type TermHit struct {
	TermID       int64  `json:"term_id"`
	ConceptID    int64  `json:"concept_id"` // hits sharing a concept_id are synonyms for one meaning
	SourceTerm   string `json:"source_term"`
	TargetTerm   string `json:"target_term"`
	Status       string `json:"status"`
	Notes        string `json:"notes"`
	Domain       string `json:"domain"`
	TermBaseName string `json:"term_base_name"`
}
