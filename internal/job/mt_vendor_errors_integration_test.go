package job_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

// An MT vendor that rejects the user's key, plan or rate is the user's to
// fix: MT preview and Apply MT answer 400 with a message that says so, and
// Honeybadger hears nothing. A vendor that is down may be our integration:
// 502, and the request is reported. So is any refusal from LibreTranslate,
// whose key the server provides, not the user.
func TestIntegrationMTVendorErrors(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	sealed, err := enc.Encrypt("deepl-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	cases := []struct {
		name       string
		engineType string
		vendor     int
		wantStatus int
		wantText   string
		wantNotice bool
	}{
		{"key rejected", "deepl", http.StatusForbidden, http.StatusBadRequest, "DeepL rejected the API key", false},
		{"quota used up", "deepl", 456, http.StatusBadRequest, "has no credit left", false},
		{"rate limited", "deepl", http.StatusTooManyRequests, http.StatusBadRequest, "is limiting requests", false},
		{"vendor down", "deepl", http.StatusServiceUnavailable, http.StatusBadGateway, "is having a problem right now", true},
		{"server-provided vendor refused", "libretranslate", http.StatusForbidden, http.StatusBadGateway, "tell your administrator", true},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/languages": // LibreTranslate
					fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]},{"code":"de","name":"German","targets":["en"]}]`)
					return
				case "/v2/languages": // DeepL
					fmt.Fprint(w, `[{"language":"EN","name":"English"},{"language":"DE","name":"German"}]`)
					return
				}
				w.WriteHeader(c.vendor)
				fmt.Fprint(w, `{"error":"vendor says no"}`)
			}))
			defer vendor.Close()

			tenant := testutil.SeedTenant(t, q, fmt.Sprintf("MTVendorErrCo%d", i))
			owner := testutil.SeedUserWithRole(t, q, tenant.ID, fmt.Sprintf("owner%d@mtvendorerr.test", i), "pass", "owner")
			project := testutil.SeedProject(t, q, tenant.ID, "Vendor errors", "en", []string{"de"})
			file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
			seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
			testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
			job := testutil.SeedJob(t, q, file.ID, "de")
			params := db.CreateMTEngineParams{Name: "Vendor", EngineType: c.engineType, Enabled: true, TenantID: tenant.ID}
			if c.engineType == "deepl" {
				tok := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "deepl", "DeepL key", sealed, vendor.URL)
				params.TokenID = &tok.ID
			} else {
				url := vendor.URL
				params.ApiUrl = &url
			}
			engine, err := q.CreateMTEngine(ctx, params)
			if err != nil {
				t.Fatalf("create engine: %v", err)
			}
			testutil.SeedMTRouterForProfile(t, q, tenant.ID, project.SettingsProfileID, &engine.ID, nil)

			requests := map[string]func() *httptest.ResponseRecorder{
				"preview": func() *httptest.ResponseRecorder {
					return testutil.DoRequestWithRole(t, router, "GET",
						fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=Hello%%20world", job.ID), nil, owner.ID, tenant.ID, "owner")
				},
				"apply": func() *httptest.ResponseRecorder {
					return testutil.DoRequestWithRole(t, router, "POST",
						fmt.Sprintf("/api/jobs/%d/mt-apply", job.ID), nil, owner.ID, tenant.ID, "owner")
				},
			}
			for _, name := range []string{"preview", "apply"} {
				notices := errtracktest.Install(t)
				rec := requests[name]()
				if rec.Code != c.wantStatus {
					t.Fatalf("%s: status %d, want %d (body %s)", name, rec.Code, c.wantStatus, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), c.wantText) {
					t.Errorf("%s: body should say %q, got %s", name, c.wantText, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "vendor says no") {
					t.Errorf("%s: the vendor's own text reached the client: %s", name, rec.Body.String())
				}
				got := 0
				for _, n := range notices.Notices() {
					if n.ErrorClass == errtrack.ClassHTTPServerError {
						got++
					}
				}
				if want := map[bool]int{true: 1, false: 0}[c.wantNotice]; got != want {
					t.Errorf("%s: %d HTTPServerError notices, want %d", name, got, want)
				}
			}
		})
	}
}
