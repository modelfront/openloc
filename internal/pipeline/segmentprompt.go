package pipeline

import (
	"context"
	"encoding/json"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/segmentprompt"
	"openloc/internal/terminology"
	"openloc/internal/translationmemory"
)

// Building the per-segment user message. The system prompt names which optional
// context each segment carries; this file resolves those values for a batch and
// internal/segmentprompt does the pure JSON work.
//
// Resolution is FIELD-DRIVEN: only the context the prompt asked for is
// resolved, so a prompt without terms runs no glossary query and one
// without tm_match runs no TM lookup. That is what keeps a feature with several
// possible context sources cheap for the common two-field case.
//
// Shared by both LLM paths — QE+APE (aistep_llm.go) and AI-as-MT
// (resolveAIMTEngine) — so the lazy-fetch rules exist in exactly one place.

// segPromptRow is one segment to build a body for. TargetText is empty on the
// MT path (nothing is translated yet).
type segPromptRow struct {
	TranslationID int64
	// SourceText and TargetText are what the model reads, with inline tags
	// as chip labels.
	SourceText string
	TargetText string
	// PlainSource is the source without tags, the text the TM lookup matches
	// on — as the TM phase does, since TM entries hold no chip labels. Empty
	// means SourceText has no tags.
	PlainSource string
}

// segPromptRenderer builds a batch of per-segment bodies.
type segPromptRenderer struct {
	svc    *Service
	fields segmentprompt.Fields

	// Lookup inputs, populated only for the fields that need them.
	tmIDs                  []int64
	sourceLang, targetLang string
	tmCfg                  tmMatchConfig
	settingsProfileID      int64
	projectDomain          string
}

// newSegPromptRenderer resolves what an engine row's system prompt asks for.
// Never nil: every LLM row sends a segment body now, because the source text is
// implied rather than chosen.
func (s *Service) newSegPromptRenderer(
	ctx context.Context, tenantID int64, prompt db.AiPrompt, mode string,
	project db.Project, targetLang string,
) *segPromptRenderer {
	fields := segmentprompt.FieldsFor(mode, prompt.SegmentContext)
	r := &segPromptRenderer{
		svc:               s,
		fields:            fields,
		sourceLang:        project.SourceLanguage,
		targetLang:        targetLang,
		settingsProfileID: project.SettingsProfileID,
		projectDomain:     s.systemLabelValue(ctx, tenantID, project, "domain"),
	}
	if fields.Has(segmentprompt.KeyTMMatch) {
		var assignments []db.LookupSettingsTMsRow
		r.tmIDs, assignments = s.resolveAssignedTMs(ctx, project.SettingsProfileID, project.SourceLanguage, targetLang)
		r.tmCfg = s.resolveTMMatchConfig(ctx, project.SettingsProfileID, project.SourceLanguage, targetLang, assignments)
	}
	return r
}

// RenderBatch builds one body per row, index-aligned. Per-field lookups are
// batched across the whole slice (one query for every row's comments, one for
// every row's notes, one for every row's terms) — only tm_match is inherently
// per-row, since it is a similarity search on each source text.
//
// A row whose body resolved to nothing gets a nil entry, so the caller's
// built-in fallback applies instead of an empty `{}` reaching the model.
func (r *segPromptRenderer) RenderBatch(ctx context.Context, rows []segPromptRow) []json.RawMessage {
	if r == nil || len(rows) == 0 {
		return nil
	}
	matches := r.lookupTMMatches(ctx, rows)
	terms := r.fetchTerms(ctx, rows)

	out := make([]json.RawMessage, len(rows))
	for i, row := range rows {
		vals := segmentprompt.Values{
			SourceText: row.SourceText,
			TargetText: row.TargetText,
			Terms:      terms[row.TranslationID],
		}
		if m, ok := matches[i]; ok {
			vals.TMMatches = m
		}
		body := segmentprompt.Build(r.fields, vals)
		if segmentprompt.Blank(body) {
			continue
		}
		out[i] = body
	}
	return out
}

// fetchTerms batch-loads the glossary entries matching each row's source text.
//
// One query for the whole batch, not one per segment: every row's n-grams go in
// as a single token set, and the returned terms are mapped back by matching
// each row's own tokens. The alternative — the per-segment lookup the CAT
// sidebar and the QA checker use — would be llmBatchSize queries per request.
func (r *segPromptRenderer) fetchTerms(ctx context.Context, rows []segPromptRow) map[int64][]segmentprompt.Term {
	if !r.fields.Has(segmentprompt.KeyTerms) {
		return nil
	}
	// Per-row token sets, and their union for the query.
	rowTokens := make(map[int64]map[string]bool, len(rows))
	seen := make(map[string]bool)
	var all []string
	for _, row := range rows {
		set := make(map[string]bool)
		for _, tok := range terminology.TokenizeSourceText(row.SourceText, termMaxNGram) {
			set[tok] = true
			if !seen[tok] {
				seen[tok] = true
				all = append(all, tok)
			}
		}
		rowTokens[row.TranslationID] = set
	}
	if len(all) == 0 {
		return nil
	}

	found, err := r.svc.queries.LookupSettingsTermsByTokens(ctx, db.LookupSettingsTermsByTokensParams{
		SettingsProfileID: r.settingsProfileID,
		SourceLanguage:    r.sourceLang,
		TargetLanguage:    r.targetLang,
		Tokens:            all,
		ProjectDomain:     r.projectDomain,
	})
	if err != nil {
		logging.Warn(ctx, err, "could not load glossary terms — the terms field is omitted",
			"prompt.segment_terms_load_failed")
		return nil
	}

	out := make(map[int64][]segmentprompt.Term)
	for _, row := range rows {
		tokens := rowTokens[row.TranslationID]
		// A term matched the batch, not necessarily this row — the query was
		// asked for every row's tokens at once.
		var dedupe map[string]bool
		for _, t := range found {
			if !tokens[strings.ToLower(t.SourceTerm)] {
				continue
			}
			key := t.SourceTerm + "\x00" + t.TargetTerm
			if dedupe == nil {
				dedupe = make(map[string]bool)
			}
			if dedupe[key] {
				continue
			}
			dedupe[key] = true
			out[row.TranslationID] = append(out[row.TranslationID], segmentprompt.Term{
				Source: t.SourceTerm, Target: t.TargetTerm, Status: t.Status,
			})
		}
	}
	return out
}

// termMaxNGram is how many words a glossary term may span, matching the CAT
// editor's own lookup (terminology.HandleLookup) so the model sees the terms
// the translator sees.
const termMaxNGram = 4

// lookupTMMatches returns the best fuzzy TM match for each row, by row index,
// with its score after penalties, when the prompt asks for it; rows with no TM
// or no hit clearing the threshold are left out. The whole batch shares one
// lookup session.
//
// Callers send BOTH halves. The source is the one the model cannot
// otherwise see, and the delta between it and the current source is where a
// fuzzy-match defect lives — a TM source reading "Turn off the device"
// against a segment reading "Do NOT turn off the device" means the inherited
// translation is wrong in the most dangerous possible way, and nothing else
// in the prompt reveals it. The target comes along because the pair is what
// a TM match IS: without it the model cannot tell whether the translation it
// is judging came from this entry or from somewhere else.
func (r *segPromptRenderer) lookupTMMatches(ctx context.Context, rows []segPromptRow) map[int][]segmentprompt.TMMatch {
	if !r.fields.Has(segmentprompt.KeyTMMatch) || len(r.tmIDs) == 0 {
		return nil
	}
	out := make(map[int][]segmentprompt.TMMatch, len(rows))
	r.svc.withFuzzySession(ctx, func(lookups *fuzzySession) {
		for i, row := range rows {
			lookup := row.PlainSource
			if lookup == "" {
				lookup = row.SourceText
			}
			if hit, score, found := lookups.findBest(r.tmIDs, r.tmCfg, lookup); found {
				out[i] = []segmentprompt.TMMatch{{
					Source: hit.SourceText,
					Target: hit.TargetText,
					Score:  int(translationmemory.HitPercent(lookup, hit, score, nil)),
				}}
			}
		}
	})
	return out
}

func translationIDs(rows []segPromptRow) []int64 {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.TranslationID
	}
	return ids
}

// resolveTMMatchConfig reads the profile's TM threshold and penalties, so the
// tm_match field resolves "the TM suggestion" exactly the way the TM phase and
// the editor sidebar do. Falls back to the same 0.75 threshold the TM phase
// uses, and to the default penalties — dropping them on a failed read would
// over-report a penalised hit as a clean one.
func (s *Service) resolveTMMatchConfig(
	ctx context.Context, settingsProfileID int64, sourceLang, targetLang string,
	assignments []db.LookupSettingsTMsRow,
) tmMatchConfig {
	cfg := tmMatchConfig{
		sourceLang:     sourceLang,
		targetLang:     targetLang,
		fuzzyThreshold: translationmemory.DefaultFuzzyThreshold,
		penalties:      translationmemory.DefaultPenalties(),
	}
	profile, err := s.queries.GetTMScopeSettingsProfile(ctx, settingsProfileID)
	if err == nil {
		cfg.fuzzyThreshold = translationmemory.FuzzyThreshold(profile.FuzzyThreshold)
		cfg.penalties = translationmemory.PenaltiesFromProfile(profile)
	}
	cfg.penalties = cfg.penalties.WithTMs(assignments)
	return cfg
}
