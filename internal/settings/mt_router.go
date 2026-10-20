package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
)

// ParseNullableIDPatch reads a tri-state id field from a PATCH body: absent →
// (nil, false) keep; `null` or 0 → (nil, true) clear; N > 0 → (&N, true) set.
// Anything else is a client error naming the field.
func ParseNullableIDPatch(raw json.RawMessage, field string) (*int64, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, true, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n < 0 {
		return nil, false, fmt.Errorf("invalid %s (must be null, 0, or positive integer)", field)
	}
	if n == 0 {
		return nil, true, nil
	}
	return &n, true, nil
}

// MTRouterName resolves a profile's MT router pointer to a display name,
// including retired routers. nil when the profile has no router or the row
// cannot be read.
func MTRouterName(ctx context.Context, q db.Querier, tenantID int64, routerID *int64) *string {
	if routerID == nil {
		return nil
	}
	rt, err := q.GetMTRouterAny(ctx, db.GetMTRouterAnyParams{ID: *routerID, TenantID: tenantID})
	if err != nil {
		return nil
	}
	return &rt.Name
}
