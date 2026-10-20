package publicapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// specOp is one operation of the spec.
type specOp struct {
	method, path string
	op           *openapi3.Operation
}

func specOps(t *testing.T) []specOp {
	t.Helper()
	doc := testutil.APISpec().Doc
	var out []specOp
	for _, path := range doc.Paths.InMatchingOrder() {
		for method, op := range doc.Paths.Value(path).Operations() {
			out = append(out, specOp{method: method, path: path, op: op})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].op.OperationID < out[j].op.OperationID })
	if len(out) < 70 {
		t.Fatalf("only %d operations in the spec", len(out))
	}
	return out
}

func (o specOp) name() string { return o.method + " " + o.path }

func (o specOp) roles() []string {
	raw, _ := o.op.Extensions["x-roles"].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		out = append(out, fmt.Sprint(r))
	}
	return out
}

// handlerChecked marks an operation whose handler narrows the route gate
// further (a live project's delete is owner/admin even though managers pass
// the route), so only the route gate's refusals are asserted for it.
func (o specOp) handlerChecked() bool {
	v, _ := o.op.Extensions["x-handler-check"].(bool)
	return v
}

// sampleBody is a valid body for each writing operation, so a refusal in the
// tests below comes from the gate under test and not from validation.
func sampleBody(f *fixture, operationID string) any {
	switch operationID {
	case "createProject":
		return map[string]any{"name": "Gate probe", "source_language": "en", "target_languages": []string{"de"}}
	case "updateProject":
		return map[string]any{"name": "Renamed"}
	case "assignJob":
		return map[string]any{"user_id": f.users["linguist"].ID}
	case "saveSegmentDraft":
		return map[string]any{"text": "Hallo Welt."}
	case "confirmSegment":
		return map[string]any{"text": "Hallo Welt."}
	case "addComment":
		return map[string]any{"content": "Looks fine."}
	case "createTM":
		return map[string]any{"name": "Probe memory", "source_language": "en", "target_languages": []string{"de"}}
	case "renameTM", "updateGlossary":
		return map[string]any{"name": "Renamed"}
	case "createGlossary":
		return map[string]any{"name": "Probe glossary", "source_language": "en"}
	case "createConcept":
		return map[string]any{"terms": []map[string]any{{"language": "en", "text": "hello"}, {"language": "de", "text": "hallo"}}}
	case "createWorkflow", "replaceWorkflow":
		return map[string]any{"name": "Probe workflow", "steps": []map[string]any{{"step_definition_id": f.ids["stepDefinitionID"]}}}
	case "createWorkflowStep", "replaceWorkflowStep":
		return map[string]any{"name": "Proofread", "type": "human"}
	case "createLabel":
		return map[string]any{"name": "Region", "values": []string{"EU"}}
	case "addLabelValue":
		return map[string]any{"value": "Low"}
	case "setProjectLabels":
		return map[string]any{"value_ids": []int64{f.ids["valueID"]}}
	case "changeMemberRole":
		return map[string]any{"role": "project_manager"}
	case "createInvite":
		return map[string]any{"email": fmt.Sprintf("probe-%d@example.com", f.tenant.ID), "role": "linguist"}
	case "createAIPrompt", "replaceAIPrompt":
		return map[string]any{"name": "Probe prompt", "mode": "qe", "prompt_text": "Check the translation."}
	case "createAIEngine", "replaceAIEngine":
		return map[string]any{"name": "Probe engine", "token_id": f.ids["tokenID"], "model": testutil.MockExtensionID}
	}
	return nil
}

// sampleUpload is the file each upload operation sends in its `file` field.
func sampleUpload(operationID string) (name, content string, ok bool) {
	switch operationID {
	case "uploadFile":
		return "probe.txt", "Probe sentence.\n", true
	case "importTMX":
		return "probe.tmx", sampleTMX, true
	case "importGlossary":
		return "probe.tbx", sampleTBX, true
	}
	return "", "", false
}

const sampleTBX = `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
  <text><body>
    <conceptEntry id="c1">
      <langSec xml:lang="en"><termSec><term>probe</term></termSec></langSec>
      <langSec xml:lang="de"><termSec><term>Sonde</term></termSec></langSec>
    </conceptEntry>
  </body></text>
</tbx>`

// call sends the operation as the holder of key, without spec validation:
// these probes are about the status, and the flow tests check the shapes.
func call(f *fixture, o specOp, key string) int {
	return send(f, o.method, f.path(o.path), o.op.OperationID, key)
}

// send is call for an explicit method and path, with operationID's sample
// body or upload.
func send(f *fixture, method, path, operationID, key string) int {
	body, contentType := "{}", "application/json"
	if b := sampleBody(f, operationID); b != nil {
		raw, err := json.Marshal(b)
		if err != nil {
			panic(err)
		}
		body = string(raw)
	}
	if name, content, ok := sampleUpload(operationID); ok {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", name)
		if err == nil {
			_, err = io.WriteString(fw, content)
		}
		if err == nil {
			err = mw.Close()
		}
		if err != nil {
			panic(err)
		}
		body, contentType = buf.String(), mw.FormDataContentType()
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+key)
	return serve(f.router, req).Code
}

// successCode is the operation's documented 2xx status.
func (o specOp) successCode() int {
	best := 0
	for code := range o.op.Responses.Map() {
		if n, err := strconv.Atoi(code); err == nil && n >= 200 && n < 300 && (best == 0 || n < best) {
			best = n
		}
	}
	if best == 0 {
		panic(o.name() + " documents no 2xx response")
	}
	return best
}

// prepare makes the fixture's items usable by role for operation o: a comment
// can only be deleted by its author, so deleteComment gets one of role's own.
func prepare(t *testing.T, f *fixture, o specOp, role string) {
	t.Helper()
	if o.op.OperationID != "deleteComment" {
		return
	}
	author := f.users[role].ID
	c, err := f.q.CreateComment(context.Background(), db.CreateCommentParams{TranslationID: f.ids["translationID"], UserID: &author, Content: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	f.ids["commentID"] = c.ID
}

// Every route mounted under /api/v1 is in the spec, and every operation in
// the spec is mounted: an undocumented endpoint or a documented ghost fails.
func TestIntegrationV1_SpecMatchesRoutes(t *testing.T) {
	_, router, cleanup := newRouter(t)
	defer cleanup()

	mounted := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		rest, ok := strings.CutPrefix(route, "/api/v1/")
		if !ok {
			return nil
		}
		path := "/" + strings.TrimSuffix(rest, "/")
		mounted[method+" "+path] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, o := range specOps(t) {
		documented[o.name()] = true
		if !mounted[o.name()] {
			t.Errorf("%s is in the spec but not mounted", o.name())
		}
	}
	for route := range mounted {
		if !documented[route] {
			t.Errorf("%s is mounted but not in the spec", route)
		}
	}
}

// A key from one organization finds nothing in another: every operation that
// names an item answers 404 for another organization's ids. The same request
// with the owner's own key then succeeds, so the 404 comes from the
// organization check and not from a bad id or body, and the attack changed
// nothing the owner relies on.
func TestIntegrationV1_OtherOrganizationIs404(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	attacker := newFixture(t, q, router, "Attacker")

	probed := 0
	for i, o := range specOps(t) {
		if !strings.Contains(o.path, "{") {
			continue
		}
		probed++
		// A fresh victim per operation: the control call may delete the item.
		victim := newFixture(t, q, router, fmt.Sprintf("Victim%d", i))
		prepare(t, victim, o, "owner")
		// A key per call: one key sending every probe would hit its rate limit.
		if code := call(victim, o, attacker.key(t, "owner")); code != http.StatusNotFound {
			t.Errorf("%s with another organization's ids = %d, want 404", o.name(), code)
		}
		if code := call(victim, o, victim.key(t, "owner")); code != o.successCode() {
			t.Errorf("%s with the organization's own key = %d, want %d", o.name(), code, o.successCode())
		}
	}
	if probed == 0 {
		t.Fatal("no operation with path parameters probed")
	}
}

// A list shows only the caller's organization: every list operation returns
// the victim's items to the victim and none of them to another organization.
func TestIntegrationV1_ListsShowOnlyOwnOrganization(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	victim := newFixture(t, q, router, "ListVictim")
	attacker := newFixture(t, q, router, "ListAttacker")

	ids := func(f *fixture, o specOp, key string) map[float64]bool {
		t.Helper()
		rec := serve(f.router, httpRequest(http.MethodGet, "/api/v1"+o.path, key))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", o.name(), rec.Code, rec.Body.String())
		}
		var body struct {
			Data []map[string]any `json:"data"`
		}
		decodeJSON(t, rec, &body)
		out := map[float64]bool{}
		for _, item := range body.Data {
			id, ok := item["id"].(float64)
			if !ok {
				t.Fatalf("%s: an item has no numeric id: %v", o.name(), item)
			}
			out[id] = true
		}
		return out
	}

	lists := 0
	for _, o := range specOps(t) {
		// Every GET without a path parameter is a list, except the spec itself.
		if o.method != http.MethodGet || strings.Contains(o.path, "{") || o.op.OperationID == "getOpenAPISpec" {
			continue
		}
		lists++
		own := ids(victim, o, victim.key(t, "owner"))
		if len(own) == 0 {
			t.Errorf("%s: the victim sees none of its own items, so the probe proves nothing", o.name())
			continue
		}
		for id := range ids(victim, o, attacker.key(t, "owner")) {
			if own[id] {
				t.Errorf("%s: another organization sees item %v", o.name(), id)
			}
		}
	}
	if lists < 10 {
		t.Fatalf("only %d list operations probed", lists)
	}
}

// A read-only key reads, and every other method is refused.
func TestIntegrationV1_ReadOnlyKeyCannotWrite(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "ReadOnly")

	reads, writes := 0, 0
	for _, o := range specOps(t) {
		code := call(f, o, f.readKey)
		if o.method == http.MethodGet {
			reads++
			if code != o.successCode() {
				t.Errorf("read-only key on %s = %d, want %d", o.name(), code, o.successCode())
			}
			continue
		}
		writes++
		req := httpRequest(o.method, f.path(o.path), f.readKey)
		rec := serve(router, req)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "read_only_key" {
			t.Errorf("read-only key on %s = %d %s, want 403 read_only_key", o.name(), rec.Code, rec.Body.String())
		}
	}
	if reads == 0 || writes == 0 {
		t.Fatalf("probed %d reading and %d writing operations; want both", reads, writes)
	}
}

// Each operation lets in exactly the roles its x-roles names: the others get
// 403, the named ones get the documented success status. An operation marked
// x-handler-check narrows further in the handler: on the fixture's live
// project only owner and admin pass it, so other named roles get 403 there.
func TestIntegrationV1_RolesMatchSpec(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()

	n := 0
	for _, o := range specOps(t) {
		allowed := o.roles()
		for _, role := range roles {
			n++
			f := newFixture(t, q, router, fmt.Sprintf("Roles%d", n))
			prepare(t, f, o, role)
			code := call(f, o, f.keys[role])
			want := o.successCode()
			if !slices.Contains(allowed, role) || (o.handlerChecked() && role != "owner" && role != "admin") {
				want = http.StatusForbidden
			}
			if code != want {
				t.Errorf("%s as %s = %d, want %d (x-roles %v)", o.name(), role, code, want, allowed)
			}
		}
	}
}

// appTwin maps each v1 operation to the app route it mirrors. Every v1
// operation must be listed (the spec itself is the only exception), so a new
// one cannot skip the parity check.
var appTwin = map[string]string{
	"listProjects":       "GET /api/projects",
	"createProject":      "POST /api/projects",
	"getProject":         "GET /api/projects/{projectID}",
	"updateProject":      "PATCH /api/projects/{projectID}",
	"deleteProject":      "DELETE /api/projects/{projectID}",
	"listProjectJobs":    "GET /api/projects/{projectID}/jobs",
	"listFiles":          "GET /api/projects/{projectID}/files",
	"uploadFile":         "POST /api/projects/{projectID}/files",
	"getFile":            "GET /api/projects/{projectID}/files/{fileID}",
	"deleteFile":         "DELETE /api/projects/{projectID}/files/{fileID}",
	"downloadSourceFile": "GET /api/files/{fileID}/download",
	"getJob":             "GET /api/jobs/{jobID}",
	"deleteJob":          "DELETE /api/jobs/{jobID}",
	"exportJob":          "GET /api/jobs/{jobID}/export",
	"assignJob":          "POST /api/jobs/{jobID}/assign",

	"listSegments":     "GET /api/jobs/{jobID}/translations",
	"saveSegmentDraft": "PUT /api/files/{fileID}/translations/{translationID}/draft",
	"confirmSegment":   "PUT /api/files/{fileID}/translations/{translationID}",
	"listComments":     "GET /api/translations/{translationID}/comments",
	"addComment":       "POST /api/translations/{translationID}/comments",
	"deleteComment":    "DELETE /api/translations/{translationID}/comments/{commentID}",

	"listTMs":       "GET /api/tms",
	"createTM":      "POST /api/tms",
	"getTM":         "GET /api/tms/{tmID}",
	"renameTM":      "PATCH /api/tms/{tmID}",
	"deleteTM":      "DELETE /api/tms/{tmID}",
	"listTMEntries": "GET /api/tms/{tmID}/entries",
	"importTMX":     "POST /api/tms/{tmID}/import",
	"listTMImports": "GET /api/tms/{tmID}/imports",
	"exportTMX":     "GET /api/tms/{tmID}/export",

	"listGlossaries": "GET /api/terminology",
	"createGlossary": "POST /api/terminology",
	"getGlossary":    "GET /api/terminology/{glossaryID}",
	"updateGlossary": "PATCH /api/terminology/{glossaryID}",
	"deleteGlossary": "DELETE /api/terminology/{glossaryID}",
	"listConcepts":   "GET /api/terminology/{glossaryID}/concepts",
	"createConcept":  "POST /api/terminology/{glossaryID}/concepts",
	"deleteConcept":  "DELETE /api/terminology/{glossaryID}/concepts/{conceptID}",
	"importGlossary": "POST /api/terminology/{glossaryID}/import-tbx",
	"exportGlossary": "GET /api/terminology/{glossaryID}/export-tbx",

	"listTemplates":     "GET /api/templates",
	"getTemplate":       "GET /api/templates/{templateID}",
	"duplicateTemplate": "POST /api/templates/{templateID}/duplicate",
	"deleteTemplate":    "DELETE /api/templates/{templateID}",

	"listWorkflows":       "GET /api/workflows",
	"createWorkflow":      "POST /api/workflows",
	"getWorkflow":         "GET /api/workflows/{workflowID}",
	"replaceWorkflow":     "PUT /api/workflows/{workflowID}",
	"deleteWorkflow":      "DELETE /api/workflows/{workflowID}",
	"listWorkflowSteps":   "GET /api/step-definitions",
	"createWorkflowStep":  "POST /api/step-definitions",
	"getWorkflowStep":     "GET /api/step-definitions/{stepDefinitionID}",
	"replaceWorkflowStep": "PUT /api/step-definitions/{stepDefinitionID}",
	"deleteWorkflowStep":  "DELETE /api/step-definitions/{stepDefinitionID}",

	"listLabels":        "GET /api/labels",
	"createLabel":       "POST /api/labels",
	"deleteLabel":       "DELETE /api/labels/{labelID}",
	"addLabelValue":     "POST /api/labels/{labelID}/values",
	"deleteLabelValue":  "DELETE /api/labels/{labelID}/values/{valueID}",
	"listProjectLabels": "GET /api/projects/{projectID}/labels",
	"setProjectLabels":  "PUT /api/projects/{projectID}/labels",

	"listMembers":      "GET /api/members",
	"changeMemberRole": "PUT /api/members/{userID}/role",
	"removeMember":     "DELETE /api/members/{userID}",
	"listInvites":      "GET /api/invites",
	"createInvite":     "POST /api/invites",
	"revokeInvite":     "DELETE /api/invites/{inviteID}",

	"listAIPrompts":   "GET /api/ai/prompts",
	"createAIPrompt":  "POST /api/ai/prompts",
	"replaceAIPrompt": "PATCH /api/ai/prompts/{promptID}",
	"deleteAIPrompt":  "DELETE /api/ai/prompts/{promptID}",
	"listAIEngines":   "GET /api/ai/engines",
	"createAIEngine":  "POST /api/ai/engines",
	"replaceAIEngine": "PATCH /api/ai/engines/{engineID}",
	"deleteAIEngine":  "DELETE /api/ai/engines/{engineID}",
}

// Every v1 route refuses exactly the roles its app twin refuses, so the two
// surfaces cannot drift apart when a gate changes on one of them.
func TestIntegrationV1_GatesMatchAppRoutes(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()

	n := 0
	for _, o := range specOps(t) {
		if o.op.OperationID == "getOpenAPISpec" {
			continue
		}
		twin, ok := appTwin[o.op.OperationID]
		if !ok {
			t.Errorf("%s (%s) has no app twin listed in appTwin", o.name(), o.op.OperationID)
			continue
		}
		method, appPath, _ := strings.Cut(twin, " ")
		for _, role := range roles {
			n++
			f := newFixture(t, q, router, fmt.Sprintf("Twin%d", n))
			prepare(t, f, o, role)
			appCode := send(f, method, f.fill(appPath), o.op.OperationID, f.keys[role])
			// A twin path that does not exist (401 from an unmatched route's
			// auth, 404, 405) would never return 403 and pass for any gate.
			if appCode == http.StatusUnauthorized || appCode == http.StatusNotFound || appCode == http.StatusMethodNotAllowed {
				t.Errorf("%s as %s: app twin %s = %d; the twin route is wrong", o.name(), role, twin, appCode)
				continue
			}
			v1Code := call(f, o, f.keys[role])
			if (appCode == http.StatusForbidden) != (v1Code == http.StatusForbidden) {
				t.Errorf("%s as %s: v1 = %d, app %s = %d — the gates differ", o.name(), role, v1Code, twin, appCode)
			}
		}
	}
}

// Each key has its own budget; a burst past it is refused with Retry-After,
// another key is unaffected, and a browser session is not limited.
func TestIntegrationV1_RateLimitPerKey(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Limits")
	const burst = 40

	limited := 0
	for i := 0; i < burst+20; i++ {
		rec := serve(router, httpRequest(http.MethodGet, "/api/v1/openapi.json", f.keys["owner"]))
		if i < burst && rec.Code != http.StatusOK {
			t.Fatalf("call %d inside the burst = %d", i+1, rec.Code)
		}
		if rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") == "" || errorCode(t, rec) != "rate_limited" {
				t.Fatalf("429 without Retry-After or code: %v %s", rec.Header(), rec.Body.String())
			}
		}
	}
	if limited == 0 {
		t.Fatal("no request was limited past the burst")
	}

	if rec := serve(router, httpRequest(http.MethodGet, "/api/v1/openapi.json", f.keys["admin"])); rec.Code != http.StatusOK {
		t.Fatalf("another key was throttled by the first: %d", rec.Code)
	}

	owner := f.users["owner"]
	for i := 0; i < burst+20; i++ {
		req := httpRequest(http.MethodGet, "/api/v1/openapi.json", "")
		req.Header.Set("Authorization", testutil.AuthHeaderWithRole(owner.ID, f.tenant.ID, "owner"))
		if rec := serve(router, req); rec.Code != http.StatusOK {
			t.Fatalf("browser session call %d = %d; sessions are not limited", i+1, rec.Code)
		}
	}
}
