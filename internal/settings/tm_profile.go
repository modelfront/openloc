package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// ErrTMProfileLinked is returned when a caller tries to edit TM rows or match
// scoring on a settings row that follows a TM profile. Those values belong to
// the profile; handlers map this to 409 so the UI can point at "Detach".
var ErrTMProfileLinked = errors.New("settings follow a TM profile; detach to customise")

// ClampPctPtr bounds an optional percentage, preserving nil (= leave the stored
// value alone).
func ClampPctPtr(v *int32) *int32 {
	if v == nil {
		return nil
	}
	c := ClampPct(*v)
	return &c
}

// LinkTMProfile points a project's or template's own settings row at a TM
// profile in the same tenant. The row's own TM assignments are deleted: while
// linked they are never read, and leaving them would show stale rows on detach
// and inflate the TM usage counts. Own scoring scalars are left untouched (they
// are ignored while linked and overwritten on detach). Must run inside the
// caller's transaction.
//
// Only project/template rows are ever passed here; a TM profile's own settings
// row is never linked, so chains cannot form.
func LinkTMProfile(ctx context.Context, q db.Querier, tenantID, ownSettingsProfileID, tmProfileID int64) (db.SettingsProfile, error) {
	if _, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: tmProfileID, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.SettingsProfile{}, httputil.NewClientError("TM profile not found in this tenant")
		}
		return db.SettingsProfile{}, fmt.Errorf("verify TM profile: %w", err)
	}
	if err := q.DeleteSettingsTMAssignments(ctx, ownSettingsProfileID); err != nil {
		return db.SettingsProfile{}, fmt.Errorf("clear own TM assignments: %w", err)
	}
	row, err := q.SetSettingsProfileTMProfile(ctx, db.SetSettingsProfileTMProfileParams{
		ID: ownSettingsProfileID, TmProfileID: &tmProfileID,
	})
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("link TM profile: %w", err)
	}
	return row, nil
}

// LinkTMProfileTx wraps LinkTMProfile in a transaction.
func LinkTMProfileTx(ctx context.Context, pool database.TxBeginner, tenantID, ownSettingsProfileID, tmProfileID int64) (db.SettingsProfile, error) {
	return withTx(ctx, pool, func(q db.Querier) (db.SettingsProfile, error) {
		return LinkTMProfile(ctx, q, tenantID, ownSettingsProfileID, tmProfileID)
	})
}

// DetachTMProfile turns a linked settings row back into a self-contained one:
// the profile's TM rows and its three scoring values are copied onto the row,
// then the link is cleared. This is the only way to leave a profile — the copy
// is what keeps the project translating the same way after the link is gone.
// Must run inside the caller's transaction.
func DetachTMProfile(ctx context.Context, q db.Querier, ownSettingsProfileID int64) (db.SettingsProfile, error) {
	own, err := q.GetSettingsProfile(ctx, ownSettingsProfileID)
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("get settings profile: %w", err)
	}
	if own.TmProfileID == nil {
		return db.SettingsProfile{}, httputil.NewClientError("settings do not follow a TM profile")
	}
	scope, err := q.GetTMScopeSettingsProfile(ctx, ownSettingsProfileID)
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("get TM scope profile: %w", err)
	}
	if err := CopyTMAssignments(ctx, q, scope.ID, own.ID); err != nil {
		return db.SettingsProfile{}, err
	}
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:                    own.ID,
		FuzzyThreshold:        scope.FuzzyThreshold,
		MtOnUpload:            own.MtOnUpload,
		TmOnUpload:            own.TmOnUpload,
		QaProfileID:           own.QaProfileID,
		SublanguagePenaltyPct: &scope.SublanguagePenaltyPct,
		NearExactPenaltyPct:   &scope.NearExactPenaltyPct,
		TmLookupOrder:         &scope.TmLookupOrder,
	}); err != nil {
		return db.SettingsProfile{}, fmt.Errorf("copy match scoring: %w", err)
	}
	row, err := q.SetSettingsProfileTMProfile(ctx, db.SetSettingsProfileTMProfileParams{ID: own.ID})
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("clear TM profile link: %w", err)
	}
	return row, nil
}

// DetachTMProfileTx wraps DetachTMProfile in a transaction.
func DetachTMProfileTx(ctx context.Context, pool database.TxBeginner, ownSettingsProfileID int64) (db.SettingsProfile, error) {
	return withTx(ctx, pool, func(q db.Querier) (db.SettingsProfile, error) {
		return DetachTMProfile(ctx, q, ownSettingsProfileID)
	})
}
