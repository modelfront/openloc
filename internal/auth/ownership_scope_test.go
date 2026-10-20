package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// The ownership middlewares already fetch the row they check, so recording the
// surrounding entity IDs on the logging.Scope costs nothing. These
// tests pin that, because the failure mode is silent: drop the Attach call and
// everything still works, notices just go back to naming only the entity that
// happened to be in the URL.
//
// scopeQuerier embeds db.Querier as a nil interface and overrides only the two
// methods under test. Any other call panics, which is the point — it keeps the
// fake honest about what the middleware is allowed to touch, and needs no
// testcontainers Postgres for what is really a field-plumbing assertion.
type scopeQuerier struct {
	db.Querier

	jobRow db.GetJobOwnershipContextRow
	jobErr error

	file    db.File
	fileErr error

	gotJobParams  db.GetJobOwnershipContextParams
	gotFileParams db.GetFileTenantParams
}

func (q *scopeQuerier) GetJobOwnershipContext(_ context.Context, arg db.GetJobOwnershipContextParams) (db.GetJobOwnershipContextRow, error) {
	q.gotJobParams = arg
	return q.jobRow, q.jobErr
}

func (q *scopeQuerier) GetFileTenant(_ context.Context, arg db.GetFileTenantParams) (db.File, error) {
	q.gotFileParams = arg
	return q.file, q.fileErr
}

// serveWithScope runs mw over a request carrying claims and a fresh Scope, and
// returns the scope's fields plus the recorder.
func serveWithScope(t *testing.T, mw func(http.Handler) http.Handler, target string, urlParams map[string]string) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()

	req := httptest.NewRequest("POST", target, nil)
	ctx, scope := logging.WithScope(req.Context())
	ctx = WithClaims(ctx, &Claims{TenantID: 7, UserID: 9, Role: "owner"})
	req = req.WithContext(withURLParams(ctx, urlParams))

	res := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(res, req)
	return scope.Fields(), res
}

// errNotOurs stands in for the pgx.ErrNoRows a tenant-scoped query returns when
// the row exists but belongs to somebody else.
var errNotOurs = errors.New("no rows in result set")

// withURLParams installs a chi RouteContext, which is how httputil.ParseID reads
// {jobID} and {fileID}. The middlewares run under a chi router in production.
func withURLParams(ctx context.Context, params map[string]string) context.Context {
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	return context.WithValue(ctx, chi.RouteCtxKey, rc)
}

func TestJobOwnershipRecordsProjectAndFile(t *testing.T) {
	q := &scopeQuerier{jobRow: db.GetJobOwnershipContextRow{
		ID: 241, FileID: 88, ProjectID: 12, TargetLanguage: "fr-FR",
	}}

	fields, res := serveWithScope(t, RequireJobOwnership(q), "/api/jobs/241/mt-apply",
		map[string]string{"jobID": "241"})

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — ownership should have passed", res.Code)
	}
	// The ownership check itself must still be tenant-scoped.
	if q.gotJobParams.TenantID != 7 || q.gotJobParams.ID != 241 {
		t.Errorf("query params = %+v, want job 241 scoped to tenant 7", q.gotJobParams)
	}
	// /api/jobs/{jobID}/* names only the job. These are the fields that make a
	// notice actionable, and none of them are derivable from the URL.
	for key, want := range map[string]any{
		logging.KeyProjectID:  "12",
		logging.KeyFileID:     "88",
		logging.KeyTargetLang: "fr-FR",
	} {
		if got := fields[key]; got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
}

func TestFileOwnershipRecordsProject(t *testing.T) {
	q := &scopeQuerier{file: db.File{ID: 88, ProjectID: 12}}

	fields, res := serveWithScope(t, RequireFileOwnership(q), "/api/files/88/export",
		map[string]string{"fileID": "88"})

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got := fields[logging.KeyProjectID]; got != "12" {
		t.Errorf("project id = %#v, want \"12\"", got)
	}
}

// TestOwnershipFailureRecordsNothing — a 404 means the entity is not the
// caller's. Recording IDs read off a row we just refused to hand over would
// write another tenant's project ID into this tenant's log line.
func TestOwnershipFailureRecordsNothing(t *testing.T) {
	q := &scopeQuerier{jobErr: errNotOurs}

	fields, res := serveWithScope(t, RequireJobOwnership(q), "/api/jobs/241/mt-apply",
		map[string]string{"jobID": "241"})

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
	for _, key := range []string{logging.KeyProjectID, logging.KeyFileID, logging.KeyTargetLang} {
		if got, ok := fields[key]; ok {
			t.Errorf("%s = %#v, want absent — nothing is known about a job we rejected", key, got)
		}
	}
}
