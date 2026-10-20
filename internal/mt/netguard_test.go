package mt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Outside development DeepL and Google refuse a loopback endpoint: their base
// URL is tenant-set. LibreTranslate's URL comes from the operator's
// deployment config and stays reachable on the internal network.
func TestMTClientsGuardTenantEndpointsOutsideDev(t *testing.T) {
	t.Setenv("ENV", "production")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"code":"en","name":"English","targets":["de"]}]`))
	}))
	defer srv.Close()
	ctx := context.Background()
	req := TranslateRequest{SourceLang: "en", TargetLang: "de", Texts: []string{"hello"}}

	if _, err := (DeepL{APIKey: "k", BaseURL: srv.URL}).Translate(ctx, req); err == nil {
		t.Error("deepl: call to a loopback endpoint succeeded")
	}
	if _, err := (Google{APIKey: "k", BaseURL: srv.URL}).Translate(ctx, req); err == nil {
		t.Error("google: call to a loopback endpoint succeeded")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("loopback server received %d requests from tenant-set engines, want 0", n)
	}

	if _, err := (LibreTranslate{APIURL: srv.URL}).FetchLanguages(ctx); err != nil {
		t.Errorf("libretranslate: operator endpoint refused: %v", err)
	}
	if hits.Load() == 0 {
		t.Error("libretranslate never reached its operator-configured endpoint")
	}
}
