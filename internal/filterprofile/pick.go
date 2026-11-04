package filterprofile

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// ErrProfileNotFound refuses a profile id that is not a live profile of the
// tenant or a built-in, including one deleted while a save was on its way.
var ErrProfileNotFound = httputil.NewClientError("Filter profile not found.")

// Check refuses an id that is not a live profile of the tenant (or a
// built-in) of format, with a client error.
func Check(ctx context.Context, q db.Querier, tenantID, id int64, format string) error {
	p, err := get(ctx, q, tenantID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProfileNotFound
	}
	if err != nil {
		return err
	}
	if p.Format != format {
		return httputil.NewClientError(fmt.Sprintf("The filter profile %q is for %s files, not %s.", p.Name, p.Format, format))
	}
	return nil
}

// SetChoices stores a project's or template's profile per format: a nil id
// goes back to the format's Default, a format left out keeps its choice.
// settingsProfileID must be a settings row the caller read with tenantID.
func SetChoices(ctx context.Context, q db.Querier, tenantID, settingsProfileID int64, choices map[string]*int64) error {
	formats := make([]string, 0, len(choices))
	for f := range choices {
		formats = append(formats, f)
	}
	sort.Strings(formats) // a stable lock order
	for _, format := range formats {
		if !slices.Contains(Formats, format) {
			return httputil.NewClientError(fmt.Sprintf("%q has no filter profiles.", format))
		}
		id := choices[format]
		if id == nil {
			if err := q.ClearSettingsFilterProfile(ctx, db.ClearSettingsFilterProfileParams{SettingsProfileID: settingsProfileID, Format: format}); err != nil {
				return fmt.Errorf("clear filter profile: %w", err)
			}
			continue
		}
		if err := Check(ctx, q, tenantID, *id, format); err != nil {
			return err
		}
		n, err := q.SetSettingsFilterProfile(ctx, db.SetSettingsFilterProfileParams{SettingsProfileID: settingsProfileID, Format: format, FilterProfileID: *id})
		if err != nil {
			return fmt.Errorf("set filter profile: %w", err)
		}
		if n == 0 {
			return ErrProfileNotFound
		}
	}
	return nil
}

// ValidateChoices checks choices as SetChoices would, without storing them.
func ValidateChoices(ctx context.Context, q db.Querier, tenantID int64, choices map[string]*int64) error {
	for format, id := range choices {
		if !slices.Contains(Formats, format) {
			return httputil.NewClientError(fmt.Sprintf("%q has no filter profiles.", format))
		}
		if id != nil {
			if err := Check(ctx, q, tenantID, *id, format); err != nil {
				return err
			}
		}
	}
	return nil
}

// Choices returns the profile a settings row names per format; a format left
// out uses its Default.
func Choices(ctx context.Context, q db.Querier, settingsProfileID int64) ([]Choice, error) {
	rows, err := q.ListSettingsFilterProfiles(ctx, settingsProfileID)
	if err != nil {
		return nil, fmt.Errorf("list filter profiles: %w", err)
	}
	out := make([]Choice, 0, len(rows))
	for _, r := range rows {
		out = append(out, Choice{Format: r.Format, ID: r.FilterProfileID, Name: r.Name})
	}
	return out, nil
}

// CopyChoices gives the settings row dst the choices of src.
func CopyChoices(ctx context.Context, q db.Querier, src, dst int64) error {
	if err := q.CopySettingsFilterProfiles(ctx, db.CopySettingsFilterProfilesParams{Src: src, Dst: dst}); err != nil {
		return fmt.Errorf("copy filter profiles: %w", err)
	}
	return nil
}

// Resolve returns the snapshot a new file of fileFormat is parsed with:
// override when set (a live profile of the tenant, or a built-in, of the
// file's format), else the profile the settings row names, else the
// format's Default. A profile deleted after the settings were read still
// resolves. nil when the file format has no filter profiles.
func Resolve(ctx context.Context, q db.Querier, tenantID, settingsProfileID int64, fileFormat string, override *int64) (*Snapshot, error) {
	format, ok := FormatOf(fileFormat)
	if !ok {
		// A .xml file is only known to be Android string resources once its
		// root is read, so the uploader was offered XML profiles: an XML pick
		// of the tenant is ignored, not refused.
		if override != nil && fileFormat == "android_xml" {
			return nil, Check(ctx, q, tenantID, *override, FormatXML)
		}
		if override != nil {
			return nil, httputil.NewClientError(fmt.Sprintf("%s files have no filter profiles.", fileFormat))
		}
		return nil, nil
	}
	var row db.FilterProfileCatalog
	switch {
	case override != nil:
		if err := Check(ctx, q, tenantID, *override, format); err != nil {
			return nil, err
		}
		r, err := q.GetFilterProfile(ctx, db.GetFilterProfileParams{ID: *override, TenantID: &tenantID})
		if err != nil {
			return nil, fmt.Errorf("read filter profile %d: %w", *override, err)
		}
		row = r
	default:
		id, err := q.GetSettingsFilterProfile(ctx, db.GetSettingsFilterProfileParams{SettingsProfileID: settingsProfileID, Format: format})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if row, err = q.GetDefaultFilterProfile(ctx, format); err != nil {
				return nil, fmt.Errorf("read default %s filter profile: %w", format, err)
			}
		case err != nil:
			return nil, fmt.Errorf("read project filter profile: %w", err)
		default:
			if row, err = q.GetFilterProfileForImport(ctx, db.GetFilterProfileForImportParams{ID: id, TenantID: &tenantID}); err != nil {
				return nil, fmt.Errorf("read filter profile %d: %w", id, err)
			}
		}
	}
	p, err := fromRow(row)
	if err != nil {
		return nil, err
	}
	return &Snapshot{ID: p.ID, Name: p.Name, Format: p.Format, Builtin: p.Builtin, Options: p.Options}, nil
}

// InlineHTML is what a snapshot says about tags in strings: on unless the
// profile turns it off. A nil snapshot (a format without profiles, or a file
// stored before them) is on.
func (s *Snapshot) InlineHTML() bool {
	return s == nil || s.Options.InlineHTML == nil || *s.Options.InlineHTML
}

// InlineHTMLChoices are the choices that stand for the "Strings contain HTML"
// switch of older MCP clients: on is each string format's Default, off its
// Plain text strings built-in. explicit, the client's own picks, win per
// format.
func InlineHTMLChoices(ctx context.Context, q db.Querier, on bool, explicit map[string]*int64) (map[string]*int64, error) {
	out := make(map[string]*int64, len(inlineHTMLFormats)+len(explicit))
	for _, format := range inlineHTMLFormats {
		if on {
			out[format] = nil
			continue
		}
		row, err := q.GetBuiltinFilterProfileByKey(ctx, plainKey(format))
		if err != nil {
			return nil, fmt.Errorf("read plain text %s filter profile: %w", format, err)
		}
		out[format] = &row.ID
	}
	for format, id := range explicit {
		out[format] = id
	}
	return out, nil
}

// plainKey is the builtin_key of a format's Plain text strings built-in.
func plainKey(format string) *string {
	k := "builtin:" + format + "-plain@1"
	return &k
}
