package segmentationapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/segmentation"
)

// Fits reports whether a profile for profileLangs suits a project or
// template in lang: a profile for any language (no languages) suits every
// one, and with no language every profile does. Otherwise lang or its base
// language is one of the profile's languages.
func Fits(profileLangs []string, lang string) bool {
	if len(profileLangs) == 0 || lang == "" {
		return true
	}
	lang = langcode.Normalize(lang)
	base := langcode.BaseLang(lang)
	for _, l := range profileLangs {
		if l == lang || l == base {
			return true
		}
	}
	return false
}

// Suggest returns the profile a new project or template in lang starts with:
// the newest built-in for the language, then for its base language, then the
// newest built-in for any language.
func Suggest(ctx context.Context, q db.Querier, lang string) (int64, error) {
	lang = langcode.Normalize(lang)
	langs := []string{}
	if lang != "" {
		langs = append(langs, lang)
		if base := langcode.BaseLang(lang); base != lang {
			langs = append(langs, base)
		}
	}
	rows, err := q.ListBuiltinSegmentationProfiles(ctx)
	if err != nil {
		return 0, fmt.Errorf("list built-in segmentation profiles: %w", err)
	}
	// rows hold the newest built-in first, so the first match is the pick.
	for _, l := range langs {
		for _, r := range rows {
			if slices.Contains(r.SourceLanguages, l) {
				return r.ID, nil
			}
		}
	}
	for _, r := range rows {
		if r.BuiltinKey != nil && strings.HasPrefix(*r.BuiltinKey, segmentation.DefaultKeyPrefix) {
			return r.ID, nil
		}
	}
	return 0, fmt.Errorf("no built-in %s* segmentation profile", segmentation.DefaultKeyPrefix)
}

// Check refuses a profile id that is not a live profile of the tenant (or a
// built-in), or that does not fit lang, with a client error.
func Check(ctx context.Context, q db.Querier, tenantID, id int64, lang string) error {
	p, err := get(ctx, q, tenantID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProfileNotFound
	}
	if err != nil {
		return err
	}
	if !Fits(p.SourceLanguages, lang) {
		return httputil.NewClientError(fmt.Sprintf("The segmentation profile %q is for %s, not for the source language %s.", p.Name, strings.Join(p.SourceLanguages, ", "), langcode.Normalize(lang)))
	}
	return nil
}

// Choose returns the profile a project or template in lang uses. requested,
// when set, must be a live profile of the tenant (or a built-in) that fits
// lang. Otherwise current is kept while it fits lang, and anything else gets
// Suggest's pick.
func Choose(ctx context.Context, q db.Querier, tenantID int64, requested, current *int64, lang string) (int64, error) {
	if requested != nil {
		if err := Check(ctx, q, tenantID, *requested, lang); err != nil {
			return 0, err
		}
		return *requested, nil
	}
	if current != nil {
		row, err := q.GetSegmentationProfileForImport(ctx, db.GetSegmentationProfileForImportParams{ID: *current, TenantID: &tenantID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if err == nil && Fits(row.SourceLanguages, lang) {
			return row.ID, nil
		}
	}
	return Suggest(ctx, q, lang)
}

// IsAnyLanguageDefault reports whether id is the built-in for any language
// that Suggest falls back to: on a template without a source language it
// stands for "the project's own language", not a choice.
func IsAnyLanguageDefault(ctx context.Context, q db.Querier, tenantID, id int64) (bool, error) {
	row, err := q.GetSegmentationProfileForImport(ctx, db.GetSegmentationProfileForImportParams{ID: id, TenantID: &tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.BuiltinKey != nil && strings.HasPrefix(*row.BuiltinKey, segmentation.DefaultKeyPrefix), nil
}

// ErrProfileNotFound refuses a profile id that is not a live profile of the
// tenant or a built-in, including one deleted while a save was on its way.
var ErrProfileNotFound = httputil.NewClientError("Segmentation profile not found.")

// ErrNoProfile refuses an upload to a project that names no segmentation
// profile: its profile was deleted, and someone has to pick another.
var ErrNoProfile = httputil.NewClientError("This project has no segmentation profile. Pick one in the project's settings first.")

// Resolve returns the snapshot a new file in sourceLang is split with: the
// profile the project names. A profile deleted after the project's settings
// were read still resolves. With no profile it is ErrNoProfile.
func Resolve(ctx context.Context, q db.Querier, tenantID int64, profileID *int64, sourceLang string) (segmentation.Snapshot, error) {
	lang := langcode.Normalize(sourceLang)
	if profileID == nil {
		return segmentation.Snapshot{}, ErrNoProfile
	}
	id := *profileID
	row, err := q.GetSegmentationProfileForImport(ctx, db.GetSegmentationProfileForImportParams{ID: id, TenantID: &tenantID})
	if err != nil {
		return segmentation.Snapshot{}, fmt.Errorf("read segmentation profile %d: %w", id, err)
	}
	p, err := fromRow(row)
	if err != nil {
		return segmentation.Snapshot{}, err
	}
	src := segmentation.SnapshotSource{Kind: segmentation.SourceKindOrg, ID: strconv.FormatInt(p.ID, 10), Name: p.Name}
	if p.Builtin {
		src = segmentation.SnapshotSource{Kind: segmentation.SourceKindBuiltin, ID: p.BuiltinKey, Name: p.Name}
	}
	return segmentation.Snapshot{RuleSet: p.Definition, Source: src, Language: lang, ProfileID: p.ID}, nil
}
