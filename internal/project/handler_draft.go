package project

// handler_draft.go — the New Project wizard's autosave surface.
// A draft is a real projects row with status='draft'
// that may still miss its name and languages; the wizard replays its whole
// form into PUT /draft on every change and POST /publish turns it into an
// in_progress project.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/ptr"
	"openloc/internal/settings"
)

// FileMaterializer creates the translations, jobs and pipeline work for files
// that were uploaded while the project was a draft. Implemented by
// file.Handler and injected from api.go — internal/file imports this package,
// so the dependency cannot point the other way.
type FileMaterializer interface {
	MaterializeProjectFiles(ctx context.Context, tenantID, projectID int64, analyze bool) error
}

// SetFileMaterializer wires the publish step that turns a draft's files into
// jobs. nil publishes without touching files (tests without storage).
func (h *Handler) SetFileMaterializer(m FileMaterializer) { h.files = m }

// HandleSaveDraft — PUT /api/projects/{projectID}/draft. Manager surface
// (same route group as POST /). Replaces the draft's metadata and
// configuration with the wizard's current form; the body is the create body.
// The row stays a draft whatever `status` says — publish is the only way out.
// 404 when the project is not this tenant's draft. No audit event: the
// draft's project.created is filled in with the final form on publish.
func (h *Handler) HandleSaveDraft(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()

	req, params, cfg, ok := h.decodeCreateRequest(w, r)
	if !ok {
		return
	}
	fail := func(err error, verb, resource string) {
		writeCreateError(ctx, w, err, verb, resource, "project-draft-save-failed", logging.ProjectID(id))
	}
	meta, err := validateMetadata(ctx, h.queries, tenantID, params, true)
	if err != nil {
		fail(err, "validate", "project")
		return
	}
	if err := settings.ValidateSetup(ctx, h.queries, tenantID, cfg); err != nil {
		fail(err, "validate", "project setup")
		return
	}
	templateID := req.templateID()
	if templateID != nil {
		_, err := h.queries.GetTemplate(ctx, db.GetTemplateParams{ID: *templateID, TenantID: tenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			err = validationf("template not found in this tenant")
		} else if err != nil {
			err = fmt.Errorf("get template: %w", err)
		}
		if err != nil {
			fail(err, "verify", "template")
			return
		}
	}

	project, err := saveDraft(ctx, h.pool, tenantID, id, meta, templateID, cfg)
	if errors.Is(err, errDraftNotFound) {
		httputil.NotFound(w, "project")
		return
	}
	if err != nil {
		fail(err, "update", "project draft")
		return
	}
	httputil.Data(w, project)
}

// errDraftNotFound means the id is not a draft of the caller's tenant.
var errDraftNotFound = errors.New("draft not found")

// saveDraft writes the wizard's form onto the draft in one transaction: a
// setup refused at any step leaves the draft and its settings as they were.
func saveDraft(ctx context.Context, pool database.TxBeginner, tenantID, id int64, meta projectMetadata, templateID *int64, cfg SetupConfig) (db.Project, error) {
	var project db.Project
	err := database.InTx(ctx, pool, func(tx pgx.Tx, q db.Querier) error {
		var err error
		project, err = q.UpdateProjectDraft(ctx, db.UpdateProjectDraftParams{
			ID:              id,
			TenantID:        tenantID,
			Name:            meta.Name,
			SourceLanguage:  meta.SourceLanguage,
			TargetLanguages: meta.TargetLanguages,
			CustomerID:      meta.CustomerID,
			Deadline:        meta.Deadline,
			Client:          meta.Client,
			Domain:          meta.Domain,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errDraftNotFound
		}
		if err != nil {
			return fmt.Errorf("update draft: %w", err)
		}

		// Template provenance is stamped, never applied: the wizard resolved the
		// template into the settings this body carries.
		if !ptr.Equal(templateID, project.TemplateID) {
			if project, err = q.SetProjectTemplate(ctx, db.SetProjectTemplateParams{
				ID: project.ID, TenantID: tenantID, TemplateID: templateID,
			}); err != nil {
				return fmt.Errorf("stamp template: %w", err)
			}
		}

		// A form that stopped following a TM profile sends its own TM rows; the
		// stored link has to go first or the replace is refused as "linked".
		if cfg.TMProfileID == nil {
			profile, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
			if err != nil {
				return fmt.Errorf("get settings profile: %w", err)
			}
			if profile.TmProfileID != nil {
				if _, err := settings.DetachTMProfile(ctx, q, project.SettingsProfileID); err != nil {
					return fmt.Errorf("detach TM profile: %w", err)
				}
			}
		}

		// A draft records no project.created event, so only the settings are
		// applied, nested in this transaction.
		return settings.ApplySetup(ctx, tx, q, tenantID, project.SettingsProfileID, cfg)
	})
	if err != nil {
		return db.Project{}, err
	}
	return project, nil
}

// HandlePublish — POST /api/projects/{projectID}/publish. Manager surface.
// Body: {"analyze": bool} — whether the materialized uploads request an
// automatic analysis (the wizard's "Run analysis on upload" checkbox). Rules
// in Publish.
func (h *Handler) HandlePublish(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req struct {
		Analyze bool `json:"analyze"`
	}
	if r.ContentLength != 0 {
		if err := httputil.Decode(r, &req); err != nil {
			httputil.BadRequest(w, "invalid request body")
			return
		}
	}
	ctx := r.Context()
	published, err := h.Publish(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), id, req.Analyze)
	if err != nil {
		switch {
		case errors.Is(err, ErrPublishFiles):
			httputil.Error(w, http.StatusInternalServerError, ErrPublishFiles.Error())
		case errors.Is(err, ErrValidation):
			writeCreateError(ctx, w, err, "validate", "project", "project-publish-failed")
		default:
			WriteServiceError(w, err, "publish", "project")
		}
		return
	}
	httputil.Data(w, published)
}
