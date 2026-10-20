package parser

import (
	"errors"
	"strings"
)

// importMessages maps the parse errors a user can fix to the message the
// upload shows. Messages are written here, never carry file content, and go
// back to the user with a 400. Order matters only where one sentinel wraps
// another, which none do.
var importMessages = []struct {
	err error
	msg string
}{
	{ErrPPTXLegacy, msgPPTXLegacy},
	{ErrPPTXEncrypted, "This presentation is password-protected. Remove the password in PowerPoint, save the file, and upload it again."},
	{ErrPPTXTooLarge, "This presentation is too large to translate: its slides and notes are more than 64 MB when unzipped, or hold too many shapes. Split it into smaller files and upload them separately."},
	{ErrPPTXMalformed, "This file is not a valid PowerPoint presentation (.pptx). Open it in PowerPoint, save it again, and upload it."},
	{ErrPPTXEmpty, "This presentation has no text to translate."},
	{ErrXLSXEncrypted, "This workbook is password-protected. Remove the password in Excel, save the file, and upload it again."},
	{ErrXLSXTooLarge, "This workbook is too large: it is more than 256 MB when unzipped."},
	{ErrXLSXMalformed, msgXLSXMalformed},
	{ErrXLSXOutOfGrid, msgXLSXMalformed},
	{ErrXLSXEmpty, "This workbook has no text to translate."},
	{ErrXLSXLegacy, msgXLSXLegacy},
}

const (
	msgPPTXLegacy    = "PowerPoint 97–2003 files (.ppt) are not supported. Open the file in PowerPoint, save it as .pptx, and upload it again."
	msgXLSXMalformed = "This file is not a valid Excel workbook (.xlsx). Open it in Excel, save it again, and upload it."
	msgXLSXLegacy    = "Excel 97–2003 files (.xls) are not supported. Open the file in Excel, save it as .xlsx, and upload it again."
)

// ImportMessage returns the message to show when Parse failed for a reason
// the user can fix. ok is false for every other error, which the upload
// answers with a generic message.
func ImportMessage(err error) (msg string, ok bool) {
	// Errors that carry their own message: a workbook, or the part of it a
	// layout chose, with no text, and a layout that does not fit the file.
	var noText *XLSXNoTextError
	if errors.As(err, &noText) {
		return noText.Msg, true
	}
	var layout *XLSXLayoutError
	if errors.As(err, &layout) {
		return layout.Msg, true
	}
	for _, m := range importMessages {
		if errors.Is(err, m.err) {
			return m.msg, true
		}
	}
	return "", false
}

// UnsupportedFormatMessage returns the message for a file whose extension no
// parser handles but that a user can convert, such as a .ppt or .xls. ok is
// false when there is no specific advice.
func UnsupportedFormatMessage(filename string) (msg string, ok bool) {
	switch lower := strings.ToLower(filename); {
	case strings.HasSuffix(lower, ".ppt"):
		return msgPPTXLegacy, true
	case strings.HasSuffix(lower, ".xls"):
		return msgXLSXLegacy, true
	}
	return "", false
}
