package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/ai"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

// verdictTester fails every key except "good-key", with no network call.
type verdictTester struct{}

func (verdictTester) Test(_ context.Context, _, apiKey, _ string) error {
	if apiKey == "good-key" {
		return nil
	}
	return errors.New("invalid API key (status 401)")
}

// errorField decodes the error body's field tag and message.
func errorField(t *testing.T, rec *httptest.ResponseRecorder) (field, message string) {
	t.Helper()
	var body struct {
		Error struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %v\n%s", err, rec.Body.String())
	}
	return body.Error.Field, body.Error.Message
}

// keyFailureRecords returns every decoded ai-key-test-failed record.
func keyFailureRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}
		if action, _ := rec["event.action"].(string); action == "ai-key-test-failed" {
			out = append(out, rec)
		}
	}
	return out
}

// A failed key check logs a masked key so support can tell what was pasted;
// a passing check logs nothing. Covers both create and update.
func TestIntegrationFailedKeyCheckLogsMaskedKey(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "KeyLogTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@keylog.test", "pass", "owner")
	tok := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "openai", "azure", "ENC(old)", "https://acme.openai.azure.com/openai")

	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatal(err)
	}
	h := ai.NewHandler(q, enc, verdictTester{}, nil)

	call := func(method string, fn http.HandlerFunc, params map[string]string, body string) *httptest.ResponseRecorder {
		t.Helper()
		rc := chi.NewRouteContext()
		for k, v := range params {
			rc.URLParams.Add(k, v)
		}
		ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rc)
		ctx = auth.WithClaims(ctx, &auth.Claims{TenantID: tenant.ID, UserID: owner.ID, Role: "owner"})
		req := httptest.NewRequest(method, "/", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()
		fn(rec, req)
		return rec
	}
	connID := strconv.FormatInt(tok.ConnectionID, 10)
	tokID := strconv.FormatInt(tok.ID, 10)

	// storedID names the token row the good key must land on.
	cases := []struct {
		name     string
		run      func(key string) *httptest.ResponseRecorder
		storedID func(t *testing.T, rec *httptest.ResponseRecorder) int64
	}{
		{"create", func(key string) *httptest.ResponseRecorder {
			return call("POST", h.HandleCreateToken, map[string]string{"provider": "openai"},
				`{"name":"k","api_key":"`+key+`","connection_id":`+connID+`}`)
		}, func(t *testing.T, rec *httptest.ResponseRecorder) int64 {
			t.Helper()
			var body struct {
				Data struct {
					ID int64 `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode created token: %v", err)
			}
			return body.Data.ID
		}},
		{"update", func(key string) *httptest.ResponseRecorder {
			return call("PATCH", h.HandleUpdateToken, map[string]string{"provider": "openai", "tokenID": tokID},
				`{"name":"edited","api_key":"`+key+`"}`)
		}, func(*testing.T, *httptest.ResponseRecorder) int64 { return tok.ID }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := testutil.CaptureLogs(t)
			const badKey = "sk-proj-FULL-bad-key-1234567890"
			rec := tc.run(badKey)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)

			recs := keyFailureRecords(t, buf)
			if len(recs) != 1 {
				t.Fatalf("want 1 ai-key-test-failed record, got %d\n%s", len(recs), buf.String())
			}
			got := recs[0]
			if strings.Contains(buf.String(), badKey) {
				t.Errorf("log carries the full key:\n%s", buf.String())
			}
			if want := "sk-p…7890 (31 chars)"; got["openloc.debug.api_key"] != want {
				t.Errorf("api_key = %v, want the masked key %q", got["openloc.debug.api_key"], want)
			}
			if got["openloc.provider"] != "openai" {
				t.Errorf("provider = %v, want openai", got["openloc.provider"])
			}
			if got["openloc.debug.base_url"] != "https://acme.openai.azure.com/openai" {
				t.Errorf("base_url = %v", got["openloc.debug.base_url"])
			}
			if got["openloc.debug.connection_id"] != float64(tok.ConnectionID) {
				t.Errorf("connection_id = %v, want %d", got["openloc.debug.connection_id"], tok.ConnectionID)
			}

			const wantReason = "Could not check this key with OpenAI. Check the endpoint address and try again."
			if field, msg := errorField(t, rec); field != "api_key" || msg != wantReason {
				t.Errorf("400 body: field=%q message=%q, want api_key / %q", field, msg, wantReason)
			}

			buf.Reset()
			rec = tc.run("good-key")
			if rec.Code >= 400 {
				t.Fatalf("good key: status %d: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(buf.String(), "good-key") {
				t.Errorf("a passing key check must not log the key:\n%s", buf.String())
			}

			stored, err := q.GetVendorToken(context.Background(), db.GetVendorTokenParams{
				ID: tc.storedID(t, rec), TenantID: tenant.ID,
			})
			if err != nil {
				t.Fatalf("reload token: %v", err)
			}
			if stored.KeyLast4 != "-key" {
				t.Errorf("key_last4 = %q, want %q from the new key", stored.KeyLast4, "-key")
			}
			plain, err := enc.Decrypt(stored.ApiKeyEncrypted)
			if err != nil {
				t.Fatalf("decrypt stored key: %v", err)
			}
			if plain != "good-key" {
				t.Errorf("stored key decrypts to %q, want the new key", plain)
			}
		})
	}

	// A taken name is the Name field's error, in the form's words.
	t.Run("duplicate name", func(t *testing.T) {
		const want = `A key named "k" already exists for OpenAI. Choose another name.`
		rec := call("POST", h.HandleCreateToken, map[string]string{"provider": "openai"},
			`{"name":"k","api_key":"good-key","connection_id":`+connID+`}`)
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if field, msg := errorField(t, rec); field != "name" || msg != want {
			t.Errorf("create: field=%q message=%q, want name / %q", field, msg, want)
		}
		rec = call("PATCH", h.HandleUpdateToken, map[string]string{"provider": "openai", "tokenID": tokID}, `{"name":"k"}`)
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if field, msg := errorField(t, rec); field != "name" || msg != want {
			t.Errorf("update: field=%q message=%q, want name / %q", field, msg, want)
		}
	})
}
