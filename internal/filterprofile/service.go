// Package filterprofile stores and lists filter profiles (named import
// options for one file format: the built-ins, shared rows with no tenant,
// and an org's own), keeps the profile a project or template uses per
// format, resolves the snapshot a new file is parsed with, and serves them
// over HTTP.
//
// The functions here are transport-free and shared by the REST handlers, the
// customer API and the MCP tools. A caller mistake comes back as
// httputil.ClientError, a refused change (a built-in, a stale save, a taken
// name, a profile templates use) as httputil.ConflictError, and an id outside
// the tenant as pgx.ErrNoRows.
package filterprofile

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
)

// MaxNameLength is the longest profile name (the column's check).
const MaxNameLength = 100

var errBuiltinReadOnly = httputil.NewConflictError("Built-in filter profiles are read-only. Copy it to make changes.")

// Service reads and writes filter profiles for one database.
type Service struct {
	q    db.Querier
	pool *pgxpool.Pool
}

// NewService returns a Service; pool runs the multi-step writes.
func NewService(q db.Querier, pool *pgxpool.Pool) *Service {
	return &Service{q: q, pool: pool}
}

// List returns the built-ins, then the org's profiles, per format; format,
// when set, keeps one format's.
func (s *Service) List(ctx context.Context, tenantID int64, format string) ([]Profile, error) {
	if err := checkFormatFilter(format); err != nil {
		return nil, err
	}
	rows, err := s.q.ListFilterProfiles(ctx, db.ListFilterProfilesParams{TenantID: &tenantID, Format: format})
	if err != nil {
		return nil, err
	}
	return s.withOrgDefaults(ctx, tenantID, rows)
}

// ListPage returns up to limit profiles after cursor, by id, with List's
// format filter.
func (s *Service) ListPage(ctx context.Context, tenantID int64, format string, cursor int64, limit int32) ([]Profile, error) {
	if err := checkFormatFilter(format); err != nil {
		return nil, err
	}
	rows, err := s.q.ListFilterProfilesPage(ctx, db.ListFilterProfilesPageParams{TenantID: &tenantID, Format: format, Cursor: cursor, Lim: limit})
	if err != nil {
		return nil, err
	}
	return s.withOrgDefaults(ctx, tenantID, rows)
}

// withOrgDefaults converts rows and marks the tenant's defaults among them.
func (s *Service) withOrgDefaults(ctx context.Context, tenantID int64, rows []db.FilterProfileCatalog) ([]Profile, error) {
	profiles, err := fromRows(rows)
	if err != nil {
		return nil, err
	}
	if err := markOrgDefaults(ctx, s.q, tenantID, profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// Get returns one profile: a built-in or one of the org's.
func (s *Service) Get(ctx context.Context, tenantID, id int64) (Profile, error) {
	p, err := get(ctx, s.q, tenantID, id)
	if err != nil {
		return Profile{}, err
	}
	return s.marked(ctx, tenantID, p)
}

// marked returns p with OrgDefault set when it is the tenant's default.
func (s *Service) marked(ctx context.Context, tenantID int64, p Profile) (Profile, error) {
	one := []Profile{p}
	if err := markOrgDefaults(ctx, s.q, tenantID, one); err != nil {
		return Profile{}, err
	}
	return one[0], nil
}

func get(ctx context.Context, q db.Querier, tenantID, id int64) (Profile, error) {
	row, err := q.GetFilterProfile(ctx, db.GetFilterProfileParams{ID: id, TenantID: &tenantID})
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

// Create stores a new org profile. With From, the format is the copied
// profile's and Options default to its options.
func (s *Service) Create(ctx context.Context, tenantID, userID int64, in Input) (Profile, error) {
	opts, format := in.Options, in.Format
	if in.From != nil {
		src, err := s.Get(ctx, tenantID, *in.From)
		if errors.Is(err, pgx.ErrNoRows) {
			return Profile{}, httputil.NewClientError("The filter profile to copy does not exist.")
		}
		if err != nil {
			return Profile{}, err
		}
		if format != "" && format != src.Format {
			return Profile{}, httputil.NewClientError("A copy has the format of the profile it copies.")
		}
		format = src.Format
		if opts == nil {
			opts = &src.Options
		}
	}
	if !slices.Contains(Formats, format) {
		return Profile{}, httputil.NewClientError(fmt.Sprintf("format must be one of %s.", strings.Join(Formats, ", ")))
	}
	name, raw, err := validInput(in.Name, format, opts)
	if err != nil {
		return Profile{}, err
	}
	var createdBy *int64
	if userID != 0 {
		createdBy = &userID
	}
	var out Profile
	err = s.inTx(ctx, func(txq *db.Queries) error {
		id, err := txq.CreateFilterProfile(ctx, db.CreateFilterProfileParams{
			TenantID: tenantID, Name: name, Format: format, Options: raw, BasedOn: in.From, CreatedBy: createdBy,
		})
		if err != nil {
			return nameTaken(err)
		}
		out, err = get(ctx, txq, tenantID, id)
		return err
	})
	return out, err
}

// Update replaces an org profile's name and options. in.UpdatedAt must match
// the stored value. Projects and templates that use the profile keep it;
// files keep the snapshot they were imported with.
func (s *Service) Update(ctx context.Context, tenantID, id int64, in Input) (Profile, error) {
	cur, err := s.writable(ctx, tenantID, id)
	if err != nil {
		return Profile{}, err
	}
	if in.UpdatedAt == nil {
		return Profile{}, httputil.NewClientError("updated_at is required.")
	}
	if in.Format != "" && in.Format != cur.Format {
		return Profile{}, httputil.NewClientError("A filter profile's format cannot change.")
	}
	name, raw, err := validInput(in.Name, cur.Format, in.Options)
	if err != nil {
		return Profile{}, err
	}
	var out Profile
	err = s.inTx(ctx, func(txq *db.Queries) error {
		_, err := txq.UpdateFilterProfile(ctx, db.UpdateFilterProfileParams{
			ID: id, TenantID: tenantID, Name: name, Options: raw,
			ExpectedUpdatedAt: pgtype.Timestamptz{Time: *in.UpdatedAt, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// writable found it, so it was saved (or deleted) in between.
			return httputil.NewConflictError("Someone else changed this filter profile. Reload it and make your changes again.")
		}
		if err != nil {
			return nameTaken(err)
		}
		out, err = get(ctx, txq, tenantID, id)
		return err
	})
	if err != nil {
		return Profile{}, err
	}
	return s.marked(ctx, tenantID, out)
}

// Delete soft-deletes an org profile. A template that uses it must move to
// another profile first, and so must the org default; the projects that use
// it go back to the format's Default in the same transaction, and files keep
// their snapshot.
func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if _, err := s.writable(ctx, tenantID, id); err != nil {
		return err
	}
	return s.inTx(ctx, func(txq *db.Queries) error {
		deleted, err := txq.SoftDeleteFilterProfile(ctx, db.SoftDeleteFilterProfileParams{ID: id, TenantID: tenantID})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return pgx.ErrNoRows
		}
		// Counted after the soft delete: it waits for a save that holds the
		// profile row, so a template that picks it meanwhile is counted here,
		// and a later save waits for this transaction and is refused.
		uses, err := txq.CountFilterProfileUses(ctx, db.CountFilterProfileUsesParams{TenantID: tenantID, ID: id})
		if err != nil {
			return err
		}
		if uses.Templates > 0 {
			return httputil.NewConflictError(fmt.Sprintf("%s use this filter profile. Choose another profile for them first.", plural(uses.Templates, "template")))
		}
		isDefault, err := txq.IsFilterProfileDefault(ctx, db.IsFilterProfileDefaultParams{TenantID: tenantID, FilterProfileID: id})
		if err != nil {
			return err
		}
		if isDefault {
			return httputil.NewConflictError("This filter profile is your organization's default for its format. Set another default first.")
		}
		if _, err := txq.ClearFilterProfileFromProjects(ctx, db.ClearFilterProfileFromProjectsParams{TenantID: tenantID, ID: id}); err != nil {
			return fmt.Errorf("clear filter profile from projects: %w", err)
		}
		return nil
	})
}

// Uses counts the tenant's live projects and templates that name a profile.
func (s *Service) Uses(ctx context.Context, tenantID, id int64) (projects, templates int64, err error) {
	uses, err := s.q.CountFilterProfileUses(ctx, db.CountFilterProfileUsesParams{TenantID: tenantID, ID: id})
	if err != nil {
		return 0, 0, err
	}
	return uses.Projects, uses.Templates, nil
}

// writable returns id when it is one of the org's profiles: a built-in is a
// conflict, anything else not found.
func (s *Service) writable(ctx context.Context, tenantID, id int64) (Profile, error) {
	p, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return Profile{}, err
	}
	if p.Builtin {
		return Profile{}, errBuiltinReadOnly
	}
	return p, nil
}

// validInput checks a profile's name and options for format and returns the
// options as stored.
func validInput(name, format string, opts *Options) (string, []byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, httputil.NewClientError("Name is required.")
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", nil, httputil.NewClientError(fmt.Sprintf("Name can be at most %d characters.", MaxNameLength))
	}
	if opts == nil {
		return "", nil, httputil.NewClientError("Options are required.")
	}
	if err := checkSizes(*opts); err != nil {
		return "", nil, err
	}
	o := normalized(format, *opts)
	if err := validate(o); err != nil {
		return "", nil, err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return "", nil, err
	}
	return name, raw, nil
}

func checkFormatFilter(format string) error {
	if format != "" && !slices.Contains(Formats, format) {
		return httputil.NewClientError(fmt.Sprintf("format must be one of %s.", strings.Join(Formats, ", ")))
	}
	return nil
}

func nameTaken(err error) error {
	if httputil.IsUniqueViolationOn(err, "filter_profiles_name_idx") {
		return httputil.NewConflictError("A filter profile with this name already exists for this format.")
	}
	return err
}

// plural is "1 template" or "3 templates".
func plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func fromRows(rows []db.FilterProfileCatalog) ([]Profile, error) {
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

func fromRow(row db.FilterProfileCatalog) (Profile, error) {
	var o Options
	if err := json.Unmarshal(row.Options, &o); err != nil {
		return Profile{}, fmt.Errorf("read filter profile %d: %w", row.ID, err)
	}
	p := Profile{
		ID: row.ID, Name: row.Name, Format: row.Format, Builtin: row.TenantID == nil,
		Description: row.Description, BasedOn: row.BasedOn, Options: normalized(row.Format, o),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.BuiltinKey != nil {
		p.BuiltinKey = *row.BuiltinKey
	}
	return p, nil
}
