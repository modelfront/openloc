// Package segmentationapi stores and lists segmentation profiles (the
// built-ins, which are shared rows with no tenant, and an org's own), picks
// the profile a project or template uses and the snapshot a new file is split
// with, and serves them over HTTP. The rules engine is internal/segmentation.
//
// The functions here are transport-free and shared by the REST handlers, the
// customer API and the MCP tools. A caller mistake comes back as
// httputil.ClientError, a refused change (a built-in, a stale save, a taken
// name, a profile templates use) as httputil.ConflictError, and an id outside the tenant as
// pgx.ErrNoRows.
package segmentationapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/segmentation"
)

// MaxNameLength is the longest profile name (the column's check).
const MaxNameLength = 100

// MaxSourceLanguages is the most source languages one profile can list.
const MaxSourceLanguages = 50

var errBuiltinReadOnly = httputil.NewConflictError("Built-in segmentation profiles are read-only. Copy it to make changes.")

// Service reads and writes segmentation profiles for one database.
type Service struct {
	q    db.Querier
	pool *pgxpool.Pool
}

// NewService returns a Service; pool runs the multi-step writes.
func NewService(q db.Querier, pool *pgxpool.Pool) *Service {
	return &Service{q: q, pool: pool}
}

// List returns the built-in profiles, then the org's, both ordered by
// language. lang, when set, keeps the profiles that fit it (Fits) and marks
// the one a new project in lang starts with as Suggested.
func (s *Service) List(ctx context.Context, tenantID int64, lang string) ([]Profile, error) {
	rows, err := s.q.ListSegmentationProfiles(ctx, &tenantID)
	if err != nil {
		return nil, err
	}
	lang = langcode.Normalize(lang)
	var suggested int64
	if lang != "" {
		if suggested, err = Suggest(ctx, s.q, lang); err != nil {
			return nil, err
		}
	}
	out := []Profile{}
	for _, row := range rows {
		if !Fits(row.SourceLanguages, lang) {
			continue
		}
		p, err := fromRow(row)
		if err != nil {
			return nil, err
		}
		p.Suggested = p.ID == suggested
		out = append(out, p)
	}
	return out, nil
}

// ListPage returns up to limit profiles after cursor, by id, with List's
// language filter.
func (s *Service) ListPage(ctx context.Context, tenantID int64, lang string, cursor int64, limit int32) ([]Profile, error) {
	lang = langcode.Normalize(lang)
	rows, err := s.q.ListSegmentationProfilesPage(ctx, db.ListSegmentationProfilesPageParams{
		TenantID: &tenantID, Cursor: cursor, SourceLanguage: lang, BaseLanguage: langcode.BaseLang(lang), Lim: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(rows))
	for _, row := range rows {
		p, err := fromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Get returns one profile: a built-in or one of the org's.
func (s *Service) Get(ctx context.Context, tenantID, id int64) (Profile, error) {
	return get(ctx, s.q, tenantID, id)
}

func get(ctx context.Context, q db.Querier, tenantID, id int64) (Profile, error) {
	row, err := q.GetSegmentationProfile(ctx, db.GetSegmentationProfileParams{ID: id, TenantID: &tenantID})
	if err != nil {
		return Profile{}, err
	}
	return fromRow(row)
}

// inTx runs fn in a transaction and commits when it returns nil.
func (s *Service) inTx(ctx context.Context, fn func(txq *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Create stores a new org profile.
func (s *Service) Create(ctx context.Context, tenantID, userID int64, in Input) (Profile, error) {
	def := in.Definition
	if in.From != nil {
		src, err := s.Get(ctx, tenantID, *in.From)
		if errors.Is(err, pgx.ErrNoRows) {
			return Profile{}, httputil.NewClientError("The segmentation profile to copy does not exist.")
		}
		if err != nil {
			return Profile{}, err
		}
		if def == nil {
			def = &src.Definition
		}
	}
	name, langs, raw, err := validInput(in.Name, in.SourceLanguages, def)
	if err != nil {
		return Profile{}, err
	}
	var createdBy *int64
	if userID != 0 {
		createdBy = &userID
	}
	var out Profile
	err = s.inTx(ctx, func(txq *db.Queries) error {
		id, err := txq.CreateSegmentationProfile(ctx, db.CreateSegmentationProfileParams{
			TenantID: tenantID, Name: name, SourceLanguages: langs, Definition: raw, BasedOn: in.From, CreatedBy: createdBy,
		})
		if err != nil {
			return nameTaken(err)
		}
		out, err = get(ctx, txq, tenantID, id)
		return err
	})
	return out, err
}

// Update replaces an org profile's name, language and definition.
// in.UpdatedAt must match the stored value. Projects and templates that use
// the profile keep it; a new language that a template using it would no
// longer suit is refused.
func (s *Service) Update(ctx context.Context, tenantID, id int64, in Input) (Profile, error) {
	if err := s.writable(ctx, tenantID, id); err != nil {
		return Profile{}, err
	}
	if in.UpdatedAt == nil {
		return Profile{}, httputil.NewClientError("updated_at is required.")
	}
	name, langs, raw, err := validInput(in.Name, in.SourceLanguages, in.Definition)
	if err != nil {
		return Profile{}, err
	}
	if err := s.templatesStillSuit(ctx, tenantID, id, langs); err != nil {
		return Profile{}, err
	}
	var out Profile
	err = s.inTx(ctx, func(txq *db.Queries) error {
		_, err := txq.UpdateSegmentationProfile(ctx, db.UpdateSegmentationProfileParams{
			ID: id, TenantID: tenantID, Name: name, SourceLanguages: langs, Definition: raw,
			ExpectedUpdatedAt: pgtype.Timestamptz{Time: *in.UpdatedAt, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// writable found it, so it was saved (or deleted) in between.
			return httputil.NewConflictError("Someone else changed this segmentation profile. Reload it and make your changes again.")
		}
		if err != nil {
			return nameTaken(err)
		}
		out, err = get(ctx, txq, tenantID, id)
		return err
	})
	return out, err
}

// Delete soft-deletes an org profile. A template that uses it must move to
// another profile first; the projects that use it lose it in the same
// transaction, and files keep the snapshot they were split with.
func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if err := s.writable(ctx, tenantID, id); err != nil {
		return err
	}
	return s.inTx(ctx, func(txq *db.Queries) error {
		deleted, err := txq.SoftDeleteSegmentationProfile(ctx, db.SoftDeleteSegmentationProfileParams{ID: id, TenantID: tenantID})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return pgx.ErrNoRows
		}
		// Counted after the soft delete: it waits for a save that holds the
		// profile row, so a template that picks it meanwhile is counted here,
		// and a later save waits for this transaction and is refused.
		uses, err := txq.CountSegmentationProfileUses(ctx, db.CountSegmentationProfileUsesParams{TenantID: tenantID, ID: &id})
		if err != nil {
			return err
		}
		if uses.Templates > 0 {
			return httputil.NewConflictError(fmt.Sprintf("%s use this segmentation profile. Choose another profile for them first.", Plural(uses.Templates, "template")))
		}
		if _, err := txq.ClearSegmentationProfileFromProjects(ctx, db.ClearSegmentationProfileFromProjectsParams{TenantID: tenantID, ID: id}); err != nil {
			return fmt.Errorf("clear segmentation profile from projects: %w", err)
		}
		return nil
	})
}

// templatesStillSuit refuses a new language for a profile that templates
// use when it would no longer suit their source language: they must move to
// another profile first, as for a delete.
func (s *Service) templatesStillSuit(ctx context.Context, tenantID, id int64, langs []string) error {
	templateLangs, err := s.q.ListSegmentationProfileTemplateLanguages(ctx, db.ListSegmentationProfileTemplateLanguagesParams{TenantID: tenantID, ID: &id})
	if err != nil {
		return err
	}
	var unfit int64
	for _, l := range templateLangs {
		if !Fits(langs, l) {
			unfit++
		}
	}
	if unfit > 0 {
		return httputil.NewConflictError(fmt.Sprintf("%s use this segmentation profile for a language it would no longer suit. Choose another profile for them first.", Plural(unfit, "template")))
	}
	return nil
}

// Uses counts the tenant's live projects and templates that name a profile.
func (s *Service) Uses(ctx context.Context, tenantID, id int64) (projects, templates int64, err error) {
	uses, err := s.q.CountSegmentationProfileUses(ctx, db.CountSegmentationProfileUsesParams{TenantID: tenantID, ID: &id})
	if err != nil {
		return 0, 0, err
	}
	return uses.Projects, uses.Templates, nil
}

// writable checks that id is one of the org's profiles: a built-in is a
// conflict, anything else not found.
func (s *Service) writable(ctx context.Context, tenantID, id int64) error {
	set, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if set.Builtin {
		return errBuiltinReadOnly
	}
	return nil
}

// ValidateInput checks a profile's name, language and definition as a save
// would.
func ValidateInput(in Input) error {
	_, _, _, err := validInput(in.Name, in.SourceLanguages, in.Definition)
	return err
}

// validInput normalizes and checks a profile's name, languages and
// definition, and returns the languages and the definition as stored. No
// languages means any language.
func validInput(name string, langs []string, def *segmentation.RuleSet) (string, []string, []byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, nil, httputil.NewClientError("Name is required.")
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", nil, nil, httputil.NewClientError(fmt.Sprintf("Name can be at most %d characters.", MaxNameLength))
	}
	langs, err := validLanguages(langs)
	if err != nil {
		return "", nil, nil, err
	}
	if def == nil {
		return "", nil, nil, httputil.NewClientError("Definition is required.")
	}
	rs := segmentation.Normalize(*def)
	if err := validRuleSet(rs); err != nil {
		return "", nil, nil, err
	}
	raw, err := json.Marshal(rs)
	if err != nil {
		return "", nil, nil, err
	}
	return name, langs, raw, nil
}

// validLanguages normalizes a profile's languages and drops repeats, in the
// order given.
func validLanguages(langs []string) ([]string, error) {
	if len(langs) > MaxSourceLanguages {
		return nil, httputil.NewClientError(fmt.Sprintf("A segmentation profile can have at most %d source languages.", MaxSourceLanguages))
	}
	out := make([]string, 0, len(langs))
	for _, l := range langs {
		n := langcode.Normalize(l)
		if n == "" || !langcode.Known(n) {
			return nil, httputil.NewClientError(fmt.Sprintf("%q is not a language code.", l))
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// languagesOf is a stored language list, never nil, so the API answers [].
func languagesOf(langs []string) []string {
	if langs == nil {
		return []string{}
	}
	return langs
}

// validRuleSet validates rs and compiles it, as the import will, so a save
// never stores rules that cannot run.
func validRuleSet(rs segmentation.RuleSet) error {
	if _, err := segmentation.Compile(rs); err != nil {
		var ie *segmentation.InvalidError
		if errors.As(err, &ie) {
			return httputil.NewClientError(ie.Message)
		}
		return err
	}
	return nil
}

func nameTaken(err error) error {
	if httputil.IsUniqueViolationOn(err, "segmentation_profiles_name_idx") {
		return httputil.NewConflictError("A segmentation profile with this name already exists.")
	}
	return err
}

// Plural is "1 template" or "3 templates".
func Plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func fromRow(row db.SegmentationProfileCatalog) (Profile, error) {
	var def segmentation.RuleSet
	if err := json.Unmarshal(row.Definition, &def); err != nil {
		return Profile{}, fmt.Errorf("read segmentation profile %d: %w", row.ID, err)
	}
	p := Profile{
		ID: row.ID, Name: row.Name, SourceLanguages: languagesOf(row.SourceLanguages), Builtin: row.TenantID == nil,
		Description: row.Description, BasedOn: row.BasedOn,
		Definition: segmentation.Normalize(def), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.BuiltinKey != nil {
		p.BuiltinKey = *row.BuiltinKey
	}
	return p, nil
}
