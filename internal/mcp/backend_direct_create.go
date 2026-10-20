package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/project"
	"openloc/internal/ptr"
	"openloc/internal/terminology"
)

// Creation tools. All additive. create_project delegates to the
// SAME service the REST handler uses (project.Create / project.CreateWithTemplate,
// extracted for exactly this) — validation and creation sequence cannot drift
// between the two surfaces.

func (b *directBackend) CreateProject(ctx context.Context, in CreateProjectIn) (CreateProjectResult, error) {
	tenantID, userID := scope(ctx)

	// The customer is the project's Customer label pick, as on the app's New
	// project form, so it is checked before anything is created.
	if in.CustomerID != 0 {
		if err := b.checkCustomerValue(ctx, tenantID, in.CustomerID); err != nil {
			return CreateProjectResult{}, err
		}
	}
	params := project.CreateParams{
		Name:            in.Name,
		Description:     in.Description,
		SourceLanguage:  in.SourceLanguage,
		TargetLanguages: in.TargetLanguages,
		Deadline:        in.Deadline,
	}
	if in.CustomerID != 0 {
		// Written in the create's transaction: a failed pick leaves no
		// project, so a retry makes no duplicate.
		params.LabelValueIDs = []int64{in.CustomerID}
	}

	if in.TemplateID != 0 || in.WorkflowID != 0 {
		// Template and workflow writes need a transaction; refuse up front
		// rather than creating a project they can never reach.
		if err := b.needPool("creating a project with a template or workflow"); err != nil {
			return CreateProjectResult{}, err
		}
	}

	var p db.Project
	var err error
	switch {
	case in.WorkflowID != 0:
		// Same setup path as the REST create: template first, then the
		// workflow's steps on top.
		var templateID *int64
		if in.TemplateID != 0 {
			templateID = &in.TemplateID
		}
		p, err = project.CreateWithSetup(ctx, b.pool, b.q, tenantID, userID, params, templateID, project.SetupConfig{WorkflowID: &in.WorkflowID})
	case in.TemplateID != 0:
		p, err = project.CreateWithTemplate(ctx, b.pool, b.q, tenantID, userID, params, in.TemplateID)
	default:
		p, err = project.Create(ctx, b.pool, b.q, tenantID, userID, params)
	}
	if err != nil {
		if errors.Is(err, project.ErrValidation) {
			return CreateProjectResult{}, invalidf("%s", trimValidation(err))
		}
		if msg, ok := httputil.AsClientError(err); ok {
			return CreateProjectResult{}, invalidf("%s", msg)
		}
		return CreateProjectResult{}, fmt.Errorf("create project: %w", err)
	}

	res := CreateProjectResult{
		ProjectID:       p.ID,
		Name:            p.Name,
		SourceLanguage:  p.SourceLanguage,
		TargetLanguages: p.TargetLanguages,
		TemplateApplied: in.TemplateID != 0,
		WorkflowApplied: in.WorkflowID != 0,
		Note:            "Project created. Add a document the user attached in the chat with openloc_attach_uploaded_file, a text file you can read with openloc_upload_text_file, or open the project for them with navigate_to {page: \"project\"} to upload there.",
	}
	return res, nil
}

// checkCustomerValue refuses an id that is not a live value of the tenant's
// Customer label.
func (b *directBackend) checkCustomerValue(ctx context.Context, tenantID, valueID int64) error {
	refused := invalidf("customer_id %d is not a value of the Customer label; list the values with openloc_list_labels", valueID)
	v, err := b.q.GetLabelValue(ctx, db.GetLabelValueParams{ID: valueID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return refused
	}
	if err != nil {
		return fmt.Errorf("get customer value: %w", err)
	}
	d, err := b.q.GetLabelDefinition(ctx, db.GetLabelDefinitionParams{ID: v.LabelDefinitionID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return refused
	}
	if err != nil {
		return fmt.Errorf("get customer label: %w", err)
	}
	if ptr.Deref(d.SystemKey) != "customer" {
		return refused
	}
	return nil
}

func (b *directBackend) CreateCustomer(ctx context.Context, in CreateCustomerIn) (CreateCustomerResult, error) {
	tenantID, _ := scope(ctx)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return CreateCustomerResult{}, invalidf("name is required")
	}
	if len(name) > 200 {
		return CreateCustomerResult{}, invalidf("name too long (max 200 characters)")
	}
	c, err := b.q.CreateCustomer(ctx, db.CreateCustomerParams{TenantID: tenantID, Name: name})
	if err != nil {
		return CreateCustomerResult{}, fmt.Errorf("create customer: %w", err)
	}
	return CreateCustomerResult{CustomerID: c.ID, Name: c.Name}, nil
}

func (b *directBackend) CreateTermBase(ctx context.Context, in CreateTermBaseIn) (CreateTermBaseResult, error) {
	tenantID, userID := scope(ctx)
	name := strings.TrimSpace(in.Name)
	src := langcode.Normalize(in.SourceLanguage)
	if name == "" || src == "" {
		return CreateTermBaseResult{}, invalidf("name and source_language are required")
	}
	// target_languages are derived from the base's terms, not set at creation.
	base, err := b.q.CreateTermBase(ctx, db.CreateTermBaseParams{
		TenantID:       tenantID,
		Name:           name,
		Description:    in.Description,
		SourceLanguage: src,
		// Attribution parity with the REST handler — an MCP-created base
		// must show its creator on the terminology list too.
		CreatedBy: auth.UserIDPtr(userID),
	})
	if err != nil {
		return CreateTermBaseResult{}, fmt.Errorf("create term base: %w", err)
	}
	return CreateTermBaseResult{
		TermBaseID:      base.ID,
		Name:            base.Name,
		SourceLanguage:  base.SourceLanguage,
		TargetLanguages: base.TargetLanguages,
	}, nil
}

var validTermStatuses = map[string]bool{"approved": true, "pending": true, "forbidden": true}

func (b *directBackend) AddTerm(ctx context.Context, in AddTermIn) (AddTermResult, error) {
	tenantID, userID := scope(ctx)

	// Ownership first: a foreign term_base_id yields the opaque not-found.
	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: in.TermBaseID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AddTermResult{}, errNotFound
		}
		return AddTermResult{}, fmt.Errorf("get term base: %w", err)
	}

	src := strings.TrimSpace(in.SourceTerm)
	tgt := strings.TrimSpace(in.TargetTerm)
	if src == "" || tgt == "" {
		return AddTermResult{}, invalidf("source_term and target_term are required")
	}
	tgtLang := langcode.Normalize(in.TargetLanguage)
	if tgtLang == "" {
		return AddTermResult{}, invalidf("target_language is required")
	}
	if tgtLang == base.SourceLanguage {
		return AddTermResult{}, invalidf("target_language must differ from the base's source language")
	}
	status := in.Status
	if status == "" {
		status = "approved"
	}
	if !validTermStatuses[status] {
		return AddTermResult{}, invalidf("status must be approved, pending, or forbidden")
	}

	// A model adding a term IS the authority on the target text — no MT enrichment.
	domain := strings.TrimSpace(in.Domain)
	tgtTerm := terminology.ConceptTermInput{
		Language: tgtLang, Text: tgt, Status: status, Notes: strings.TrimSpace(in.Notes),
	}

	// Find-or-create by concept identity (source-language term + domain). A second
	// target for the same source term is the SAME meaning, so merge into the
	// existing concept instead of spawning a duplicate; homographs stay distinct
	// because a differing domain is a different concept.
	existing, findErr := b.q.FindConceptBySourceTerm(ctx, db.FindConceptBySourceTermParams{
		TermBaseID: base.ID, Domain: domain, Language: base.SourceLanguage, Text: src,
	})
	var conceptID, termID int64
	switch {
	case findErr == nil:
		conceptID = existing.ID
		term, e := b.attachTerm(ctx, existing.ID, tgtTerm, userID)
		if e != nil {
			return AddTermResult{}, e
		}
		termID = term.ID
	case errors.Is(findErr, pgx.ErrNoRows):
		// New meaning: create the concept with the source term + the target term.
		ci := terminology.ConceptInput{
			Domain: domain,
			Terms: []terminology.ConceptTermInput{
				{Language: base.SourceLanguage, Text: src, Status: "approved"},
				tgtTerm,
			},
		}
		if conceptID, err = b.createConcept(ctx, base, ci, userID); err != nil {
			return AddTermResult{}, fmt.Errorf("create term: %w", err)
		}
		// Resolve the target term's id for the response (concept + both terms were
		// just written atomically). Best-effort: concept_id is the essential handle.
		if t, e := b.q.FindConceptTerm(ctx, db.FindConceptTermParams{
			ConceptID: conceptID, Language: tgtLang, Text: tgt,
		}); e == nil {
			termID = t.ID
		}
	default:
		return AddTermResult{}, fmt.Errorf("find concept: %w", findErr)
	}
	// Keep the base's derived target_languages in sync (best-effort).
	_ = b.q.SyncTermBaseTargetLanguages(ctx, base.ID)

	return AddTermResult{
		TermBaseID: base.ID,
		ConceptID:  conceptID,
		TermID:     termID,
		SourceTerm: src,
		TargetTerm: tgt,
		Status:     status,
	}, nil
}

// attachTerm adds a term to an existing concept idempotently: an identical live
// (language, text) term returns the existing row; otherwise it is created. Inputs
// must already be normalized/trimmed by the caller. Shared by add_term (concept
// reuse) and add_term_to_concept.
func (b *directBackend) attachTerm(ctx context.Context, conceptID int64, t terminology.ConceptTermInput, userID int64) (db.Term, error) {
	if existing, err := b.q.FindConceptTerm(ctx, db.FindConceptTermParams{
		ConceptID: conceptID, Language: t.Language, Text: t.Text,
	}); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Term{}, fmt.Errorf("find term: %w", err)
	}
	created, err := b.q.CreateTerm(ctx, db.CreateTermParams{
		ConceptID: conceptID, Language: t.Language, Text: t.Text, Status: t.Status,
		PartOfSpeech: t.PartOfSpeech, Notes: t.Notes, CreatedBy: &userID, UpdatedBy: &userID,
	})
	if err != nil {
		return db.Term{}, fmt.Errorf("create term: %w", err)
	}
	return created, nil
}

// createConcept inserts a concept + terms via terminology.CreateConcept, wrapping
// it in a transaction when a pool is available (else running directly on b.q), and
// returns the new concept's id. Shared by add_term, create_concept.
func (b *directBackend) createConcept(ctx context.Context, base db.TermBasis, ci terminology.ConceptInput, userID int64) (int64, error) {
	if b.pool != nil {
		tx, err := b.pool.Begin(ctx)
		if err != nil {
			return 0, fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(ctx)
		id, err := terminology.CreateConcept(ctx, db.New(tx), base, ci, &userID)
		if err != nil {
			return 0, err
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit: %w", err)
		}
		return id, nil
	}
	return terminology.CreateConcept(ctx, b.q, base, ci, &userID)
}

// CreateConcept adds a full multilingual concept (definition/domain + one or more
// terms across languages) to a term base. Status is required and validated per
// term — a model authoring terminology must state approved/pending/forbidden
// explicitly rather than fall through to a silent default.
func (b *directBackend) CreateConcept(ctx context.Context, in CreateConceptIn) (CreateConceptResult, error) {
	tenantID, userID := scope(ctx)

	// Ownership first: a foreign term_base_id yields the opaque not-found.
	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: in.TermBaseID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateConceptResult{}, errNotFound
		}
		return CreateConceptResult{}, fmt.Errorf("get term base: %w", err)
	}

	if len(in.Terms) == 0 {
		return CreateConceptResult{}, invalidf("at least one term is required")
	}
	ci := terminology.ConceptInput{
		Definition: strings.TrimSpace(in.Definition),
		Domain:     strings.TrimSpace(in.Domain),
	}
	for i, t := range in.Terms {
		lang := langcode.Normalize(t.Language)
		text := strings.TrimSpace(t.Text)
		if lang == "" || text == "" {
			return CreateConceptResult{}, invalidf("term %d: language and text are required", i+1)
		}
		if !validTermStatuses[t.Status] {
			return CreateConceptResult{}, invalidf("term %d (%q): status must be approved, pending, or forbidden", i+1, text)
		}
		ci.Terms = append(ci.Terms, terminology.ConceptTermInput{
			Language: lang, Text: text, Status: t.Status,
			PartOfSpeech: strings.TrimSpace(t.PartOfSpeech), Notes: strings.TrimSpace(t.Notes),
		})
	}

	conceptID, err := b.createConcept(ctx, base, ci, userID)
	if err != nil {
		return CreateConceptResult{}, fmt.Errorf("create concept: %w", err)
	}
	// Keep the base's derived target_languages in sync (best-effort).
	_ = b.q.SyncTermBaseTargetLanguages(ctx, base.ID)

	return CreateConceptResult{
		TermBaseID: base.ID,
		ConceptID:  conceptID,
		Terms:      len(ci.Terms),
	}, nil
}

// AddTermToConcept appends one term (in one language) to an existing concept.
// Idempotent: re-adding the same (language, text) returns the existing term.
// Status is required and validated (no silent default).
func (b *directBackend) AddTermToConcept(ctx context.Context, in AddTermToConceptIn) (AddTermToConceptResult, error) {
	tenantID, userID := scope(ctx)

	// Ownership: the base, then the concept must belong to it. A foreign base or
	// concept yields the opaque not-found.
	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: in.TermBaseID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AddTermToConceptResult{}, errNotFound
		}
		return AddTermToConceptResult{}, fmt.Errorf("get term base: %w", err)
	}
	if _, err := b.q.GetConcept(ctx, db.GetConceptParams{ID: in.ConceptID, TermBaseID: base.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AddTermToConceptResult{}, errNotFound
		}
		return AddTermToConceptResult{}, fmt.Errorf("get concept: %w", err)
	}

	lang := langcode.Normalize(in.Language)
	text := strings.TrimSpace(in.Text)
	if lang == "" || text == "" {
		return AddTermToConceptResult{}, invalidf("language and text are required")
	}
	if !validTermStatuses[in.Status] {
		return AddTermToConceptResult{}, invalidf("status must be approved, pending, or forbidden")
	}

	// Idempotent: an identical live (language, text) term returns the existing one.
	term, err := b.attachTerm(ctx, in.ConceptID, terminology.ConceptTermInput{
		Language: lang, Text: text, Status: in.Status,
		PartOfSpeech: strings.TrimSpace(in.PartOfSpeech), Notes: strings.TrimSpace(in.Notes),
	}, userID)
	if err != nil {
		return AddTermToConceptResult{}, err
	}
	// Keep the base's derived target_languages in sync (best-effort).
	_ = b.q.SyncTermBaseTargetLanguages(ctx, base.ID)

	return AddTermToConceptResult{
		TermBaseID: base.ID, ConceptID: in.ConceptID, TermID: term.ID,
		Language: term.Language, Text: term.Text, Status: term.Status,
	}, nil
}

// UpdateConcept edits an existing concept's definition and/or domain. Only the
// fields the caller provides change (nil = leave unchanged), so a model can fix a
// definition without clobbering the domain. Term text/status are edited via the
// term tools, not here.
func (b *directBackend) UpdateConcept(ctx context.Context, in UpdateConceptIn) (UpdateConceptResult, error) {
	tenantID, userID := scope(ctx)

	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: in.TermBaseID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpdateConceptResult{}, errNotFound
		}
		return UpdateConceptResult{}, fmt.Errorf("get term base: %w", err)
	}
	concept, err := b.q.GetConcept(ctx, db.GetConceptParams{ID: in.ConceptID, TermBaseID: base.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpdateConceptResult{}, errNotFound
		}
		return UpdateConceptResult{}, fmt.Errorf("get concept: %w", err)
	}
	if in.Definition == nil && in.Domain == nil {
		return UpdateConceptResult{}, invalidf("nothing to update: set definition and/or domain")
	}
	definition := concept.Definition
	if in.Definition != nil {
		definition = strings.TrimSpace(*in.Definition)
	}
	domain := concept.Domain
	if in.Domain != nil {
		domain = strings.TrimSpace(*in.Domain)
	}
	if err := b.q.UpdateTermConcept(ctx, db.UpdateTermConceptParams{
		ID: in.ConceptID, Definition: definition, Domain: domain, UpdatedBy: &userID,
	}); err != nil {
		return UpdateConceptResult{}, fmt.Errorf("update concept: %w", err)
	}
	return UpdateConceptResult{
		TermBaseID: base.ID, ConceptID: in.ConceptID, Definition: definition, Domain: domain,
	}, nil
}

// UpdateTerm edits one term's text/status/part-of-speech/notes. Only the provided
// fields change (nil = unchanged). Language is immutable here — mirrors the web
// editor, which updates text/status/POS/notes but never a term's language.
func (b *directBackend) UpdateTerm(ctx context.Context, in UpdateTermIn) (UpdateTermResult, error) {
	tenantID, userID := scope(ctx)

	// Ownership chain: base in tenant → concept in base → term in concept. Any
	// break yields the opaque not-found.
	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: in.TermBaseID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpdateTermResult{}, errNotFound
		}
		return UpdateTermResult{}, fmt.Errorf("get term base: %w", err)
	}
	if _, err := b.q.GetConcept(ctx, db.GetConceptParams{ID: in.ConceptID, TermBaseID: base.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpdateTermResult{}, errNotFound
		}
		return UpdateTermResult{}, fmt.Errorf("get concept: %w", err)
	}
	term, err := b.q.GetTerm(ctx, in.TermID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpdateTermResult{}, errNotFound
		}
		return UpdateTermResult{}, fmt.Errorf("get term: %w", err)
	}
	if term.ConceptID != in.ConceptID || term.DeletedAt.Valid {
		return UpdateTermResult{}, errNotFound
	}

	if in.Text == nil && in.Status == nil && in.PartOfSpeech == nil && in.Notes == nil {
		return UpdateTermResult{}, invalidf("nothing to update: set text, status, part_of_speech, and/or notes")
	}
	text := term.Text
	if in.Text != nil {
		text = strings.TrimSpace(*in.Text)
		if text == "" {
			return UpdateTermResult{}, invalidf("text cannot be empty")
		}
	}
	status := term.Status
	if in.Status != nil {
		if !validTermStatuses[*in.Status] {
			return UpdateTermResult{}, invalidf("status must be approved, pending, or forbidden")
		}
		status = *in.Status
	}
	pos := term.PartOfSpeech
	if in.PartOfSpeech != nil {
		pos = strings.TrimSpace(*in.PartOfSpeech)
	}
	notes := term.Notes
	if in.Notes != nil {
		notes = strings.TrimSpace(*in.Notes)
	}

	if err := b.q.UpdateTermFull(ctx, db.UpdateTermFullParams{
		ID: in.TermID, Text: text, Status: status, PartOfSpeech: pos, Notes: notes, UpdatedBy: &userID,
	}); err != nil {
		return UpdateTermResult{}, fmt.Errorf("update term: %w", err)
	}
	// No SyncTermBaseTargetLanguages: language is immutable here, so the base's
	// derived target languages cannot change.
	return UpdateTermResult{
		TermBaseID: base.ID, ConceptID: in.ConceptID, TermID: in.TermID,
		Language: term.Language, Text: text, Status: status, PartOfSpeech: pos, Notes: notes,
	}, nil
}

// trimValidation strips the ErrValidation sentinel prefix, leaving the
// authored user-facing message.
func trimValidation(err error) string {
	return strings.TrimPrefix(err.Error(), project.ErrValidation.Error()+": ")
}
