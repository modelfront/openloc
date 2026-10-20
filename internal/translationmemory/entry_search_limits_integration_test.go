package translationmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/translationmemory"
)

func seedLimitTM(t *testing.T) (*translationmemory.Service, int64) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	tenant := testutil.SeedTenant(t, q, "LimitCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "Limit TM")
	if _, err := q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello", TargetText: "Bonjour", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	return translationmemory.NewService(q, pool, pool), tm.ID
}

func TestIntegrationListEntries_SearchOverTimeLimitIsAValidationError(t *testing.T) {
	s, tmID := seedLimitTM(t)
	translationmemory.SetSearchTimeouts(s, time.Nanosecond, time.Nanosecond)

	_, err := s.ListEntries(context.Background(), tmID, translationmemory.EntryFilter{Query: "hel+o", Regex: true})
	var verr translationmemory.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("a search past the time limit must be a ValidationError (400), got %v", err)
	}
	if verr.Msg != "This search took too long. Choose a language first to search fewer entries." {
		t.Errorf("unexpected message %q", verr.Msg)
	}
}

func TestIntegrationListEntries_CountOverTimeLimitStillReturnsThePage(t *testing.T) {
	s, tmID := seedLimitTM(t)
	translationmemory.SetSearchTimeouts(s, time.Nanosecond, 5*time.Second)

	page, err := s.ListEntries(context.Background(), tmID, translationmemory.EntryFilter{Query: "hello"})
	if err != nil {
		t.Fatalf("a count past its limit must not fail the search, got %v", err)
	}
	if len(page.Matches) != 1 || page.Total != translationmemory.UnknownTotal {
		t.Errorf("got %d matches, total %d; want the match and an unknown total", len(page.Matches), page.Total)
	}
}

func TestIntegrationListEntries_LaterPagesSkipTheCount(t *testing.T) {
	s, tmID := seedLimitTM(t)
	page, err := s.ListEntries(context.Background(), tmID, translationmemory.EntryFilter{Query: "hello", Cursor: 1})
	if err != nil || page.Total != translationmemory.UnknownTotal {
		t.Fatalf("got total %d, err %v; want an unknown total, nil", page.Total, err)
	}
}

func TestIntegrationListEntries_CallerCancelIsNotAValidationError(t *testing.T) {
	s, tmID := seedLimitTM(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.ListEntries(ctx, tmID, translationmemory.EntryFilter{Query: "hello"})
	var verr translationmemory.ValidationError
	if err == nil || errors.As(err, &verr) {
		t.Fatalf("a request the caller cancelled is not the user's mistake, got %v", err)
	}
}

// The caller's own deadline running out is not the search's time limit: the
// user did nothing wrong, so it must not come back as a 400.
func TestIntegrationListEntries_CallerDeadlineIsNotAValidationError(t *testing.T) {
	s, tmID := seedLimitTM(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	_, err := s.ListEntries(ctx, tmID, translationmemory.EntryFilter{Query: "hello"})
	var verr translationmemory.ValidationError
	if err == nil || errors.As(err, &verr) {
		t.Fatalf("an expired caller deadline is not the user's mistake, got %v", err)
	}
}

func TestIntegrationListEntries_SearchWithinTimeLimitSucceeds(t *testing.T) {
	s, tmID := seedLimitTM(t)
	page, err := s.ListEntries(context.Background(), tmID, translationmemory.EntryFilter{Query: "hel+o", Regex: true})
	if err != nil || page.Total != 1 {
		t.Fatalf("got total %d, err %v; want 1, nil", page.Total, err)
	}
}
