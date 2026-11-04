package editor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

var errDB = errors.New("connection reset by peer")

// failingQueries finds the job, then fails every editor query.
type failingQueries struct{ db.Querier }

func (failingQueries) ListEditorScopeJobs(context.Context, db.ListEditorScopeJobsParams) ([]db.ListEditorScopeJobsRow, error) {
	return []db.ListEditorScopeJobsRow{{JobID: 1, FileID: 2, TargetLanguage: "de", ProjectID: 4}}, nil
}
func (failingQueries) ListEditorIndex(context.Context, db.ListEditorIndexParams) ([]db.ListEditorIndexRow, error) {
	return nil, errDB
}
func (failingQueries) ListEditorSegmentsByIDs(context.Context, db.ListEditorSegmentsByIDsParams) ([]db.Segment, error) {
	return nil, errDB
}
func (failingQueries) ListEditorTexts(context.Context, db.ListEditorTextsParams) ([]db.ListEditorTextsRow, error) {
	return nil, errDB
}
func (failingQueries) ListEditorRefSources(context.Context, db.ListEditorRefSourcesParams) ([]db.ListEditorRefSourcesRow, error) {
	return nil, errDB
}
func (failingQueries) CountEditorComments(context.Context, db.CountEditorCommentsParams) ([]db.CountEditorCommentsRow, error) {
	return nil, errDB
}
func (failingQueries) ListEditorWordCandidates(context.Context, db.ListEditorWordCandidatesParams) ([]db.ListEditorWordCandidatesRow, error) {
	return nil, errDB
}

// A failed query answers 500 and hands its cause to the request log, so the
// log says what failed.
func TestEditorQueryErrorsReachTheRequestLog(t *testing.T) {
	h := NewHandler(failingQueries{}, nil)
	for path, handle := range map[string]http.HandlerFunc{
		"index":              h.HandleIndex,
		"rows?segment_ids=1": h.HandleRows,
		"search?q=a":         h.HandleSearch,
		"refs":               h.HandleRefs,
		"word?word=a":        h.HandleWordCandidates,
		"comment-counts":     h.HandleCommentCounts,
		"fuzzy?segment_id=1": h.HandleFuzzy,
	} {
		t.Run(path, func(t *testing.T) {
			res := serveFailing(handle, path)
			if code := res.ResponseWriter.(*httptest.ResponseRecorder).Code; code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", code)
			}
			if res.Err() == nil || !strings.Contains(res.Err().Error(), "connection reset") {
				t.Errorf("recorded error = %v, want the query's error", res.Err())
			}
		})
	}
}

// laterQueriesFail answers each handler's first query, so the one after it
// fails: the translations of the rows found, the segments the references name.
type laterQueriesFail struct{ failingQueries }

func (laterQueriesFail) ListEditorSegmentsByIDs(context.Context, db.ListEditorSegmentsByIDsParams) ([]db.Segment, error) {
	return []db.Segment{{ID: 1, FileID: 2}}, nil
}
func (laterQueriesFail) ListEditorTranslationsBySegments(context.Context, db.ListEditorTranslationsBySegmentsParams) ([]db.Translation, error) {
	return nil, errDB
}
func (laterQueriesFail) ListEditorRefSources(context.Context, db.ListEditorRefSourcesParams) ([]db.ListEditorRefSourcesRow, error) {
	return []db.ListEditorRefSourcesRow{{FileID: 2, SourceRichText: `See <ph id="1" ref_key="k">x</ph>`}}, nil
}
func (laterQueriesFail) ListEditorSegmentsByKeys(context.Context, db.ListEditorSegmentsByKeysParams) ([]db.ListEditorSegmentsByKeysRow, error) {
	return nil, errDB
}

func TestEditorSecondQueryErrorsReachTheRequestLog(t *testing.T) {
	h := NewHandler(laterQueriesFail{}, nil)
	for path, handle := range map[string]http.HandlerFunc{
		"rows?segment_ids=1": h.HandleRows,
		"refs":               h.HandleRefs,
	} {
		t.Run(path, func(t *testing.T) {
			res := serveFailing(handle, path)
			if code := res.ResponseWriter.(*httptest.ResponseRecorder).Code; code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", code)
			}
			if res.Err() == nil || !strings.Contains(res.Err().Error(), "connection reset") {
				t.Errorf("recorded error = %v, want the query's error", res.Err())
			}
		})
	}
}

// serveFailing calls the handler for job 1 as a member of tenant 9.
func serveFailing(handle http.HandlerFunc, path string) *httputil.ErrorRecordingWriter {
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/1/editor/"+path, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("jobID", "1")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(auth.WithClaims(ctx, &auth.Claims{TenantID: 9, UserID: 3}))
	res := &httputil.ErrorRecordingWriter{ResponseWriter: httptest.NewRecorder()}
	handle(res, req)
	return res
}
