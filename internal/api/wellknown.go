package api

import (
	"encoding/json"
	"net/http"
)

// openAIAppsChallenge serves OpenAI's domain-verification token at
// /.well-known/openai-apps-challenge: the exact token as plain text, nothing
// else. An unset token is a 404, so only a deployment that is being verified
// answers.
func openAIAppsChallenge(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(token))
	}
}

// microsoftIdentityAssociation serves /.well-known/microsoft-identity-association.json,
// which Microsoft reads to verify the publisher domain of an Entra app. An
// unset app ID (no Microsoft sign-in) is a 404.
func microsoftIdentityAssociation(appID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if appID == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"associatedApplications": []map[string]string{{"applicationId": appID}},
		})
	}
}
