package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Outside development every vendor call refuses a loopback endpoint before
// any request reaches it: a tenant-set base URL must not reach internal
// services, directly or through a redirect.
func TestVendorClientsRefuseInternalAddressesOutsideDev(t *testing.T) {
	t.Setenv("ENV", "production")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ctx := context.Background()

	calls := map[string]func() error{
		"tester": func() error { return NewHTTPTester().Test(ctx, ProviderOpenAI, "sk-test", srv.URL) },
		"lister": func() error {
			_, err := NewHTTPModelLister().List(ctx, ProviderOpenAI, "sk-test", srv.URL)
			return err
		},
		"translator": func() error {
			_, err := NewHTTPTranslator().TranslateBatch(ctx, TranslateConfig{
				Provider: ProviderOpenAI, Model: "gpt-4o", APIKey: "sk-test", BaseURL: srv.URL,
				SourceLang: "en", TargetLang: "de",
			}, texts("hello"))
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: call to a loopback endpoint succeeded", name)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("loopback server received %d requests, want 0", n)
	}
}
