package flowtest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"openloc/internal/testutil"
)

// The user help is public: no session reads the pages every role reads. The
// API guides and the rest of the API stay behind sign-in, and a bad
// credential on the help routes is still a 401.
func TestIntegrationPublicDocs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	anon := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	t.Run("anonymous reads the help", func(t *testing.T) {
		rec := anon("/api/docs", nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var tree struct {
			Data []struct {
				Pages []struct{ Slug string } `json:"pages"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &tree)
		var slug string
		for _, s := range tree.Data {
			if len(s.Pages) > 0 {
				slug = s.Pages[0].Slug
				break
			}
		}
		if slug == "" {
			t.Fatalf("anonymous tree is empty: %s", rec.Body)
		}
		testutil.AssertStatus(t, anon("/api/docs/"+slug, nil), http.StatusOK)
		testutil.AssertStatus(t, anon("/api/docs/search?q=project", nil), http.StatusOK)
	})

	t.Run("anonymous reads the API guides and the spec", func(t *testing.T) {
		testutil.AssertStatus(t, anon("/api/api-docs", nil), http.StatusOK)
		rec := anon("/api/api-docs/openapi.json", nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var spec struct {
			OpenAPI string         `json:"openapi"`
			Paths   map[string]any `json:"paths"`
		}
		testutil.DecodeJSON(t, rec, &spec)
		if spec.OpenAPI == "" || len(spec.Paths) == 0 {
			t.Fatalf("public spec is not an OpenAPI document: %.200s", rec.Body)
		}
	})

	t.Run("the customer API and the app stay private", func(t *testing.T) {
		testutil.AssertStatus(t, anon("/api/v1/openapi.json", nil), http.StatusUnauthorized)
		testutil.AssertStatus(t, anon("/api/v1/projects", nil), http.StatusUnauthorized)
		testutil.AssertStatus(t, anon("/api/projects", nil), http.StatusUnauthorized)
	})

	t.Run("a bad credential is still rejected", func(t *testing.T) {
		testutil.AssertStatus(t, anon("/api/docs", map[string]string{"Authorization": "Bearer nope"}), http.StatusUnauthorized)
	})

	t.Run("a signed-in member still reads the help", func(t *testing.T) {
		tenant := testutil.SeedTenant(t, q, "PublicDocsCorp")
		ling := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/docs", nil, ling.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusOK)
	})
}
