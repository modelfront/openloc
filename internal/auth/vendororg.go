package auth

import (
	"net/http"
	"regexp"
	"slices"
	"strings"

	"openloc/internal/httputil"
)

// A vendor organization works for one client org and may do very little:
// manage its own members, see its tasks and the (empty) project list, read
// notifications, docs and the language list, keep personal settings and
// report issues. The
// barrier in checkTenantActive refuses everything else, on every credential
// path, so a route added later stays closed to vendor orgs until it joins
// vendorAllowed. The MCP server, the assistant and /api/v1 are closed.

// TenantGate is what the per-request tenant gate knows about the selected
// organization once it is approved.
type TenantGate struct {
	Kind string
	// ClientActive reports, for a vendor org, whether its client org is
	// approved. A vendor org works only while its client does.
	ClientActive bool
	// MemberDisabled reports, for a vendor org, that the vendor disabled the
	// caller's membership for disuse (vendoridle).
	MemberDisabled bool
}

type vendorRoute struct {
	methods []string // empty: any method
	path    *regexp.Regexp
}

func vendorRule(methods string, pattern string) vendorRoute {
	return vendorRoute{methods: strings.Fields(methods), path: regexp.MustCompile(pattern)}
}

// vendorAllowed is the whole surface of a vendor org. Path ids are numeric,
// as every handler behind them parses them.
var vendorAllowed = []vendorRoute{
	vendorRule("", `^/api/auth/`),
	vendorRule("GET", `^/api/members$`),
	vendorRule("GET", `^/api/members/page$`),
	vendorRule("GET", `^/api/members/facets$`),
	vendorRule("PUT", `^/api/members/\d+/role$`),
	vendorRule("DELETE", `^/api/members/\d+$`),
	vendorRule("POST", `^/api/members/\d+/enable$`),
	vendorRule("", `^/api/invites/?$`),
	vendorRule("GET", `^/api/invites/accepted$`),
	vendorRule("", `^/api/invites/\d+(/resend)?$`),
	vendorRule("GET PUT", `^/api/users/\d+/language-pairs$`),
	vendorRule("GET", `^/api/languages$`),
	vendorRule("GET", `^/api/tasks(/steps)?$`),
	vendorRule("GET", `^/api/projects/?$`),
	vendorRule("", `^/api/notifications(/[a-z-]+|/\d+/read)?/?$`),
	vendorRule("GET PUT", `^/api/notification-preferences$`),
	vendorRule("GET PUT", `^/api/appearance$`),
	vendorRule("GET", `^/api/(api-)?docs(/.*)?$`),
	vendorRule("", `^/api/issues(/count|/\d+(/comments|/confirm|/kind|/attachments/\d+)?)?/?$`),
}

// vendorRequestAllowed reports whether a vendor org may make the request. It
// reads the path chi routes on (the raw path when the request has one), so
// an encoded twin of an allowed path is refused rather than judged by its
// decoded form.
func vendorRequestAllowed(r *http.Request) bool {
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	for _, rule := range vendorAllowed {
		if rule.path.MatchString(path) && (len(rule.methods) == 0 || slices.Contains(rule.methods, r.Method)) {
			return true
		}
	}
	return false
}

// vendorGate answers a vendor org's request that may not go on and reports
// whether it did.
func vendorGate(w http.ResponseWriter, r *http.Request, g TenantGate) bool {
	if !g.ClientActive {
		httputil.ErrorCode(w, http.StatusForbidden, "client_inactive",
			"The organization this vendor works for is not active.")
		return true
	}
	if g.MemberDisabled {
		httputil.ErrorCode(w, http.StatusForbidden, "membership_disabled",
			"Your access to this organization is disabled. Ask its admin to enable it.")
		return true
	}
	if !vendorRequestAllowed(r) {
		httputil.ErrorCode(w, http.StatusForbidden, "vendor_org_restricted",
			"Vendor organizations can’t use this.")
		return true
	}
	return false
}
