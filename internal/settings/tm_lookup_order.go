package settings

import "openloc/internal/httputil"

// The two values of settings_profiles.tm_lookup_order.
const (
	// TMLookupOrderMatchPercent: an exact pass over every assigned TM, then a
	// fuzzy pass over every TM. An exact hit in a lower TM beats a fuzzy hit in
	// a higher one. The column default.
	TMLookupOrderMatchPercent = "match_percent"
	// TMLookupOrderTMOrder: each TM in turn gets its exact pass and then its
	// fuzzy pass before the next TM is asked, so a higher TM's fuzzy hit is
	// never replaced by a lower TM's exact one.
	TMLookupOrderTMOrder = "tm_order"
)

// ValidateTMLookupOrder returns a client error for anything but the two known
// values. Nil (field absent) is valid.
func ValidateTMLookupOrder(v *string) error {
	if v == nil || *v == TMLookupOrderMatchPercent || *v == TMLookupOrderTMOrder {
		return nil
	}
	return httputil.NewClientError("tm_lookup_order must be \"match_percent\" or \"tm_order\"")
}
