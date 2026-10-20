package issues_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/notification"
	"openloc/internal/testutil"
)

// pngBytes is enough of a PNG for content sniffing.
var pngBytes = testutil.PNG("")

// upload is one file of a multipart request.
type upload struct {
	name string
	data []byte
}

func png(name string) upload { return upload{name, pngBytes} }

const operatorEmail = "ops@test.com"

// fixture: a reporter (linguist), a manager colleague and a linguist peer in
// one org, an outsider in another, and a platform operator who belongs to a
// third.
type fixture struct {
	router    chi.Router
	outbox    *notification.Outbox
	pool      *pgxpool.Pool
	q         db.Querier
	store     *file.MemoryStore
	tenant    db.Tenant
	reporter  db.User
	colleague db.User
	peer      db.User
	other     db.Tenant
	outsider  db.User
	opsTenant db.Tenant
	operator  db.User
}

// seekableOnly refuses a body it cannot seek, as the S3 client does when it
// signs the payload.
type seekableOnly struct{ *file.MemoryStore }

func (s seekableOnly) Put(ctx context.Context, key string, body io.Reader) error {
	if _, ok := body.(io.ReadSeeker); !ok {
		return errors.New("request stream is not seekable")
	}
	return s.MemoryStore.Put(ctx, key, body)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	store := file.NewMemoryStore()
	router, outbox := testutil.NewTestRouterWithIssues(pool, seekableOnly{store}, []string{operatorEmail})
	f := &fixture{router: router, outbox: outbox, pool: pool, q: q, store: store}
	f.tenant = testutil.SeedTenant(t, q, "Acme Org")
	f.reporter = testutil.SeedUserWithRole(t, q, f.tenant.ID, "rep@test.com", "pw", "linguist")
	f.colleague = testutil.SeedUserWithRole(t, q, f.tenant.ID, "col@test.com", "pw", "owner")
	f.peer = testutil.SeedUserWithRole(t, q, f.tenant.ID, "peer@test.com", "pw", "linguist")
	f.other = testutil.SeedTenant(t, q, "Other Org")
	f.outsider = testutil.SeedUserWithRole(t, q, f.other.ID, "out@test.com", "pw", "owner")
	f.opsTenant = testutil.SeedTenant(t, q, "Ops Org")
	f.operator = testutil.SeedUserWithRole(t, q, f.opsTenant.ID, operatorEmail, "pw", "owner")
	return f
}

// multipartBody builds a form with the given fields and files.
func multipartBody(t *testing.T, fields map[string]string, files ...upload) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		part, err := mw.CreateFormFile("files", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// roleOf is the fixture user's role in their org, which the token carries.
func (f *fixture) roleOf(userID int64) string {
	switch userID {
	case f.reporter.ID, f.peer.ID:
		return "linguist"
	default:
		return "owner"
	}
}

// asMember sends a multipart request as a member of tenantID, in their role.
func (f *fixture) asMember(t *testing.T, path string, userID, tenantID int64, fields map[string]string, files ...upload) *httptest.ResponseRecorder {
	t.Helper()
	return f.asMemberWithRole(t, path, userID, tenantID, f.roleOf(userID), fields, files...)
}

func (f *fixture) asMemberWithRole(t *testing.T, path string, userID, tenantID int64, role string, fields map[string]string, files ...upload) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, fields, files...)
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", testutil.AuthHeaderWithRole(userID, tenantID, role))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) get(t *testing.T, path string, userID, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	return f.getAs(t, path, userID, tenantID, f.roleOf(userID))
}

func (f *fixture) getAs(t *testing.T, path string, userID, tenantID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, http.MethodGet, path, nil, userID, tenantID, role)
}

// operatorToken is the operator's session in their own org.
func (f *fixture) operatorToken(t *testing.T) string {
	t.Helper()
	tok, err := auth.GenerateToken(f.operator.ID, f.opsTenant.ID, "owner", testutil.TestJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *fixture) asOperator(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRawToken(t, f.router, method, path, body, f.operatorToken(t))
}

func (f *fixture) operatorMultipart(t *testing.T, path string, fields map[string]string, files ...upload) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, fields, files...)
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+f.operatorToken(t))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// bugFields are the form fields of a valid bug report.
func bugFields(title string) map[string]string {
	return map[string]string{"kind": "bug", "severity": "major", "area": "editor",
		"title": title, "description": "The export button does nothing."}
}

// report files a bug as the reporter and returns it.
func (f *fixture) report(t *testing.T, title string, files ...upload) issues.IssueView {
	t.Helper()
	return f.reportWith(t, bugFields(title), files...)
}

// reportWith files a report with these form fields as the reporter and
// returns it.
func (f *fixture) reportWith(t *testing.T, fields map[string]string, files ...upload) issues.IssueView {
	t.Helper()
	rec := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, fields, files...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	var env struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &env)
	return env.Data
}

// platformGet is the support team's view of an issue.
func (f *fixture) platformGet(t *testing.T, id int64) issues.PlatformIssueDetail {
	t.Helper()
	return decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(id, ""), nil))
}

// orgRead is a member's read that must succeed and show want: an absence check
// on an error page would pass for the wrong reason.
func (f *fixture) orgRead(t *testing.T, path string, userID int64, want string) string {
	t.Helper()
	rec := f.get(t, path, userID, f.tenant.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("read %s: %d %s", path, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, want) {
		t.Fatalf("read %s does not show %q: %s", path, want, body)
	}
	return body
}

func decodeDetail(t *testing.T, rec *httptest.ResponseRecorder) issues.IssueDetail {
	t.Helper()
	var env struct{ Data issues.IssueDetail }
	testutil.DecodeJSON(t, rec, &env)
	return env.Data
}

// setStatus moves an issue to a status as the operator, with no comment.
func (f *fixture) setStatus(t *testing.T, id int64, status string) *httptest.ResponseRecorder {
	t.Helper()
	return f.asOperator(t, http.MethodPost, platformPath(id, "/status"), map[string]string{"status": status})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func issuePath(id int64, rest string) string { return fmt.Sprintf("/api/issues/%d%s", id, rest) }

func platformPath(id int64, rest string) string {
	return fmt.Sprintf("/api/platform/issues/%d%s", id, rest)
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("error body %s: %v", rec.Body.String(), err)
	}
	return env.Error.Code
}

// subjects lists the subjects of the emails sent to an address.
func subjects(o *notification.Outbox, to string) []string {
	var out []string
	for _, e := range o.For(to) {
		out = append(out, e.Subject)
	}
	return out
}
