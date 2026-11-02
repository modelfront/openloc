package settings

import "openloc/internal/httputil"

// The values of settings_profiles.linguist_terms: whether linguists may add
// terms from the CAT editor, and the status those terms get.
const (
	// LinguistTermsOff: only managers add terms from the editor.
	LinguistTermsOff = "off"
	// LinguistTermsPending: a linguist's term waits for a project manager.
	// The column default.
	LinguistTermsPending = "pending"
	// LinguistTermsApproved: a linguist's term is approved at once.
	LinguistTermsApproved = "approved"
)

// ValidateLinguistTerms returns a client error for anything but the three
// known values. Nil (field absent) is valid.
func ValidateLinguistTerms(v *string) error {
	if v == nil || *v == LinguistTermsOff || *v == LinguistTermsPending || *v == LinguistTermsApproved {
		return nil
	}
	return httputil.NewClientError("linguist_terms must be \"off\", \"pending\" or \"approved\"")
}
