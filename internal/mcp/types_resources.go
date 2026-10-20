package mcp

import "context"

// resourceTools is the Backend slice for the translation memories, TM profiles, term bases tools.
type resourceTools interface {
	ListTMs(context.Context, ListTMsIn) (TMList, error)
	GetTM(context.Context, GetTMIn) (TMDetail, error)
	ListTMEntries(context.Context, ListTMEntriesIn) (TMEntryPage, error)
	ListTMImports(context.Context, GetTMIn) (TMImportList, error)
	CreateTM(context.Context, CreateTMIn) (TMSummary, error)
	RenameTM(context.Context, RenameTMIn) (TMSummary, error)
	UpdateTMEntry(context.Context, UpdateTMEntryIn) (TMEntry, error)
	ImportTMX(context.Context, ImportTMXIn) (TMImport, error)
	ExportTMX(context.Context, GetTMIn) (ResourceExport, error)
	PreviewDeleteTM(context.Context, DeleteTMIn) (Preview, error)
	DeleteTM(context.Context, DeleteTMIn) (ResourceDeleted, error)

	ListTMProfiles(context.Context, ListTMProfilesIn) (TMProfileList, error)
	CreateTMProfile(context.Context, CreateTMProfileIn) (TMProfileSummary, error)
	PreviewUpdateTMProfile(context.Context, UpdateTMProfileIn) (Preview, error)
	UpdateTMProfile(context.Context, UpdateTMProfileIn) (TMProfileSummary, error)
	PreviewDeleteTMProfile(context.Context, DeleteTMProfileIn) (Preview, error)
	DeleteTMProfile(context.Context, DeleteTMProfileIn) (ResourceDeleted, error)

	ListTermBases(context.Context, ListTermBasesIn) (TermBaseList, error)
	GetTermBase(context.Context, GetTermBaseIn) (TermBaseDetail, error)
	ListConcepts(context.Context, ListConceptsIn) (ConceptPage, error)
	GetConcept(context.Context, GetConceptIn) (ConceptDetail, error)
	RenameTermBase(context.Context, RenameTermBaseIn) (TermBaseSummary, error)
	ImportTerms(context.Context, ImportTermsIn) (ImportTermsResult, error)
	ExportTerms(context.Context, ExportTermsIn) (ResourceExport, error)
	PreviewDeleteTermBase(context.Context, DeleteTermBaseIn) (Preview, error)
	DeleteTermBase(context.Context, DeleteTermBaseIn) (ResourceDeleted, error)
	PreviewDeleteConcept(context.Context, DeleteConceptIn) (Preview, error)
	DeleteConcept(context.Context, DeleteConceptIn) (ResourceDeleted, error)
	PreviewDeleteTerm(context.Context, DeleteTermIn) (Preview, error)
	DeleteTerm(context.Context, DeleteTermIn) (ResourceDeleted, error)
}

// ResourceExport is where the browser downloads an export: an existing REST
// GET route, opened with the user's own session.
type ResourceExport struct {
	DownloadPath string `json:"download_path"`
	Filename     string `json:"filename"`
}

// ResourceDeleted is the result of a guarded delete.
type ResourceDeleted struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// --- Translation memories ---------------------------------------------------

type ListTMsIn struct{}

type TMSummary struct {
	TMID            int64    `json:"tm_id"`
	Name            string   `json:"name"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	EntryCount      int64    `json:"entry_count"`
	CustomerID      int64    `json:"customer_id,omitempty"`
	CustomerName    string   `json:"customer_name,omitempty"`
	CreatedBy       string   `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
}

type TMList struct {
	TMs   []TMSummary `json:"tms"`
	Count int         `json:"count"`
}

type GetTMIn struct {
	TMID int64 `json:"tm_id" jsonschema:"the translation memory, from openloc_list_tms"`
}

type TMLanguagePair struct {
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
}

type TMDetail struct {
	TMSummary
	// LanguagePairs are the pairs its entries actually hold.
	LanguagePairs []TMLanguagePair `json:"language_pairs"`
	// Assignments counts the project, template and TM profile settings that use it.
	Assignments int32 `json:"assignments"`
}

type ListTMEntriesIn struct {
	TMID           int64  `json:"tm_id" jsonschema:"the translation memory, from openloc_list_tms"`
	Query          string `json:"query,omitempty" jsonschema:"optional substring to find in the source or target text"`
	SourceLanguage string `json:"source_language,omitempty" jsonschema:"optional BCP-47 source language filter, e.g. en"`
	TargetLanguage string `json:"target_language,omitempty" jsonschema:"optional BCP-47 target language filter, e.g. de"`
	Cursor         int64  `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; omit for the first page"`
	Limit          int    `json:"limit,omitempty" jsonschema:"entries per page (default 50, max 200)"`
}

type TMEntry struct {
	EntryID        int64  `json:"entry_id"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	SourceText     string `json:"source_text"`
	TargetText     string `json:"target_text"`
	// HasTags is true when the entry carries inline tags (rich text).
	HasTags   bool   `json:"has_tags"`
	UpdatedBy string `json:"updated_by"`
	// Version is the optimistic-lock token for openloc_update_tm_entry.
	Version string `json:"version"`
}

type TMEntryPage struct {
	Entries    []TMEntry `json:"entries"`
	NextCursor int64     `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
	// Total is left out on later search pages and when counting ran too long.
	Total *int64 `json:"total,omitempty"`
}

type TMImport struct {
	ImportID     int64  `json:"import_id"`
	TMID         int64  `json:"tm_id"`
	FileName     string `json:"file_name"`
	Status       string `json:"status"`
	TotalUnits   int32  `json:"total_units"`
	Imported     int32  `json:"imported"`
	Skipped      int32  `json:"skipped"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	Hint         string `json:"hint,omitempty"`
}

type TMImportList struct {
	Imports []TMImport `json:"imports"`
	Count   int        `json:"count"`
}

type CreateTMIn struct {
	Name            string   `json:"name" jsonschema:"the translation memory name"`
	SourceLanguage  string   `json:"source_language" jsonschema:"BCP-47 source language, e.g. en"`
	TargetLanguages []string `json:"target_languages" jsonschema:"one or more BCP-47 target languages, e.g. [\"de\", \"fr\"]; none may equal the source"`
	CustomerID      int64    `json:"customer_id,omitempty" jsonschema:"optional customer, from openloc_list_customers"`
}

type RenameTMIn struct {
	TMID int64  `json:"tm_id" jsonschema:"the translation memory, from openloc_list_tms"`
	Name string `json:"name" jsonschema:"the new name"`
}

type UpdateTMEntryIn struct {
	TMID       int64  `json:"tm_id" jsonschema:"the translation memory, from openloc_list_tms"`
	EntryID    int64  `json:"entry_id" jsonschema:"the entry, from openloc_list_tm_entries"`
	SourceText string `json:"source_text" jsonschema:"the full new source text (send the current text to keep it)"`
	TargetText string `json:"target_text" jsonschema:"the full new target text (send the current text to keep it)"`
	Version    string `json:"version,omitempty" jsonschema:"the entry's version from openloc_list_tm_entries. When set, the edit is refused if someone changed the entry since; recommended."`
}

type ImportTMXIn struct {
	TMID int64 `json:"tm_id" jsonschema:"the translation memory to import into, from openloc_list_tms"`
	UploadRef
}

type DeleteTMIn struct {
	TMID int64 `json:"tm_id" jsonschema:"the translation memory to delete, from openloc_list_tms"`
	Confirm
}

// --- TM profiles ------------------------------------------------------------

type ListTMProfilesIn struct{}

type TMProfileTM struct {
	TMID       int64  `json:"tm_id"`
	Mode       string `json:"mode"`
	Priority   int32  `json:"priority"`
	PenaltyPct int32  `json:"penalty_pct"`
}

type TMProfileSummary struct {
	TMProfileID           int64         `json:"tm_profile_id"`
	Name                  string        `json:"name"`
	Description           string        `json:"description"`
	FuzzyThreshold        float32       `json:"fuzzy_threshold"`
	SublanguagePenaltyPct int32         `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   int32         `json:"near_exact_penalty_pct"`
	TMs                   []TMProfileTM `json:"tms"`
	// UsageCount is how many projects and templates follow the profile.
	UsageCount int32 `json:"usage_count"`
}

type TMProfileList struct {
	Profiles []TMProfileSummary `json:"profiles"`
	Count    int                `json:"count"`
}

type TMProfileTMIn struct {
	TMID       int64  `json:"tm_id" jsonschema:"a translation memory, from openloc_list_tms"`
	Mode       string `json:"mode,omitempty" jsonschema:"read (lookups only), write (new translations only) or read_write (default)"`
	Priority   int32  `json:"priority,omitempty" jsonschema:"lookup order; lower comes first"`
	PenaltyPct int32  `json:"penalty_pct,omitempty" jsonschema:"percentage docked from every match of this TM, 0-100 (default 0)"`
}

type CreateTMProfileIn struct {
	Name                  string          `json:"name" jsonschema:"the profile name, unique in the organization"`
	Description           string          `json:"description,omitempty"`
	FuzzyThreshold        *float32        `json:"fuzzy_threshold,omitempty" jsonschema:"lowest match score shown, 0.0-1.0 (default 0.75)"`
	SublanguagePenaltyPct *int32          `json:"sublanguage_penalty_pct,omitempty" jsonschema:"penalty for a match from another variant of the language (e.g. de-AT for de-DE), 0-100"`
	NearExactPenaltyPct   *int32          `json:"near_exact_penalty_pct,omitempty" jsonschema:"penalty for a match that differs only in numbers, tags or punctuation, 0-100"`
	TMs                   []TMProfileTMIn `json:"tms,omitempty" jsonschema:"the translation memories the profile bundles"`
}

type UpdateTMProfileIn struct {
	TMProfileID           int64    `json:"tm_profile_id" jsonschema:"the TM profile, from openloc_list_tm_profiles"`
	Name                  *string  `json:"name,omitempty" jsonschema:"new name; omit to keep"`
	Description           *string  `json:"description,omitempty" jsonschema:"new description; omit to keep, send \"\" to clear"`
	FuzzyThreshold        *float32 `json:"fuzzy_threshold,omitempty" jsonschema:"new lowest match score, 0.0-1.0; omit to keep"`
	SublanguagePenaltyPct *int32   `json:"sublanguage_penalty_pct,omitempty" jsonschema:"new sublanguage penalty, 0-100; omit to keep"`
	NearExactPenaltyPct   *int32   `json:"near_exact_penalty_pct,omitempty" jsonschema:"new near-exact penalty, 0-100; omit to keep"`
	// A pointer so "omit" (keep) and [] (remove every TM) stay distinct,
	// also in the confirm token's argument hash.
	TMs *[]TMProfileTMIn `json:"tms,omitempty" jsonschema:"the complete new TM list, replacing the current one; omit to keep the TMs"`
	Confirm
}

type DeleteTMProfileIn struct {
	TMProfileID int64 `json:"tm_profile_id" jsonschema:"the TM profile to delete, from openloc_list_tm_profiles"`
	Confirm
}

// --- Term bases -------------------------------------------------------------

type ListTermBasesIn struct{}

type TermBaseSummary struct {
	TermBaseID     int64  `json:"term_base_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	SourceLanguage string `json:"source_language"`
	// Languages are every language its terms use, source included.
	Languages    []string `json:"languages"`
	ConceptCount int64    `json:"concept_count"`
	CreatedBy    string   `json:"created_by,omitempty"`
}

type TermBaseList struct {
	TermBases []TermBaseSummary `json:"term_bases"`
	Count     int               `json:"count"`
}

type GetTermBaseIn struct {
	TermBaseID int64 `json:"term_base_id" jsonschema:"the term base, from openloc_list_term_bases"`
}

type TermBaseDetail struct {
	TermBaseSummary
	// Projects and Templates count where the base is assigned.
	Projects  int32 `json:"projects"`
	Templates int32 `json:"templates"`
}

type ListConceptsIn struct {
	TermBaseID int64    `json:"term_base_id" jsonschema:"the term base, from openloc_list_term_bases"`
	Query      string   `json:"query,omitempty" jsonschema:"optional substring matched against term text, definition and domain"`
	Languages  []string `json:"languages,omitempty" jsonschema:"optional BCP-47 languages; keeps concepts with a term in any of them and shows only those languages' terms"`
	Cursor     int64    `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; omit for the first page"`
	Limit      int      `json:"limit,omitempty" jsonschema:"concepts per page (default 50, max 200)"`
}

type ConceptTermDetail struct {
	TermID       int64  `json:"term_id"`
	Language     string `json:"language"`
	Text         string `json:"text"`
	Status       string `json:"status"`
	PartOfSpeech string `json:"part_of_speech"`
	Notes        string `json:"notes"`
}

type ConceptDetail struct {
	ConceptID  int64               `json:"concept_id"`
	Definition string              `json:"definition"`
	Domain     string              `json:"domain"`
	Terms      []ConceptTermDetail `json:"terms"`
}

type ConceptPage struct {
	Concepts   []ConceptDetail `json:"concepts"`
	NextCursor int64           `json:"next_cursor"`
	HasMore    bool            `json:"has_more"`
	Total      int64           `json:"total"`
}

type GetConceptIn struct {
	TermBaseID int64 `json:"term_base_id" jsonschema:"the term base, from openloc_list_term_bases"`
	ConceptID  int64 `json:"concept_id" jsonschema:"the concept, from openloc_list_concepts or openloc_lookup_concept"`
}

type RenameTermBaseIn struct {
	TermBaseID  int64   `json:"term_base_id" jsonschema:"the term base, from openloc_list_term_bases"`
	Name        string  `json:"name" jsonschema:"the new name"`
	Description *string `json:"description,omitempty" jsonschema:"new description; omit to keep, send \"\" to clear"`
}

type ImportTermsIn struct {
	TermBaseID int64 `json:"term_base_id" jsonschema:"the term base to import into, from openloc_list_term_bases"`
	UploadRef
	Format         string `json:"format,omitempty" jsonschema:"csv or tbx; omit to decide from the file name (.csv, .tbx, .xml)"`
	SourceLanguage string `json:"source_language,omitempty" jsonschema:"CSV only: source language for rows without a source_language column (default: the base's source language)"`
	TargetLanguage string `json:"target_language,omitempty" jsonschema:"CSV only: target language for rows without a target_language column"`
}

type ImportTermsResult struct {
	TermBaseID int64  `json:"term_base_id"`
	Format     string `json:"format"`
	// Imported counts concepts; Terms (TBX) counts the terms inside them.
	Imported int `json:"imported"`
	Terms    int `json:"terms"`
	Skipped  int `json:"skipped"`
}

type ExportTermsIn struct {
	TermBaseID      int64    `json:"term_base_id" jsonschema:"the term base, from openloc_list_term_bases"`
	Format          string   `json:"format,omitempty" jsonschema:"tbx (default: every language, synonym and status) or csv (bilingual rows)"`
	TargetLanguages []string `json:"target_languages,omitempty" jsonschema:"CSV only: target languages to include; omit for all"`
}

type DeleteTermBaseIn struct {
	TermBaseID int64 `json:"term_base_id" jsonschema:"the term base to delete, from openloc_list_term_bases"`
	Confirm
}

type DeleteConceptIn struct {
	TermBaseID int64 `json:"term_base_id" jsonschema:"the term base the concept belongs to"`
	ConceptID  int64 `json:"concept_id" jsonschema:"the concept to delete, from openloc_list_concepts or openloc_lookup_concept"`
	Confirm
}

type DeleteTermIn struct {
	TermBaseID int64 `json:"term_base_id" jsonschema:"the term base the term belongs to"`
	ConceptID  int64 `json:"concept_id" jsonschema:"the concept the term belongs to"`
	TermID     int64 `json:"term_id" jsonschema:"the term to delete, from openloc_get_concept or openloc_list_concepts"`
	Confirm
}
