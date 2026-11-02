package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/qa"
	"openloc/internal/translation"
)

// A tag the source does not have cannot be ignored, so the message tells the
// assistant to remove it rather than to ignore a finding that is not stored.
func TestServiceErrQABlock_TagNotInSourceIsNotIgnorable(t *testing.T) {
	tagOnly := serviceErr(&translation.QABlockedError{Issues: []qa.Issue{{
		CheckType: translation.SourceTagsCheckType, Severity: "error", Message: "Tag <b> is not in the source",
	}}}, "confirm").Error()
	if strings.Contains(tagOnly, "openloc_ignore_qa_finding") {
		t.Errorf("tag refusal suggests ignoring: %q", tagOnly)
	}
	if !strings.Contains(tagOnly, "Tag <b> is not in the source") || !strings.Contains(tagOnly, "Remove the tag") {
		t.Errorf("tag refusal message = %q, want the tag and an instruction to remove it", tagOnly)
	}

	profile := serviceErr(&translation.QABlockedError{Issues: []qa.Issue{{
		CheckType: "number_consistency", Severity: "error", Message: "Missing number(s) in target: 2026",
	}}}, "confirm").Error()
	if !strings.Contains(profile, "openloc_ignore_qa_finding") {
		t.Errorf("profile QA refusal lost the ignore hint: %q", profile)
	}
}

// A vendor outage keeps its worded message for the assistant, the way the
// REST answer does, instead of collapsing into a generic internal error.
func TestServiceErrVendorOutageKeepsItsMessage(t *testing.T) {
	const msg = "Machine translation failed: DeepL is having a problem right now. Try again in a few minutes."
	err := serviceErr(fmt.Errorf("apply mt: %w", &pipeline.VendorOutageError{
		Message: msg, Err: &ai.ProviderError{Provider: ai.ProviderDeepL, Status: 503, Kind: ai.ProviderErrProvider},
	}), "apply mt")
	var inv errInvalid
	if !errors.As(err, &inv) || inv.Error() != msg {
		t.Fatalf("err = %T %q, want errInvalid carrying %q", err, err, msg)
	}
}

// An extension failure is worded for the assistant too, and the tool call
// logs it at error level without the extension's own text: the REST answer is
// a 502 that is reported, so the MCP path must not go silent. The log sees
// what a tool returns, which is serviceErr's errInvalid.
func TestServiceErrExtensionFailureIsWordedAndLogged(t *testing.T) {
	const marker = "SEGMENT-TEXT-MARKER"
	failure := fmt.Errorf("rerun ai step: %w", &pipeline.ExtensionFailure{
		Message: "The AI step could not complete: QE: " + marker + ".", Provider: "ext:qe",
	})
	returned := serviceErr(failure, "rerun ai step")
	var inv errInvalid
	if !errors.As(returned, &inv) || !strings.Contains(inv.Error(), marker) {
		t.Fatalf("err = %T %q, want errInvalid carrying the extension's message", returned, returned)
	}

	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf})
	t.Cleanup(func() { logging.SetLogger(prev) })
	logReportedFailure(context.Background(), returned, "openloc_rerun_ai_step")
	logReportedFailure(context.Background(), serviceErr(httputil.NewClientError("No TM configured"), "apply tm"), "openloc_apply_tm")
	logs := buf.String()
	if strings.Count(logs, "mcp-tool-vendor-failed") != 1 || !strings.Contains(logs, `"log.level":"error"`) {
		t.Errorf("want one error-level mcp-tool-vendor-failed line, got %q", logs)
	}
	if strings.Contains(logs, marker) {
		t.Errorf("the extension's text reached the log: %q", logs)
	}
}
