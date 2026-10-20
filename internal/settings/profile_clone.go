package settings

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// CreateProfileLike creates a new settings profile carrying every scalar of
// src: the insert-time fields, the penalties, auto_complete and inline_html (which
// CreateSettingsProfile deliberately leaves at the column defaults), and the TM
// profile link. Shared by save-as-template and template duplication so a
// snapshot can't drift from its source one column at a time. Must run inside
// the caller's transaction.
func CreateProfileLike(ctx context.Context, q db.Querier, tenantID int64, src db.SettingsProfile) (db.SettingsProfile, error) {
	created, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: src.FuzzyThreshold,
		MtOnUpload:     src.MtOnUpload,
		TmOnUpload:     src.TmOnUpload,
		QaProfileID:    src.QaProfileID,
	})
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("create settings profile: %w", err)
	}
	// Explicit pointers, not nil-keeps-stored: a copy must carry the source's
	// values even when they equal the defaults.
	created, err = q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:                    created.ID,
		FuzzyThreshold:        created.FuzzyThreshold,
		MtOnUpload:            created.MtOnUpload,
		TmOnUpload:            created.TmOnUpload,
		QaProfileID:           created.QaProfileID,
		SublanguagePenaltyPct: &src.SublanguagePenaltyPct,
		NearExactPenaltyPct:   &src.NearExactPenaltyPct,
		AutoComplete:          &src.AutoComplete,
		InlineHtml:            &src.InlineHtml,
		TmLookupOrder:         &src.TmLookupOrder,
	})
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("copy penalties: %w", err)
	}
	if src.TmProfileID != nil {
		created, err = q.SetSettingsProfileTMProfile(ctx, db.SetSettingsProfileTMProfileParams{
			ID: created.ID, TmProfileID: src.TmProfileID,
		})
		if err != nil {
			return db.SettingsProfile{}, fmt.Errorf("copy TM profile link: %w", err)
		}
	}
	return created, nil
}
