package auth

import (
	"context"
	"net/http"

	"openloc/internal/httputil"
)

// patRequestAllowed holds an access key to its capability on the REST API.
// /mcp passes: the MCP server checks the capability per tool. Anything other
// than CapabilityWrite counts as read, so an unknown value never writes.
func patRequestAllowed(r *http.Request, info *PATInfo) bool {
	if r.URL.Path == "/mcp" || r.URL.Path == "/mcp/" {
		return true
	}
	if info != nil && info.Capability == CapabilityWrite {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// WriteReadOnlygsKey answers a read-only access key that tried to write.
func WriteReadOnlyKey(w http.ResponseWriter) {
	httputil.ErrorCode(w, http.StatusForbidden, "read_only_key", "This access key is read-only.")
}

// ReadOnlyKey reports whether the caller is an access key without write
// capability. A browser session is not a key and returns false.
func ReadOnlyKey(ctx context.Context) bool {
	info := PATInfoFromContext(ctx)
	return info != nil && info.Capability != CapabilityWrite
}
