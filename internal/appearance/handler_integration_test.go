// Per-user appearance: defaults, upsert roundtrip, cross-user isolation (the
// setting is keyed by user, not tenant), and backend validation of custom
// palettes. Any authenticated role may read/write their own appearance.
package appearance_test

import (
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

func TestIntegrationAppearance(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Tenant")
	userA := testutil.SeedUserWithRole(t, q, tenant.ID, "a@test.com", "pass", "linguist")
	userB := testutil.SeedUserWithRole(t, q, tenant.ID, "b@test.com", "pass", "linguist")

	type resp struct {
		Data struct {
			LightPalette   string `json:"light_palette"`
			DarkPalette    string `json:"dark_palette"`
			CustomPalettes []struct {
				ID      string            `json:"id"`
				Name    string            `json:"name"`
				Mode    string            `json:"mode"`
				Anchors map[string]string `json:"anchors"`
			} `json:"custom_palettes"`
		} `json:"data"`
	}

	// New user → built-in defaults, empty customs. A linguist (no manager role)
	// can read their own appearance.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/appearance", nil, userA.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got resp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.LightPalette != "paper" || got.Data.DarkPalette != "dim" {
		t.Fatalf("expected defaults, got %+v", got.Data)
	}
	if len(got.Data.CustomPalettes) != 0 {
		t.Fatalf("expected no custom palettes, got %d", len(got.Data.CustomPalettes))
	}

	// Upsert: switch dark palette + add one custom palette.
	anchors := map[string]string{
		"bg": "#101014", "surface": "#17171c", "elevated": "#22222a", "border": "#33333c",
		"text": "#f5f5f7", "muted": "#a0a0aa", "accent": "#7c5cff",
	}
	body := map[string]any{
		"light_palette": "light-default",
		"dark_palette":  "custom-mine",
		"custom_palettes": []map[string]any{
			{"id": "custom-mine", "name": "Mine", "mode": "dark", "anchors": anchors},
		},
	}
	rec = testutil.DoRequestWithRole(t, router, "PUT", "/api/appearance", body, userA.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Read back — persisted for user A.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/appearance", nil, userA.ID, tenant.ID, "linguist")
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.DarkPalette != "custom-mine" || len(got.Data.CustomPalettes) != 1 {
		t.Fatalf("upsert not persisted: %+v", got.Data)
	}
	if got.Data.CustomPalettes[0].Anchors["accent"] != "#7c5cff" {
		t.Errorf("custom anchors not roundtripped: %+v", got.Data.CustomPalettes[0])
	}

	// Isolation: user B still sees defaults (appearance is per-user).
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/appearance", nil, userB.ID, tenant.ID, "linguist")
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.DarkPalette != "dim" || len(got.Data.CustomPalettes) != 0 {
		t.Errorf("user B leaked user A's appearance: %+v", got.Data)
	}

	// Validation: bad hex, bad mode, empty palette id → 400 (nothing persisted).
	bad := []map[string]any{
		{"light_palette": "", "dark_palette": "black-true", "custom_palettes": []any{}},
		{"light_palette": "light-default", "dark_palette": "x", "custom_palettes": []map[string]any{
			{"id": "c1", "name": "Bad hex", "mode": "dark", "anchors": map[string]string{
				"bg": "not-hex", "surface": "#17171c", "elevated": "#22222a", "border": "#33333c",
				"text": "#f5f5f7", "muted": "#a0a0aa", "accent": "#7c5cff"}}}},
		{"light_palette": "light-default", "dark_palette": "x", "custom_palettes": []map[string]any{
			{"id": "c2", "name": "Bad mode", "mode": "purple", "anchors": anchors}}},
	}
	for _, b := range bad {
		rec = testutil.DoRequestWithRole(t, router, "PUT", "/api/appearance", b, userA.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	}

	// The rejected writes left user A's saved appearance untouched.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/appearance", nil, userA.ID, tenant.ID, "linguist")
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.LightPalette != "light-default" || got.Data.DarkPalette != "custom-mine" || len(got.Data.CustomPalettes) != 1 {
		t.Errorf("rejected PUT changed the saved appearance: %+v", got.Data)
	}
}
