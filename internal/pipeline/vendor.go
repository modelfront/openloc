package pipeline

import (
	"errors"
	"net/http"

	"openloc/internal/ai"
	"openloc/internal/httputil"
)

// VendorOutageError is a vendor that failed to answer: unreachable, or an
// error status that is not the user's key, quota or rate limit. Message is the
// user's text. It is a 502, so the request still reaches error tracking: an
// outage can be our integration breaking.
type VendorOutageError struct {
	Message string
	Err     *ai.ProviderError
}

func (e *VendorOutageError) Error() string     { return e.Message }
func (e *VendorOutageError) Unwrap() error     { return e.Err }
func (e *VendorOutageError) StatusCode() int   { return http.StatusBadGateway }
func (e *VendorOutageError) ErrorCode() string { return "vendor_unavailable" }

// LogMessage implements logging.LogSafe: the vendor, kind and status only.
// Message stays out: it can carry an extension's text, which can quote the
// vendor's body, and this is what logs and error notices carry.
func (e *VendorOutageError) LogMessage() string { return "vendor outage: " + e.Err.LogMessage() }

// ExtensionFailure is a QE/APE extension failing in a way that is not the
// user's key, plan or rate: the extension refusing on its own, a crash, bad
// output, or the host failing to run it. Message is the user's text. It is a
// 502, so the request still reaches error tracking.
type ExtensionFailure struct {
	Message  string
	Provider string // the extension's AI vendor name, ext:<id>
	Err      error  // the host's error; nil when the extension itself failed
}

func (e *ExtensionFailure) Error() string     { return e.Message }
func (e *ExtensionFailure) Unwrap() error     { return e.Err }
func (e *ExtensionFailure) StatusCode() int   { return http.StatusBadGateway }
func (e *ExtensionFailure) ErrorCode() string { return "extension_failed" }

// LogMessage implements logging.LogSafe: the extension and the host's error,
// never the extension's own text, which can quote the vendor's body.
func (e *ExtensionFailure) LogMessage() string {
	if e.Err == nil {
		return "extension " + e.Provider + " failed"
	}
	return "extension " + e.Provider + " failed: " + e.Err.Error()
}

// ReportedFailureMessage returns the user's text of a vendor outage or an
// extension failure: failures worded for the user that still count as ours.
func ReportedFailureMessage(err error) (string, bool) {
	var outage *VendorOutageError
	if errors.As(err, &outage) {
		return outage.Message, true
	}
	var failure *ExtensionFailure
	if errors.As(err, &failure) {
		return failure.Message, true
	}
	return "", false
}

// vendorFailure turns a vendor's failure into what the user sees, worded by
// message: a ClientError when the user fixes it (key, quota, rate limit), a
// VendorOutageError otherwise. ok is false when err is not a vendor's failure.
func vendorFailure(err error, message func(*ai.ProviderError) string) (_ error, ok bool) {
	var pe *ai.ProviderError
	if !errors.As(err, &pe) {
		return err, false
	}
	if pe.UserFixable() {
		return httputil.NewClientError(message(pe)), true
	}
	return &VendorOutageError{Message: message(pe), Err: pe}, true
}
