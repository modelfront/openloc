package issues_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/testutil"
)

// An image placed in the description is stored as an attachment of the issue,
// and the description points at it by id, so both sides show it in place and
// open it through their own scoped route; another org cannot.
func TestIntegrationIssue_InlineImageInDescription(t *testing.T) {
	f := newFixture(t)
	fields := bugFields("Export does nothing")
	fields["description"] = "I click Export:\n![step one.png](upload:1)\nand nothing happens."
	issue := f.reportWith(t, fields, upload{"server.log", []byte("panic")}, png("step one.png"))

	if len(issue.Attachments) != 2 || !issue.Attachments[1].IsImage {
		t.Fatalf("attachments = %+v", issue.Attachments)
	}
	imageID := issue.Attachments[1].ID
	want := fmt.Sprintf("I click Export:\n![step one.png](attachment:%d)\nand nothing happens.", imageID)
	if issue.Description != want {
		t.Errorf("created description = %q, want %q", issue.Description, want)
	}
	if got := decodeDetail(t, f.get(t, issuePath(issue.ID, ""), f.reporter.ID, f.tenant.ID)).Issue.Description; got != want {
		t.Errorf("reporter's description = %q", got)
	}
	if got := f.platformGet(t, issue.ID).Issue.Description; got != want {
		t.Errorf("support team's description = %q", got)
	}

	image := "/attachments/" + itoa(imageID)
	if rec := f.get(t, issuePath(issue.ID, image), f.reporter.ID, f.tenant.ID); rec.Code != http.StatusOK {
		t.Errorf("reporter opens the image: %d", rec.Code)
	}
	if rec := f.asOperator(t, http.MethodGet, platformPath(issue.ID, image), nil); rec.Code != http.StatusOK {
		t.Errorf("support team opens the image: %d", rec.Code)
	}
	if rec := f.getAs(t, issuePath(issue.ID, image), f.outsider.ID, f.other.ID, "owner"); rec.Code != http.StatusNotFound {
		t.Errorf("another org opens the image: %d, want 404", rec.Code)
	}

	body := newestBody(t, f.outbox, operatorEmail)
	if !strings.Contains(body, "[image: step one.png]") || strings.Contains(body, "attachment:") {
		t.Errorf("new-issue email shows the reference, not [image: …]:\n%s", body)
	}
}

// A comment's image points at the comment's own file, on both sides.
func TestIntegrationIssue_InlineImageInComments(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export does nothing")

	d := decodeDetail(t, f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "Here: ![after.png](upload:0) see?"}, png("after.png")))
	c := d.Comments[len(d.Comments)-1]
	if len(c.Attachments) != 1 || c.Body == nil ||
		*c.Body != fmt.Sprintf("Here: ![after.png](attachment:%d) see?", c.Attachments[0].ID) {
		t.Fatalf("reporter comment = %+v", c)
	}
	if got := newestBody(t, f.outbox, operatorEmail); !strings.Contains(got, "Here: [image: after.png] see?") {
		t.Errorf("reply email:\n%s", got)
	}

	pd := decodeDetail(t, f.operatorMultipart(t, platformPath(issue.ID, "/comments"),
		map[string]string{"body": "Fixed: ![fixed.png](upload:0)"}, png("fixed.png")))
	oc := pd.Comments[len(pd.Comments)-1]
	if len(oc.Attachments) != 1 || *oc.Body != fmt.Sprintf("Fixed: ![fixed.png](attachment:%d)", oc.Attachments[0].ID) {
		t.Fatalf("operator comment = %+v", oc)
	}
	if got := newestBody(t, f.outbox, "rep@test.com"); !strings.Contains(got, "Fixed: [image: fixed.png]") {
		t.Errorf("reporter's update email:\n%s", got)
	}

	nd := f.operatorMultipart(t, platformPath(issue.ID, "/notes"),
		map[string]string{"body": "Repro: ![repro.png](upload:0)"}, png("repro.png"))
	if nd.Code != http.StatusCreated {
		t.Fatalf("note: %d %s", nd.Code, nd.Body.String())
	}
	note := f.platformGet(t, issue.ID).Comments
	n := note[len(note)-1]
	if !n.Internal || len(n.Attachments) != 1 || *n.Body != fmt.Sprintf("Repro: ![repro.png](attachment:%d)", n.Attachments[0].ID) {
		t.Errorf("internal note = %+v", n)
	}
}

// A reference to a file the request does not carry, or to a file that is not
// an image, is refused, and nothing is left in storage.
func TestIntegrationIssue_InlineImageRefusals(t *testing.T) {
	f := newFixture(t)
	for name, c := range map[string]struct {
		text  string
		files []upload
	}{
		"past the files": {"See ![x.png](upload:1)", []upload{png("x.png")}},
		"no files":       {"See ![x.png](upload:0)", nil},
		"not an image":   {"See ![log](upload:0)", []upload{{"server.log", []byte("panic: export failed")}}},
	} {
		fields := bugFields("Refused " + name)
		fields["description"] = c.text
		if rec := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, fields, c.files...); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: report %d, want 400", name, rec.Code)
		}
		if f.store.Len() != 0 {
			t.Errorf("%s: a refused report left %d stored files", name, f.store.Len())
		}
	}
	var page struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, f.get(t, "/api/issues?status=", f.reporter.ID, f.tenant.ID), &page)
	if len(page.Data) != 0 {
		t.Errorf("refused reports were saved: %d issues", len(page.Data))
	}
	issue := f.report(t, "Export does nothing")
	stored := f.store.Len()
	rec := f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "![page](upload:0)"}, upload{"page.html", []byte("<!doctype html><p>x</p>")})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("comment with a non-image inline: %d, want 400", rec.Code)
	}
	if f.store.Len() != stored {
		t.Errorf("stored files = %d, want %d: a refused comment left its file", f.store.Len(), stored)
	}
	if got := len(f.platformGet(t, issue.ID).Comments); got != 0 {
		t.Errorf("a refused comment was saved: %d entries", got)
	}
}

// A browser's form upload sends each line break as CRLF; the text is stored
// with plain line breaks, so an image reference on its own line reads the
// same from every client.
func TestIntegrationIssue_TextLineBreaksAreNormalized(t *testing.T) {
	f := newFixture(t)
	fields := bugFields("Line breaks")
	fields["description"] = "First line\r\n![a.png](upload:0)\r\nlast line"
	issue := f.reportWith(t, fields, png("a.png"))
	if want := fmt.Sprintf("First line\n![a.png](attachment:%d)\nlast line", issue.Attachments[0].ID); issue.Description != want {
		t.Errorf("description = %q, want %q", issue.Description, want)
	}
	d := decodeDetail(t, f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "one\r\ntwo"}))
	if got := *d.Comments[len(d.Comments)-1].Body; got != "one\ntwo" {
		t.Errorf("comment = %q, want %q", got, "one\ntwo")
	}
}
