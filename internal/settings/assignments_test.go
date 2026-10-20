package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// mockQuerier records assignment operations for verification.
type mockQuerier struct {
	deletedTM   bool
	deletedTerm bool
	createdTM   []db.CreateSettingsTMAssignmentParams
	createdTerm []db.CreateSettingsTermAssignmentParams
	failCreate  bool // if true, create operations return error

	// aiEngines is the fake AI-engine catalog GetAIEngine resolves against
	// (keyed by id). Absent id → not found (mirrors tenant/ownership miss).
	aiEngines map[int64]db.AiEngine
	// tms / termBases mirror the tenant-scoped existence checks. nil map =
	// "everything exists" so tests that don't care stay terse; a non-nil map
	// makes absent ids return pgx.ErrNoRows (tenant/ownership miss).
	// termBases maps a term base id to its owning tenant id, so a base that
	// exists in another tenant is a miss too.
	tms       map[int64]bool
	termBases map[int64]int64
}

func (m *mockQuerier) GetTMCollection(_ context.Context, arg db.GetTMCollectionParams) (db.TmCollection, error) {
	if m.tms == nil || m.tms[arg.ID] {
		return db.TmCollection{ID: arg.ID, TenantID: arg.TenantID}, nil
	}
	return db.TmCollection{}, pgx.ErrNoRows
}

func (m *mockQuerier) GetTermBase(_ context.Context, arg db.GetTermBaseParams) (db.TermBasis, error) {
	if m.termBases == nil {
		return db.TermBasis{ID: arg.ID, TenantID: arg.TenantID}, nil
	}
	if owner, ok := m.termBases[arg.ID]; ok && owner == arg.TenantID {
		return db.TermBasis{ID: arg.ID, TenantID: owner}, nil
	}
	return db.TermBasis{}, pgx.ErrNoRows
}

func (m *mockQuerier) DeleteSettingsTMAssignments(_ context.Context, _ int64) error {
	m.deletedTM = true
	return nil
}
func (m *mockQuerier) DeleteSettingsTermAssignments(_ context.Context, _ int64) error {
	m.deletedTerm = true
	return nil
}

func (m *mockQuerier) CreateSettingsTMAssignment(_ context.Context, arg db.CreateSettingsTMAssignmentParams) (db.SettingsTmAssignment, error) {
	if m.failCreate {
		return db.SettingsTmAssignment{}, fmt.Errorf("db error")
	}
	m.createdTM = append(m.createdTM, arg)
	return db.SettingsTmAssignment{}, nil
}
func (m *mockQuerier) CreateSettingsTermAssignment(_ context.Context, arg db.CreateSettingsTermAssignmentParams) (db.SettingsTerminologyAssignment, error) {
	if m.failCreate {
		return db.SettingsTerminologyAssignment{}, fmt.Errorf("db error")
	}
	m.createdTerm = append(m.createdTerm, arg)
	return db.SettingsTerminologyAssignment{}, nil
}

func (m *mockQuerier) GetAIEngine(_ context.Context, arg db.GetAIEngineParams) (db.AiEngine, error) {
	if e, ok := m.aiEngines[arg.ID]; ok {
		return e, nil
	}
	return db.AiEngine{}, fmt.Errorf("ai engine %d not found", arg.ID)
}

func (m *mockQuerier) ListSettingsTMAssignments(_ context.Context, _ int64) ([]db.SettingsTmAssignment, error) {
	return []db.SettingsTmAssignment{}, nil
}
func (m *mockQuerier) ListSettingsTermAssignments(_ context.Context, _ int64) ([]db.SettingsTerminologyAssignment, error) {
	return []db.SettingsTerminologyAssignment{}, nil
}

var _ AssignmentQuerier = (*mockQuerier)(nil)

func makeRequest(body any) *http.Request {
	b, _ := json.Marshal(body)
	return httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(b))
}

func TestSetTMAssignments_DefaultMode(t *testing.T) {
	mq := &mockQuerier{}
	// TM assignments are per-TM (no source/target languages).
	req := makeRequest(map[string]any{
		"assignments": []map[string]any{
			{"tm_id": 1, "priority": 1},
		},
	})

	_, err := SetTMAssignments(context.Background(), mq, 1, 99, req)
	if err != nil {
		t.Fatal(err)
	}

	if !mq.deletedTM {
		t.Error("expected old TM assignments to be deleted")
	}
	if len(mq.createdTM) != 1 {
		t.Fatalf("expected 1 TM assignment created, got %d", len(mq.createdTM))
	}
	a := mq.createdTM[0]
	if a.Mode != "read_write" {
		t.Errorf("expected default mode 'read_write', got %q", a.Mode)
	}
	if a.TmID != 1 {
		t.Errorf("expected tm_id 1, got %d", a.TmID)
	}
	if a.SettingsProfileID != 99 {
		t.Errorf("expected profile ID 99, got %d", a.SettingsProfileID)
	}
}

func TestSetTMAssignments_DedupesByTM(t *testing.T) {
	// A full-replace with duplicate tm_ids must collapse to one row (the
	// UNIQUE(settings_profile_id, tm_id) constraint would otherwise be violated).
	mq := &mockQuerier{}
	req := makeRequest(map[string]any{
		"assignments": []map[string]any{
			{"tm_id": 7, "mode": "read_write", "priority": 1},
			{"tm_id": 7, "mode": "read", "priority": 2},
		},
	})
	if _, err := SetTMAssignments(context.Background(), mq, 1, 1, req); err != nil {
		t.Fatal(err)
	}
	if len(mq.createdTM) != 1 {
		t.Fatalf("expected duplicate tm_id collapsed to 1 assignment, got %d", len(mq.createdTM))
	}
}

func TestSetTMAssignments_ExplicitMode(t *testing.T) {
	mq := &mockQuerier{}
	req := makeRequest(map[string]any{
		"assignments": []map[string]any{
			{"tm_id": 1, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 2},
		},
	})

	_, err := SetTMAssignments(context.Background(), mq, 1, 10, req)
	if err != nil {
		t.Fatal(err)
	}
	if mq.createdTM[0].Mode != "read" {
		t.Errorf("expected explicit mode 'read', got %q", mq.createdTM[0].Mode)
	}
}

func TestSetTermAssignments_DefaultMode(t *testing.T) {
	mq := &mockQuerier{}
	req := makeRequest(map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": 5, "source_language": "EN-US", "target_language": "FR-FR", "priority": 1},
		},
	})

	_, err := SetTermAssignments(context.Background(), mq, 1, 10, req)
	if err != nil {
		t.Fatal(err)
	}

	if len(mq.createdTerm) != 1 {
		t.Fatalf("expected 1 term assignment, got %d", len(mq.createdTerm))
	}
	a := mq.createdTerm[0]
	if a.Mode != "read" {
		t.Errorf("expected default mode 'read', got %q", a.Mode)
	}
	if a.SourceLanguage != "en-us" || a.TargetLanguage != "fr-fr" {
		t.Errorf("expected normalized langs, got %s/%s", a.SourceLanguage, a.TargetLanguage)
	}
}

func TestSetTMAssignments_EmptyBody(t *testing.T) {
	mq := &mockQuerier{}
	req := makeRequest(map[string]any{
		"assignments": []map[string]any{},
	})

	_, err := SetTMAssignments(context.Background(), mq, 1, 10, req)
	if err != nil {
		t.Fatal(err)
	}

	if !mq.deletedTM {
		t.Error("expected old assignments to be deleted even with empty input")
	}
	if len(mq.createdTM) != 0 {
		t.Errorf("expected 0 assignments created, got %d", len(mq.createdTM))
	}
}

func TestSetTMAssignments_CreateError_Propagates(t *testing.T) {
	mq := &mockQuerier{failCreate: true}
	req := makeRequest(map[string]any{
		"assignments": []map[string]any{
			{"tm_id": 1, "source_language": "en", "target_language": "de", "priority": 1},
		},
	})

	_, err := SetTMAssignments(context.Background(), mq, 1, 10, req)
	if err == nil {
		t.Fatal("expected error when create fails")
	}
}

func TestSetTMAssignments_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte("not json")))
	mq := &mockQuerier{}

	_, err := SetTMAssignments(context.Background(), mq, 1, 10, req)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if mq.deletedTM {
		t.Error("should not delete assignments on decode error")
	}
}

// Tenant isolation: a tm_id/term_base_id outside the caller's tenant is
// rejected as a client error BEFORE any assignment row is written — the
// insert is otherwise a blind FK (never trust a client-supplied id).
func TestApplyTMAssignments_RejectsCrossTenantTM(t *testing.T) {
	mq := &mockQuerier{tms: map[int64]bool{1: true}} // tm 999 not in tenant
	_, err := ApplyTMAssignments(context.Background(), mq, 1, 10, []TMAssignmentInput{
		{TmID: 999, Mode: "read_write", Priority: 1},
	})
	if err == nil {
		t.Fatal("expected cross-tenant tm_id to be rejected")
	}
	if msg, ok := httputil.AsClientError(err); !ok || !strings.Contains(msg, "TM 999") {
		t.Fatalf("want client error naming the TM, got %v", err)
	}
	if len(mq.createdTM) != 0 {
		t.Fatalf("no assignment must be written, got %d", len(mq.createdTM))
	}
}

func TestApplyTermAssignments_RejectsCrossTenantTermBase(t *testing.T) {
	const callerTenant, otherTenant = 1, 2
	// 5 is the caller's, 777 exists but belongs to another tenant, 888 does not exist.
	termBases := map[int64]int64{5: callerTenant, 777: otherTenant}

	own := &mockQuerier{termBases: termBases}
	if _, err := ApplyTermAssignments(context.Background(), own, callerTenant, 10, []TermAssignmentInput{
		{TermBaseID: 5, SourceLanguage: "en", TargetLanguage: "de", Mode: "read", Priority: 1},
	}); err != nil {
		t.Fatalf("own term base: %v", err)
	}
	if len(own.createdTerm) != 1 {
		t.Fatalf("own term base: want 1 assignment written, got %d", len(own.createdTerm))
	}

	for _, id := range []int64{777, 888} {
		mq := &mockQuerier{termBases: termBases}
		_, err := ApplyTermAssignments(context.Background(), mq, callerTenant, 10, []TermAssignmentInput{
			{TermBaseID: id, SourceLanguage: "en", TargetLanguage: "de", Mode: "read", Priority: 1},
		})
		if err == nil {
			t.Fatalf("term base %d: expected to be rejected", id)
		}
		if msg, ok := httputil.AsClientError(err); !ok || !strings.Contains(msg, fmt.Sprintf("term base %d", id)) {
			t.Fatalf("term base %d: want client error naming the term base, got %v", id, err)
		}
		if len(mq.createdTerm) != 0 {
			t.Fatalf("term base %d: no assignment must be written, got %d", id, len(mq.createdTerm))
		}
	}
}
