// Package settings provides shared helpers for settings profile operations
// used by both project and template handlers.
package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// SetTMAssignmentsTx wraps SetTMAssignments in a transaction so a mid-loop
// failure (e.g. FK violation on the 3rd row) rolls back the preceding DELETE.
func SetTMAssignmentsTx(ctx context.Context, pool database.TxBeginner, tenantID, profileID int64, r *http.Request) ([]db.SettingsTmAssignment, error) {
	return withTx(ctx, pool, func(q db.Querier) ([]db.SettingsTmAssignment, error) {
		return SetTMAssignments(ctx, q, tenantID, profileID, r)
	})
}

// SetTermAssignmentsTx wraps SetTermAssignments in a transaction.
func SetTermAssignmentsTx(ctx context.Context, pool database.TxBeginner, tenantID, profileID int64, r *http.Request) ([]db.SettingsTerminologyAssignment, error) {
	return withTx(ctx, pool, func(q db.Querier) ([]db.SettingsTerminologyAssignment, error) {
		return SetTermAssignments(ctx, q, tenantID, profileID, r)
	})
}

// AssignmentQuerier is the subset of db.Querier used by assignment operations.
// Accepting this instead of the full Querier makes testing straightforward.
type AssignmentQuerier interface {
	DeleteSettingsTMAssignments(ctx context.Context, settingsProfileID int64) error
	CreateSettingsTMAssignment(ctx context.Context, arg db.CreateSettingsTMAssignmentParams) (db.SettingsTmAssignment, error)
	ListSettingsTMAssignments(ctx context.Context, settingsProfileID int64) ([]db.SettingsTmAssignment, error)
	GetTMCollection(ctx context.Context, arg db.GetTMCollectionParams) (db.TmCollection, error)
	GetTermBase(ctx context.Context, arg db.GetTermBaseParams) (db.TermBasis, error)

	DeleteSettingsTermAssignments(ctx context.Context, settingsProfileID int64) error
	CreateSettingsTermAssignment(ctx context.Context, arg db.CreateSettingsTermAssignmentParams) (db.SettingsTerminologyAssignment, error)
	ListSettingsTermAssignments(ctx context.Context, settingsProfileID int64) ([]db.SettingsTerminologyAssignment, error)
}

// Compile-time check: db.Querier satisfies AssignmentQuerier.
var _ AssignmentQuerier = (db.Querier)(nil)

// --- TM Assignments ---

// TMAssignmentInput is one TM assignment row as submitted by clients.
// TM assignments are per-TM. Source/target languages come from the
// TM's own tm_collections metadata, not from the assignment — so the input
// carries only tm_id + mode + priority. Exported so the atomic project-create
// path (project.CreateWithSetup) can apply the same shape without an HTTP
// request in hand.
type TMAssignmentInput struct {
	TmID     int64  `json:"tm_id"`
	Mode     string `json:"mode"`
	Priority int32  `json:"priority"`
	// PenaltyPct docks every match from this TM — 0–100, default
	// 0. Out-of-range values are clamped rather than rejected: the column has a
	// CHECK constraint and a 500 on a stray slider value helps nobody.
	PenaltyPct int32 `json:"penalty_pct"`
}

type tmAssignmentReq struct {
	Assignments []TMAssignmentInput `json:"assignments"`
}

// SetTMAssignments replaces all TM assignments for a settings profile from an
// HTTP request body. Thin decode wrapper over ApplyTMAssignments.
func SetTMAssignments(ctx context.Context, q AssignmentQuerier, tenantID, profileID int64, r *http.Request) ([]db.SettingsTmAssignment, error) {
	var req tmAssignmentReq
	if err := httputil.Decode(r, &req); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return ApplyTMAssignments(ctx, q, tenantID, profileID, req.Assignments)
}

// ApplyTMAssignments replaces all TM assignments for a settings profile.
// Every tm_id is verified to belong to tenantID — the insert is otherwise a
// blind FK, and trusting a client-supplied id would let one tenant read
// matches out of another tenant's TM.
func ApplyTMAssignments(ctx context.Context, q AssignmentQuerier, tenantID, profileID int64, assignments []TMAssignmentInput) ([]db.SettingsTmAssignment, error) {
	if err := q.DeleteSettingsTMAssignments(ctx, profileID); err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}

	// Dedupe by tm_id — a full-replace with duplicate tm_ids would violate the
	// UNIQUE(settings_profile_id, tm_id) constraint (migration 051).
	seen := make(map[int64]bool, len(assignments))
	for _, a := range assignments {
		if seen[a.TmID] {
			continue
		}
		seen[a.TmID] = true
		if _, err := q.GetTMCollection(ctx, db.GetTMCollectionParams{ID: a.TmID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, httputil.NewClientError(fmt.Sprintf("TM %d not found", a.TmID))
			}
			return nil, fmt.Errorf("verify TM %d: %w", a.TmID, err)
		}
		mode := a.Mode
		if mode == "" {
			mode = "read_write"
		}
		if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
			SettingsProfileID: profileID, TmID: a.TmID,
			Mode: mode, Priority: a.Priority, PenaltyPct: ClampPct(a.PenaltyPct),
		}); err != nil {
			return nil, fmt.Errorf("create: %w", err)
		}
	}

	return q.ListSettingsTMAssignments(ctx, profileID)
}

// ApplyTMAssignmentsTx is ApplyTMAssignments wrapped in a transaction.
func ApplyTMAssignmentsTx(ctx context.Context, pool database.TxBeginner, tenantID, profileID int64, assignments []TMAssignmentInput) ([]db.SettingsTmAssignment, error) {
	return withTx(ctx, pool, func(q db.Querier) ([]db.SettingsTmAssignment, error) {
		return ApplyTMAssignments(ctx, q, tenantID, profileID, assignments)
	})
}

// --- Terminology Assignments ---

// TermAssignmentInput is one terminology assignment row as submitted by clients.
type TermAssignmentInput struct {
	TermBaseID     int64  `json:"term_base_id"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	Mode           string `json:"mode"`
	Priority       int32  `json:"priority"`
}

type termAssignmentReq struct {
	Assignments []TermAssignmentInput `json:"assignments"`
}

// SetTermAssignments replaces all terminology assignments for a settings
// profile from an HTTP request body. Thin decode wrapper over
// ApplyTermAssignments.
func SetTermAssignments(ctx context.Context, q AssignmentQuerier, tenantID, profileID int64, r *http.Request) ([]db.SettingsTerminologyAssignment, error) {
	var req termAssignmentReq
	if err := httputil.Decode(r, &req); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return ApplyTermAssignments(ctx, q, tenantID, profileID, req.Assignments)
}

// ApplyTermAssignments replaces all terminology assignments for a settings
// profile. Every term_base_id is verified to belong to tenantID — same
// never-trust-a-client-id rule as ApplyTMAssignments.
func ApplyTermAssignments(ctx context.Context, q AssignmentQuerier, tenantID, profileID int64, assignments []TermAssignmentInput) ([]db.SettingsTerminologyAssignment, error) {
	if err := q.DeleteSettingsTermAssignments(ctx, profileID); err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}

	for _, a := range assignments {
		if _, err := q.GetTermBase(ctx, db.GetTermBaseParams{ID: a.TermBaseID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, httputil.NewClientError(fmt.Sprintf("term base %d not found", a.TermBaseID))
			}
			return nil, fmt.Errorf("verify term base %d: %w", a.TermBaseID, err)
		}
		mode := a.Mode
		if mode == "" {
			mode = "read"
		}
		if _, err := q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
			SettingsProfileID: profileID, TermBaseID: a.TermBaseID,
			SourceLanguage: langcode.Normalize(a.SourceLanguage),
			TargetLanguage: langcode.Normalize(a.TargetLanguage),
			Mode:           mode, Priority: a.Priority,
		}); err != nil {
			return nil, fmt.Errorf("create: %w", err)
		}
	}

	return q.ListSettingsTermAssignments(ctx, profileID)
}

// ApplyTermAssignmentsTx is ApplyTermAssignments wrapped in a transaction.
func ApplyTermAssignmentsTx(ctx context.Context, pool database.TxBeginner, tenantID, profileID int64, assignments []TermAssignmentInput) ([]db.SettingsTerminologyAssignment, error) {
	return withTx(ctx, pool, func(q db.Querier) ([]db.SettingsTerminologyAssignment, error) {
		return ApplyTermAssignments(ctx, q, tenantID, profileID, assignments)
	})
}

// ClampPct bounds a user-supplied percentage to 0–100.
//
// Clamped rather than rejected: these arrive from number inputs and API
// clients, the columns carry CHECK constraints that would turn a stray value
// into a 500, and "a penalty of 150% is 100%" is the answer a user would give.
// The UI enforces the same range; this is the backend half.
func ClampPct(v int32) int32 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
