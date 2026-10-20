package mt

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"openloc/internal/langcode"
	"openloc/internal/netguard"
)

// mtTimeout covers large chunks on a slow self-hosted LibreTranslate. Don't
// shorten it to catch hangs: those come from tag transfer, not the network.
const mtTimeout = 5 * time.Minute

// HTTPClient serves the engines whose base URL a tenant sets (DeepL, Google),
// so it refuses internal addresses outside development.
var HTTPClient = netguard.Client(mtTimeout)

// operatorClient serves LibreTranslate, whose URL comes from the deployment's
// own config and usually points at an internal host.
var operatorClient = &http.Client{Timeout: mtTimeout}

// maxLanguageListBytes caps a language-list response body. A real catalog is a
// few KB; the endpoint is tenant-settable, so an unbounded read would let a
// hostile or broken host exhaust memory on a request any manager can trigger.
const maxLanguageListBytes = 2 << 20 // 2 MiB

// maxLanguageEntries caps how many languages one fetched list may declare, and
// how many distinct targets a per-source catalog may accumulate.
//
// The endpoint is owner/admin-settable and the process is shared by every
// tenant on the replica, so the size of what a reply becomes has to be bounded
// by something other than the vendor's good manners. Real catalogs run ~40
// (DeepL) to ~190 (Google), so this refuses only a host lying about being one.
const maxLanguageEntries = 500

// maxPerSourceTargets bounds a whole per-source map, across every source in one
// catalog. The other bounds each cap a list on its own; none of them caps the
// product, and PerSource is the one field that grows with sources × targets. 500
// sources naming 500 targets each fit inside the body cap.
//
// 10,000 is a fully-connected 100-language instance. LibreTranslate publishes
// around 50, so this refuses only a host inventing a catalog. Refusing is the
// safe direction: the read falls back to the static table instead of caching
// something outsized.
const maxPerSourceTargets = 10_000

// maxLanguageCodeLen bounds one language code. BCP 47 tops out near 35
// characters, so anything longer is a host inventing input. langcode.Normalize
// checks neither length nor shape, and a code is held in both catalog lists and
// echoed by the languages endpoint.
const maxLanguageCodeLen = 64

// maxLanguageNameLen bounds a language's display name. A name is not a code, so
// it is truncated rather than refused — a vendor that pads its labels should not
// take the catalog down with it. Uncapped, 500 names of 4 KB fit inside the body
// cap and every cache entry holds them.
const maxLanguageNameLen = 128

// truncateName bounds a display name.
//
// strings.Clone, not a plain slice: a Go substring shares the original backing
// array, so name[:128] of a 2 MiB name keeps all 2 MiB alive for as long as the
// catalog is cached. The cap would bound what you can see and nothing else.
func truncateName(name string) string {
	if len(name) <= maxLanguageNameLen {
		return name
	}
	return strings.Clone(name[:maxLanguageNameLen])
}

// normalizeCode normalizes a code from an engine and rejects one too long to be
// a language code.
func normalizeCode(raw string) (string, bool) {
	code := langcode.Normalize(raw)
	if code == "" || len(code) > maxLanguageCodeLen {
		return "", false
	}
	return code, true
}

// readLanguageList reads a capped language-list body.
func readLanguageList(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxLanguageListBytes))
}

// scrubURLError strips the query string from a *url.Error's message.
//
// Both http.NewRequest and http.Client.Do wrap failures in a *url.Error whose
// text quotes the whole URL, and Google authenticates with a `key` query
// parameter — so without this the tenant's API key ends up in the log line for
// every timeout, DNS failure and refused connection. net/url redacts userinfo
// passwords on its own, never query parameters.
func scrubURLError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	clean := *uerr
	if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
		u.RawQuery = ""
		clean.URL = u.String()
	} else {
		// The URL is what failed to parse, so it cannot be split safely.
		clean.URL = "(redacted)"
	}
	return &clean
}
