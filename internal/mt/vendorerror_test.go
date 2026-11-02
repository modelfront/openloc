package mt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
)

// A vendor's answer is classified, so the pipeline can tell a problem the user
// fixes (key, quota, rate limit) from a vendor outage.
func TestVendorErrorKinds(t *testing.T) {
	engines := map[string]func(url string) Engine{
		ai.ProviderDeepL:       func(url string) Engine { return DeepL{APIKey: "k", BaseURL: url} },
		ai.ProviderGoogle:      func(url string) Engine { return Google{APIKey: "k", BaseURL: url} },
		ProviderLibreTranslate: func(url string) Engine { return LibreTranslate{APIKey: "k", APIURL: url} },
	}
	cases := []struct {
		vendor string
		status int
		want   string
	}{
		{ai.ProviderDeepL, 403, ai.ProviderErrAuth},
		{ai.ProviderDeepL, 456, ai.ProviderErrQuota},
		{ai.ProviderDeepL, 429, ai.ProviderErrRateLimit},
		{ai.ProviderDeepL, 503, ai.ProviderErrProvider},
		{ai.ProviderGoogle, 403, ai.ProviderErrAuth},
		{ai.ProviderGoogle, 429, ai.ProviderErrRateLimit},
		{ai.ProviderGoogle, 500, ai.ProviderErrProvider},
		// The server provides LibreTranslate's key: a refusal is never the
		// user's to fix.
		{ProviderLibreTranslate, 403, ai.ProviderErrProvider},
		{ProviderLibreTranslate, 429, ai.ProviderErrProvider},
		{ProviderLibreTranslate, 500, ai.ProviderErrProvider},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s %d", c.vendor, c.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				w.Write([]byte(`{"error":{"message":"vendor says no"}}`))
			}))
			defer srv.Close()

			_, err := engines[c.vendor](srv.URL).Translate(context.Background(), TranslateRequest{SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"}})
			var pe *ai.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("want *ai.ProviderError, got %T: %v", err, err)
			}
			if pe.Provider != c.vendor || pe.Status != c.status || pe.Kind != c.want {
				t.Errorf("got provider=%q status=%d kind=%q, want %q %d %q", pe.Provider, pe.Status, pe.Kind, c.vendor, c.status, c.want)
			}
		})
	}
}

// Google names the cause in the body, and its status alone misleads: an
// invalid key is a 400, a used-up daily quota a 403.
func TestGoogleErrorKindFromReason(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","errors":[{"reason":"badRequest"}],"details":[{"reason":"API_KEY_INVALID"}]}}`, ai.ProviderErrAuth},
		{400, `{"error":{"code":400,"message":"Bad key","errors":[{"reason":"keyInvalid"}]}}`, ai.ProviderErrAuth},
		{403, `{"error":{"code":403,"message":"Daily Limit Exceeded","errors":[{"reason":"dailyLimitExceeded"}]}}`, ai.ProviderErrQuota},
		{403, `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`, ai.ProviderErrQuota},
		{403, `{"error":{"code":403,"message":"User Rate Limit Exceeded","errors":[{"reason":"userRateLimitExceeded"}]}}`, ai.ProviderErrRateLimit},
		{400, `{"error":{"code":400,"message":"Invalid Value","errors":[{"reason":"invalid"}]}}`, ai.ProviderErrProvider},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		_, err := Google{APIKey: "k", BaseURL: srv.URL}.Translate(context.Background(), TranslateRequest{SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"}})
		srv.Close()
		var pe *ai.ProviderError
		if !errors.As(err, &pe) || pe.Kind != c.want {
			t.Errorf("%d %s: got %v, want kind %q", c.status, c.body, err, c.want)
		}
	}
}

func TestVendorUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens any more

	_, err := DeepL{APIKey: "k", BaseURL: url}.Translate(context.Background(), TranslateRequest{SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"}})
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Kind != ai.ProviderErrUnreachable {
		t.Fatalf("want an unreachable *ai.ProviderError, got %T: %v", err, err)
	}
}

// A user's Stop cancels the run's context mid-request; the error must still
// read as a cancellation, or the run is recorded as failed instead of stopped.
func TestVendorCanceledStaysCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := DeepL{APIKey: "k", BaseURL: srv.URL}.Translate(ctx, TranslateRequest{SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled in the chain, got %T: %v", err, err)
	}
}

// A vendor body can echo the segment text it was sent, and errtrack notices go
// to a third party, so the notice carries the status and never the body.
func TestVendorErrorBodyStaysOutOfErrtrackNotice(t *testing.T) {
	const marker = "SEGMENT-TEXT-MARKER"
	rec := errtracktest.Install(t)
	err := fmt.Errorf("translate batch: %w", vendorError(ai.ProviderDeepL, 400, "bad text "+marker))

	errtrack.Capture(context.Background(), err, errtrack.CaptureOptions{
		Class: errtrack.ClassWorkerTaskFail, Message: "task failed", Subsystem: "worker", Event: "translate",
	})

	msg := rec.One(t).ErrorMessage
	if strings.Contains(msg, marker) {
		t.Errorf("vendor body reached the notice: %q", msg)
	}
	if !strings.Contains(msg, "task failed: translate batch: deepl provider provider (status 400)") {
		t.Errorf("notice lost its context: %q", msg)
	}
}
