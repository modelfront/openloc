package demodata

import (
	"fmt"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/strdist"
	"openloc/internal/terminology"
	"openloc/internal/wordcount"
)

func (b *builder) tm(name string, targets []string, customerID int64, entries []tmEntry) (int64, error) {
	src := "en"
	tm, err := b.q.CreateTMCollection(b.ctx, db.CreateTMCollectionParams{
		Name: name, TenantID: b.tenantID, SourceLanguage: &src, TargetLanguages: targets,
		CustomerID: &customerID, CreatedBy: auth.UserIDPtr(b.ownerID),
	})
	if err != nil {
		return 0, fmt.Errorf("tm %q: %w", name, err)
	}
	if err := b.record(ItemTM, tm.ID); err != nil {
		return 0, err
	}
	for _, e := range entries {
		for _, t := range []struct{ lang, text string }{{"de", e.de}, {"fr", e.fr}} {
			if t.text == "" {
				continue
			}
			if _, err := b.q.InsertTM(b.ctx, db.InsertTMParams{
				TmID: tm.ID, SourceLanguage: "en", TargetLanguage: t.lang,
				SourceText: e.src, TargetText: t.text, CreatedBy: b.author,
			}); err != nil {
				return 0, fmt.Errorf("tm entry %q: %w", e.src, err)
			}
		}
	}
	return tm.ID, nil
}

func (b *builder) termBase() (db.TermBasis, error) {
	base, err := b.q.CreateTermBase(b.ctx, db.CreateTermBaseParams{
		TenantID: b.tenantID, Name: termBase, Description: "Approved wording for Nordwind Travel copy.",
		SourceLanguage: "en", CreatedBy: auth.UserIDPtr(b.ownerID),
	})
	if err != nil {
		return db.TermBasis{}, fmt.Errorf("term base: %w", err)
	}
	if err := b.record(ItemTermBase, base.ID); err != nil {
		return db.TermBasis{}, err
	}
	for _, t := range brandTerms {
		ci := terminology.ConceptInput{Definition: t.definition, Domain: "Marketing", Terms: []terminology.ConceptTermInput{
			{Language: "en", Text: t.en, Status: "approved"},
			{Language: "de", Text: t.de, Status: t.deStatus},
			{Language: "fr", Text: t.fr, Status: t.deStatus},
		}}
		if _, err := terminology.CreateConcept(b.ctx, b.q, base, ci, auth.UserIDPtr(b.ownerID)); err != nil {
			return db.TermBasis{}, fmt.Errorf("term %q: %w", t.en, err)
		}
	}
	if err := b.q.SyncTermBaseTargetLanguages(b.ctx, base.ID); err != nil {
		return db.TermBasis{}, fmt.Errorf("term base languages: %w", err)
	}
	return base, nil
}

func (b *builder) qaProfile() (int64, error) {
	p, err := b.q.CreateQAProfile(b.ctx, db.CreateQAProfileParams{TenantID: b.tenantID, Name: qaProfile, Description: "Checks for customer-facing marketing copy."})
	if err != nil {
		return 0, fmt.Errorf("qa profile: %w", err)
	}
	if err := b.record(ItemQAProfile, p.ID); err != nil {
		return 0, err
	}
	rules := []struct{ check, severity string }{
		{"empty_target", "error"}, {"forbidden_terms", "error"}, {"tag_checks", "error"},
		{"number_consistency", "error"}, {"terminology", "warning"}, {"punctuation", "warning"}, {"consistency", "warning"},
	}
	for _, r := range rules {
		if _, err := b.q.UpsertQARule(b.ctx, db.UpsertQARuleParams{ProfileID: p.ID, CheckType: r.check, Enabled: true, Severity: r.severity, Config: []byte("{}")}); err != nil {
			return 0, fmt.Errorf("qa rule %s: %w", r.check, err)
		}
	}
	return p.ID, nil
}

// file inserts a file, its segments and one translation row per target
// language (empty rows for untranslated segments), then one job per language.
// Returns job ids by language.
func (b *builder) file(p db.Project, name string, format db.FileFormat, rows []row, targets []string) (map[string]int64, error) {
	f, err := b.q.CreateFile(b.ctx, db.CreateFileParams{ProjectID: p.ID, Name: name, Format: format, StoragePath: fmt.Sprintf("seed-docs/%d/%s", p.ID, name), SegmentCount: int32(len(rows))})
	if err != nil {
		return nil, fmt.Errorf("file %q: %w", name, err)
	}
	words := 0
	for pos, r := range rows {
		wc := wordcount.CountWords(r.src)
		words += wc
		var segID int64
		if err := b.tx.QueryRow(b.ctx,
			`INSERT INTO segments (file_id, source_text, segment_key, position, word_count) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			f.ID, r.src, fmt.Sprintf("%d", pos+1), pos, wc).Scan(&segID); err != nil {
			return nil, fmt.Errorf("segment: %w", err)
		}
		for _, lang := range targets {
			t := r.de
			if lang == "fr" {
				t = r.fr
			}
			if err := b.translation(segID, lang, t); err != nil {
				return nil, err
			}
		}
	}
	jobs := map[string]int64{}
	for _, lang := range targets {
		j, err := b.q.CreateJob(b.ctx, db.CreateJobParams{FileID: f.ID, TargetLanguage: lang, WordCount: int32(words)})
		if err != nil {
			return nil, fmt.Errorf("job: %w", err)
		}
		// jobs.status is the PIPELINE state: 'in_progress' means pre-translation
		// is still running and the editor shows "Processing…". Demo jobs are
		// past that — people are working on them — so 'needs_attention'.
		if err := b.exec(`UPDATE jobs SET status = 'needs_attention' WHERE id = $1`, j.ID); err != nil {
			return nil, err
		}
		jobs[lang] = j.ID
	}
	return jobs, nil
}

func (b *builder) translation(segID int64, lang string, t tr) error {
	status := t.status
	if status == "" {
		status = "needs_review"
	}
	var origin *string
	if t.origin != "" {
		origin = &t.origin
	}
	var pct *int32
	if t.percent > 0 {
		pct = &t.percent
	}
	var editedBy *int64
	if t.human {
		editor := b.editor(lang)
		editedBy = &editor
	}
	// An untranslated segment is NULL, as an uploaded one is: pre-translation
	// picks rows by active_text IS NULL.
	var text *string
	if t.text != "" {
		text = &t.text
	}
	var tid int64
	if err := b.tx.QueryRow(b.ctx,
		`INSERT INTO translations (segment_id, target_language, status, active_text, origin, tm_match_percent, edited_by)
	         VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		segID, lang, status, text, origin, pct, editedBy).Scan(&tid); err != nil {
		return fmt.Errorf("translation: %w", err)
	}
	return b.history(tid, lang, t)
}

// history writes the events that produced this translation, so the editor's
// History panel shows a real lineage instead of "No recorded changes yet".
//
// Two rules of the product shape what appears here, and breaking either would
// make the panel teach the wrong thing:
//   - A draft writes no history. A segment left at `needs_review` by a person
//     keeps only its machine row.
//   - A confirm that changed nothing is an `approved` row carrying no text;
//     only a confirm that rewrote the machine output is a `human` row with a
//     distance. Text on a status row would become the next edit-distance
//     baseline and flatten every later Δ to zero.
func (b *builder) history(tid int64, lang string, t tr) error {
	if t.text == "" {
		return nil
	}
	var ids []int64
	add := func(p db.InsertTranslationHistoryParams) error {
		p.TranslationID = tid
		if p.Details == nil {
			p.Details = []byte("{}")
		}
		row, err := b.q.InsertTranslationHistory(b.ctx, p)
		if err != nil {
			return fmt.Errorf("history (%s): %w", p.Kind, err)
		}
		ids = append(ids, row.ID)
		return nil
	}

	// The machine's version, and what the panel measures a human edit against.
	machine := ""
	var events []db.InsertTranslationHistoryParams
	switch t.origin {
	case "tm":
		machine = t.text
		pct := int16(t.percent)
		events = append(events, db.InsertTranslationHistoryParams{Kind: "tm", Text: &machine, TmID: b.tmID,
			TmMatchPercent: &pct, Details: []byte(`{"source":"pipeline"}`)})
	case "mt":
		machine = t.text
		events = append(events, db.InsertTranslationHistoryParams{Kind: "mt", Text: &machine, MtEngineID: b.mtEngineID})
	case "ape":
		machine = t.text
		events = append(events,
			db.InsertTranslationHistoryParams{Kind: "mt", Text: &t.mtDraft, MtEngineID: b.mtEngineID},
			// The rewrite was approved, so this one row carries the approval — the
			// panel puts an Auto-approved chip beside APE rather than adding a row.
			db.InsertTranslationHistoryParams{Kind: "ape", Text: &machine, MtEngineID: b.mtEngineID,
				Details: []byte(`{"predict_outcome":"ape_approved"}`)})
	}

	editor := b.editor(lang)
	if t.human && t.status == "approved" {
		editMs := int64(24_000)
		if machine == t.text {
			// Read it, agreed with it, pressed confirm.
			events = append(events, db.InsertTranslationHistoryParams{Kind: "approved",
				CreatedBy: &editor, EditingTimeMs: &editMs})
		} else {
			dist := int32(strdist.Levenshtein(machine, t.text))
			events = append(events, db.InsertTranslationHistoryParams{Kind: "human", Text: &t.text,
				CreatedBy: &editor, EditingTimeMs: &editMs, EditDistance: &dist})
		}
	}
	for _, e := range events {
		if err := add(e); err != nil {
			return err
		}
	}

	// Backdated oldest-first in insertion order, so the chain reads the way it
	// happened: the machine row above the confirm that followed it.
	start := b.historyStart()
	for i, id := range ids {
		if err := b.exec(
			`UPDATE translation_history SET created_at = $1::timestamptz + make_interval(hours => $2) WHERE id = $3`,
			start, i*3, id); err != nil {
			return err
		}
	}
	return nil
}

// historyStart is when a translation's history begins: two days ago, or in
// organization mode one of the last 84 days, so the dashboards' 30- and 90-day
// charts have a point on most days. 5 and 84 are coprime, so consecutive
// translations land on distinct days.
func (b *builder) historyStart() time.Time {
	b.histSeq++
	if b.org == nil {
		return b.daysAgo(2)
	}
	return b.daysAgo(1 + (b.histSeq*5)%84)
}

// editor is who confirmed a human translation: the owner, or in organization
// mode the linguist for that language.
func (b *builder) editor(lang string) int64 {
	if b.org == nil {
		return b.ownerID
	}
	if lang == "fr" {
		return b.frLinguist(b.linguistID)
	}
	return b.linguistID
}

// assignment puts a job on step `current` (earlier steps completed, later ones
// pending) with a deadline `days` from now.
type assignment struct {
	jobID    int64
	steps    []db.WorkflowStep
	current  int
	assignee int64
	days     int
	status   string
}

func (b *builder) assignAll(as ...assignment) error {
	for _, a := range as {
		if err := b.assign(a); err != nil {
			return err
		}
	}
	return nil
}

// assign writes one job's step assignments. jobs.current_step_id references
// the job's step ASSIGNMENT row, not the workflow step definition.
func (b *builder) assign(a assignment) error {
	var currentAssignment int64
	for i, st := range a.steps {
		stStatus := "pending"
		switch {
		case i < a.current:
			stStatus = "completed"
		case i == a.current:
			stStatus = a.status
		}
		who := a.assignee
		if st.DefaultAssignee != nil && i != a.current {
			who = *st.DefaultAssignee
		}
		row, err := b.q.CreateJobStepAssignment(b.ctx, db.CreateJobStepAssignmentParams{JobID: a.jobID, WorkflowStepID: st.ID, AssignedTo: &who, Status: stStatus, Deadline: b.deadline(a.days + i*2)})
		if err != nil {
			return fmt.Errorf("assignment: %w", err)
		}
		if i == a.current {
			currentAssignment = row.ID
		}
	}
	if err := b.q.SetJobCurrentStep(b.ctx, db.SetJobCurrentStepParams{ID: a.jobID, CurrentStepID: &currentAssignment}); err != nil {
		return fmt.Errorf("current step: %w", err)
	}
	return nil
}

// runQA runs the real checker over a job and stores what it finds, the same way
// the editor's "Run QA" button does. Findings are computed, never inserted by
// hand, so re-running QA in the app leaves the demo picture unchanged. The
// service runs on the transaction's querier, so it sees the uncommitted rows.
func (b *builder) runQA(jobID int64) error {
	if _, err := qa.NewService(b.q).RunAndPersistJob(b.ctx, jobID, b.tenantID); err != nil {
		return fmt.Errorf("qa run on job %d: %w", jobID, err)
	}
	return nil
}

func (b *builder) comment(jobID int64, source, lang string, author int64, text string) error {
	var translationID int64
	if err := b.tx.QueryRow(b.ctx, `
		SELECT t.id FROM translations t
		JOIN segments sg ON sg.id = t.segment_id
		JOIN jobs j ON j.file_id = sg.file_id
		WHERE j.id = $1 AND sg.source_text = $2 AND t.target_language = $3`, jobID, source, lang).Scan(&translationID); err != nil {
		return fmt.Errorf("comment target: %w", err)
	}
	if _, err := b.q.CreateComment(b.ctx, db.CreateCommentParams{TranslationID: translationID, UserID: auth.UserIDPtr(author), Content: text}); err != nil {
		return fmt.Errorf("comment: %w", err)
	}
	return nil
}
