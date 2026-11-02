package mt

import (
	"context"
	"fmt"
	"strings"

	"openloc/internal/ai"
)

// ProviderLibreTranslate names LibreTranslate in a *ai.ProviderError. DeepL and
// Google use their ai.Provider* names, since their keys live in AI settings;
// LibreTranslate's endpoint and key come from the server (LIBRETRANSLATE_URL).
const ProviderLibreTranslate = "libretranslate"

// maxVendorDetail caps the vendor text kept on an error.
const maxVendorDetail = 300

// vendorError classifies a vendor's non-2xx answer, so the pipeline can tell a
// problem the user fixes (key, quota, rate limit) from a vendor outage. Detail
// is the vendor's own text: Error() shows it, while LogMessage — what logs and
// errtrack notices carry — never does, because a vendor can echo the request,
// and the request is segment text.
func vendorError(provider string, status int, detail string) *ai.ProviderError {
	kind := ai.ProviderErrProvider
	switch status {
	case 401, 403:
		kind = ai.ProviderErrAuth
	case 429:
		kind = ai.ProviderErrRateLimit
	case 456: // DeepL: the account's character quota is used up
		kind = ai.ProviderErrQuota
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > maxVendorDetail {
		detail = detail[:maxVendorDetail] + "…"
	}
	return &ai.ProviderError{Provider: provider, Status: status, Kind: kind, Detail: detail}
}

// transportError is a request that got no answer. A canceled context (a user's
// Stop, a deleted job) keeps its cause in the chain, so the run reads as
// stopped; anything else is the vendor being unreachable. err must not carry
// a credential (Google's key rides the URL: pass it through scrubURLError).
func transportError(ctx context.Context, provider string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: request failed: %w", provider, err)
	}
	return &ai.ProviderError{Provider: provider, Kind: ai.ProviderErrUnreachable, Detail: err.Error()}
}

// serverVendorError is vendorError for a vendor the server provides, key and
// all: whatever it refuses, the user cannot fix it, so it is never user-fixable.
func serverVendorError(provider string, status int, detail string) *ai.ProviderError {
	pe := vendorError(provider, status, detail)
	pe.Kind = ai.ProviderErrProvider
	return pe
}
