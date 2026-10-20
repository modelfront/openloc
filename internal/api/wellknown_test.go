package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIAppsChallenge(t *testing.T) {
	rec := httptest.NewRecorder()
	openAIAppsChallenge("tok_123")(rec, httptest.NewRequest("GET", "/.well-known/openai-apps-challenge", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "tok_123" {
		t.Fatalf("set token: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}

	rec = httptest.NewRecorder()
	openAIAppsChallenge("")(rec, httptest.NewRequest("GET", "/.well-known/openai-apps-challenge", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unset token: %d", rec.Code)
	}
}

func TestMicrosoftIdentityAssociation(t *testing.T) {
	rec := httptest.NewRecorder()
	microsoftIdentityAssociation("app-1")(rec, httptest.NewRequest("GET", "/.well-known/microsoft-identity-association.json", nil))
	want := `{"associatedApplications":[{"applicationId":"app-1"}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("set app id: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}

	rec = httptest.NewRecorder()
	microsoftIdentityAssociation("")(rec, httptest.NewRequest("GET", "/.well-known/microsoft-identity-association.json", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unset app id: %d", rec.Code)
	}
}
