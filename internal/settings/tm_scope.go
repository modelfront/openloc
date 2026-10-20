package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// TMProfileRef names the TM profile a settings row follows. UsageCount is how
// many projects + templates follow it in total (this row included), so the UI
// can tell "only I use this" (safe to edit in place) from "shared".
type TMProfileRef struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	UsageCount int32  `json:"usage_count"`
}

// TMScope is what a settings row's TM matching actually uses: the row whose
// threshold + penalties apply, the TM rows, and the profile they come from
// (nil when the row is self-contained).
type TMScope struct {
	Profile       db.SettingsProfile
	TMAssignments []db.SettingsTmAssignment
	Linked        *TMProfileRef
}

// ResolveTMScope loads the effective TM scope for a project's or template's own
// settings row. Handlers return this (not the raw own row) so the UI shows
// exactly what the pipeline will use.
func ResolveTMScope(ctx context.Context, q db.Querier, tenantID int64, own db.SettingsProfile) (TMScope, error) {
	scope := TMScope{Profile: own}
	if own.TmProfileID != nil {
		tp, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: *own.TmProfileID, TenantID: tenantID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return TMScope{}, fmt.Errorf("get linked TM profile: %w", err)
		}
		if err == nil {
			usage, err := q.CountTMProfileUsage(ctx, db.CountTMProfileUsageParams{TmProfileID: &tp.ID, TenantID: tenantID})
			if err != nil {
				return TMScope{}, fmt.Errorf("count TM profile usage: %w", err)
			}
			scope.Linked = &TMProfileRef{ID: tp.ID, Name: tp.Name, UsageCount: usage.ProjectCount + usage.TemplateCount}
			scope.Profile, err = q.GetSettingsProfile(ctx, tp.SettingsProfileID)
			if err != nil {
				return TMScope{}, fmt.Errorf("get TM profile settings: %w", err)
			}
		}
	}
	rows, err := q.ListSettingsTMAssignments(ctx, scope.Profile.ID)
	if err != nil {
		return TMScope{}, fmt.Errorf("list TM assignments: %w", err)
	}
	scope.TMAssignments = rows
	return scope, nil
}

// Effective returns the own row with its match scoring replaced by the
// scope's — the values a response should show for threshold, penalties and
// lookup order.
func (s TMScope) Effective(own db.SettingsProfile) db.SettingsProfile {
	own.FuzzyThreshold = s.Profile.FuzzyThreshold
	own.SublanguagePenaltyPct = s.Profile.SublanguagePenaltyPct
	own.NearExactPenaltyPct = s.Profile.NearExactPenaltyPct
	own.TmLookupOrder = s.Profile.TmLookupOrder
	return own
}

// ParseTMProfileIDPatch reads the tri-state `tm_profile_id` PATCH field:
// absent → (nil, false); a positive id → (&id, true). `null` is refused: the
// only way off a profile is the detach endpoint, which copies the profile's
// rows so the project keeps translating the same way.
func ParseTMProfileIDPatch(raw json.RawMessage) (*int64, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, false, httputil.NewClientError("tm_profile_id cannot be cleared here; use the detach endpoint")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n <= 0 {
		return nil, false, httputil.NewClientError("invalid tm_profile_id (must be a positive integer)")
	}
	return &n, true, nil
}

// GuardTMScoringEdit rejects a request that edits threshold, penalties or lookup
// order on a row that follows (or is about to follow) a TM profile. Those
// values belong to the profile; handlers map the error to 409.
func GuardTMScoringEdit(linked bool, threshold *float32, sublanguage, nearExact *int32, lookupOrder *string) error {
	if linked && (threshold != nil || sublanguage != nil || nearExact != nil || lookupOrder != nil) {
		return ErrTMProfileLinked
	}
	return nil
}

// WriteTMProfileLinked writes the 409 for ErrTMProfileLinked with a stable code
// the UI keys on to point at "Detach and customise".
func WriteTMProfileLinked(w http.ResponseWriter) {
	httputil.ErrorCode(w, http.StatusConflict, "tm_profile_linked", ErrTMProfileLinked.Error())
}
