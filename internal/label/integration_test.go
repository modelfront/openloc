package label_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/testutil"
)

// labelResp is the shape every label read returns: the definition plus its
// values, so a caller never stitches two lists together.
type labelResp struct {
	Data struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Values []struct {
			ID    int64  `json:"id"`
			Value string `json:"value"`
		} `json:"values"`
	} `json:"data"`
}

type valueItem struct {
	ID           int64  `json:"id"`
	Value        string `json:"value"`
	InUse        bool   `json:"in_use"`
	ProjectCount int64  `json:"project_count"`
}

type labelItem struct {
	ID     int64       `json:"id"`
	Name   string      `json:"name"`
	InUse  bool        `json:"in_use"`
	Values []valueItem `json:"values"`
}

type labelListResp struct {
	Data []labelItem `json:"data"`
}

type projectLabelsResp struct {
	Data []struct {
		LabelDefinitionID int64  `json:"label_definition_id"`
		LabelValueID      int64  `json:"label_value_id"`
		LabelName         string `json:"label_name"`
		LabelValue        string `json:"label_value"`
	} `json:"data"`
}

// ownLabels drops Customer and Domain: every tenant is born with them,
// so a test about a tenant's own labels has to say so.
func ownLabels(list labelListResp) []labelItem {
	out := list.Data[:0:0]
	for _, l := range list.Data {
		if l.Name != "Customer" && l.Name != "Domain" {
			out = append(out, l)
		}
	}
	return out
}

func TestIntegrationLabelCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Tenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	// Create with values in one call — the transactional path.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
		"name": "Country", "values": []string{"Germany", "Japan"},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created labelResp
	testutil.DecodeJSON(t, rec, &created)
	if len(created.Data.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(created.Data.Values))
	}
	labelID := created.Data.ID
	germanyID := created.Data.Values[0].ID

	// Duplicate name is a 409, not a 500.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
		"name": "country", // case-insensitive: the unique index lowers both sides
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Adding a value it already has is a 409 too.
	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/labels/%d/values", labelID), map[string]any{
		"value": "germany",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// A blank name is refused before it reaches the database.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
		"name": "   ",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// A label's name is fixed once it exists — there is no rename route at all,
	// not a route that refuses. Asserted so re-adding one is a deliberate act.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/labels/%d", labelID), map[string]any{
		"name": "Market",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusMethodNotAllowed)

	// Attach the label to a project and read it back.
	project := testutil.SeedProject(t, q, tenant.ID, "Labelled project", "en", []string{"de"})
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/projects/%d/labels", project.ID), map[string]any{
		"labels": []map[string]int64{{"label_definition_id": labelID, "label_value_id": germanyID}},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var picks projectLabelsResp
	testutil.DecodeJSON(t, rec, &picks)
	if len(picks.Data) != 1 || picks.Data[0].LabelValue != "Germany" {
		t.Fatalf("expected one pick of Germany, got %+v", picks.Data)
	}

	// A template presets the same value, so deleting it has two kinds of pick
	// to clear.
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Labelled template")
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/templates/%d/labels", tmpl.ID), map[string]any{
		"labels": []map[string]int64{{"label_definition_id": labelID, "label_value_id": germanyID}},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// The list reports usage per value, with the project count the UI quotes.
	own := listOwn(t, router, owner.ID, tenant.ID)
	if len(own) != 1 || !own[0].InUse {
		t.Fatalf("label carried by a project should report in_use, got %+v", own)
	}
	germany, japan := valueByID(t, own[0], germanyID), otherValue(t, own[0], germanyID)
	if !germany.InUse || germany.ProjectCount != 1 {
		t.Errorf("the chosen value should report in_use with 1 project, got %+v", germany)
	}
	if japan.InUse || japan.ProjectCount != 0 {
		t.Errorf("a value nobody picked should not report in_use, got %+v", japan)
	}

	// The label cannot go while a project uses one of its values, and the 409
	// says why.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/labels/%d", labelID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "1 project is still using values of this label") {
		t.Errorf("refusal should name the project count, got %s", rec.Body.String())
	}

	// The value itself can be deleted while in use: the project and the
	// template lose the pick, rather than keeping a hidden reference.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/labels/%d/values/%d", labelID, germanyID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/projects/%d/labels", project.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &picks)
	if len(picks.Data) != 0 {
		t.Errorf("project should have lost the deleted value, got %+v", picks.Data)
	}
	if n := testutil.CountRows(t, pool, "SELECT count(*) FROM project_labels WHERE label_value_id = $1", germanyID); n != 0 {
		t.Errorf("project_labels should hold no row for the deleted value, got %d", n)
	}
	if n := testutil.CountRows(t, pool, "SELECT count(*) FROM template_labels WHERE label_value_id = $1", germanyID); n != 0 {
		t.Errorf("template_labels should hold no row for the deleted value, got %d", n)
	}
	own = listOwn(t, router, owner.ID, tenant.ID)
	if len(own) != 1 || len(own[0].Values) != 1 || own[0].InUse {
		t.Fatalf("expected the label with one unused value left, got %+v", own)
	}

	// Deleting it again is a no-op, not an error.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/labels/%d/values/%d", labelID, germanyID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Replace semantics: an empty list clears every pick rather than being a
	// no-op. This is what lets the form remove a row without its own DELETE.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/projects/%d/labels", project.ID), map[string]any{
		"labels": []map[string]int64{{"label_definition_id": labelID, "label_value_id": japan.ID}},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/projects/%d/labels", project.ID), map[string]any{
		"labels": []map[string]int64{},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &picks)
	if len(picks.Data) != 0 {
		t.Errorf("expected picks cleared, got %+v", picks.Data)
	}

	// A pick left on a deleted value (a project saving its picks while the
	// value is being deleted) is hidden everywhere, so it must not hold the
	// label either.
	if _, err := pool.Exec(context.Background(),
		"INSERT INTO project_labels (project_id, label_definition_id, label_value_id) VALUES ($1, $2, $3)",
		project.ID, labelID, germanyID); err != nil {
		t.Fatalf("insert stale pick: %v", err)
	}

	// With no live value in use, the label can be deleted, and it leaves the list.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/labels/%d", labelID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if own := listOwn(t, router, owner.ID, tenant.ID); len(own) != 0 {
		t.Errorf("expected no labels of its own after delete, got %+v", own)
	}
}

func TestIntegrationLabelCrossTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
		"name": "Country", "values": []string{"Germany"},
	}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created labelResp
	testutil.DecodeJSON(t, rec, &created)
	labelAID := created.Data.ID
	valueAID := created.Data.Values[0].ID

	// B cannot see A's labels.
	if own := listOwn(t, router, ownerB.ID, tenantB.ID); len(own) != 0 {
		t.Errorf("tenant B should see none of tenant A's labels, got %d", len(own))
	}

	// B cannot add a value to A's label.
	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/labels/%d/values", labelAID), map[string]any{
		"value": "Hijacked",
	}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// B's delete of A's label is a silent 204 — same reasoning as customers:
	// a 404 would confirm the id exists, a 403 would confirm the pattern.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/labels/%d", labelAID), nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if own := listOwn(t, router, ownerA.ID, tenantA.ID); len(own) != 1 {
		t.Errorf("tenant A's label should have survived tenant B's delete, got %+v", own)
	}

	// B cannot attach A's value to B's own project: the value is resolved
	// tenant-scoped, so a foreign id never reaches the insert.
	projectB := testutil.SeedProject(t, q, tenantB.ID, "B's project", "en", []string{"de"})
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/projects/%d/labels", projectB.ID), map[string]any{
		"labels": []map[string]int64{{"label_definition_id": labelAID, "label_value_id": valueAID}},
	}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// And B cannot write labels onto A's project at all.
	projectA := testutil.SeedProject(t, q, tenantA.ID, "A's project", "en", []string{"de"})
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/projects/%d/labels", projectA.ID), map[string]any{
		"labels": []map[string]int64{{"label_definition_id": labelAID, "label_value_id": valueAID}},
	}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// B's delete of A's value is a silent 204 that neither deletes it nor
	// unsets it on A's project.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/labels/%d/values/%d", labelAID, valueAID), nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/projects/%d/labels", projectA.ID), nil, ownerA.ID, tenantA.ID, "owner")
	var picksA projectLabelsResp
	testutil.DecodeJSON(t, rec, &picksA)
	if len(picksA.Data) != 1 || picksA.Data[0].LabelValueID != valueAID {
		t.Errorf("tenant A's pick should survive tenant B's value delete, got %+v", picksA.Data)
	}

	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/projects/%d/labels", projectA.ID), map[string]any{
		"labels": []map[string]int64{},
	}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Same label name in two tenants is fine.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
		"name": "Country",
	}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
}

// Another tenant's template id reads back no presets and cannot have its
// presets replaced: GET filters the picks by tenant, PUT looks the template up
// by tenant first. The owner's own reads and writes are the positive control.
func TestIntegrationTemplateLabelsCrossTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	victimOwner, victim := testutil.RegisterUser(t, q, "victim")
	attackerOwner, attacker := testutil.RegisterUser(t, q, "attacker")

	createLabel := func(userID, tenantID int64, name, value string) (labelID, valueID int64) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
			"name": name, "values": []string{value},
		}, userID, tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created labelResp
		testutil.DecodeJSON(t, rec, &created)
		return created.Data.ID, created.Data.Values[0].ID
	}
	victimLabel, victimValue := createLabel(victimOwner, victim, "Country", "Germany")
	attackerLabel, attackerValue := createLabel(attackerOwner, attacker, "Region", "North")

	tmpl := testutil.SeedTemplate(t, q, victim, "Victim template")
	path := fmt.Sprintf("/api/templates/%d/labels", tmpl.ID)
	rec := testutil.DoRequestWithRole(t, router, "PUT", path, map[string]any{
		"labels": []map[string]int64{{"label_definition_id": victimLabel, "label_value_id": victimValue}},
	}, victimOwner, victim, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	picks := func(userID, tenantID int64) projectLabelsResp {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, userID, tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var out projectLabelsResp
		testutil.DecodeJSON(t, rec, &out)
		return out
	}
	// victimPicks reads the stored rows, so it does not depend on the GET route.
	victimPicks := func() int64 {
		t.Helper()
		return testutil.CountRows(t, pool,
			"SELECT count(*) FROM template_labels WHERE template_id = $1 AND label_value_id = $2", tmpl.ID, victimValue)
	}

	if got := picks(attackerOwner, attacker); len(got.Data) != 0 {
		t.Errorf("attacker read the victim's template presets: %+v", got.Data)
	}
	if got := picks(victimOwner, victim); len(got.Data) != 1 || got.Data[0].LabelValue != "Germany" {
		t.Errorf("owner should read one Germany preset, got %+v", got.Data)
	}

	// An empty list would clear every preset; the attacker's own value would
	// add one.
	for _, labels := range [][]map[string]int64{
		{},
		{{"label_definition_id": attackerLabel, "label_value_id": attackerValue}},
	} {
		rec = testutil.DoRequestWithRole(t, router, "PUT", path, map[string]any{"labels": labels},
			attackerOwner, attacker, "owner")
		if rec.Code != http.StatusNotFound {
			t.Errorf("attacker PUT %v: got %d, want 404", labels, rec.Code)
		}
	}
	if n := victimPicks(); n != 1 {
		t.Errorf("attacker PUT removed the victim's preset (%d left)", n)
	}
	if n := testutil.CountRows(t, pool, "SELECT count(*) FROM template_labels WHERE template_id = $1", tmpl.ID); n != 1 {
		t.Errorf("victim's template holds %d presets after the attacker's PUT, want 1", n)
	}

	rec = testutil.DoRequestWithRole(t, router, "PUT", path, map[string]any{"labels": []map[string]int64{}},
		victimOwner, victim, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if n := victimPicks(); n != 0 {
		t.Errorf("owner's empty PUT should clear the preset, %d left", n)
	}
}

// A tenant with no labels of its own still has Customer and Domain: the trigger
// on `tenants` seeds them, so every creation path gets them — including the
// seeder and these fixtures, which never call the label API.
func TestIntegrationTenantIsBornWithSystemLabels(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Fresh")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "fresh@test.com", "pass", "owner")

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/labels", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list labelListResp
	testutil.DecodeJSON(t, rec, &list)

	if len(list.Data) != 2 {
		t.Fatalf("a new tenant should start with Customer and Domain, got %+v", list.Data)
	}
	// Built-ins sort first and are named for what they classify.
	if list.Data[0].Name != "Customer" || list.Data[1].Name != "Domain" {
		t.Errorf("want [Customer Domain] in that order, got %q and %q", list.Data[0].Name, list.Data[1].Name)
	}
	for _, l := range list.Data {
		if len(l.Values) != 0 {
			t.Errorf("%s should start with no values, got %+v", l.Name, l.Values)
		}
		// Undeletable, which is the point of marking them.
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/labels/%d", l.ID), nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
	}
}

// listOwn reads the label list and returns the tenant's own labels.
func listOwn(t *testing.T, router http.Handler, userID, tenantID int64) []labelItem {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/labels", nil, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list labelListResp
	testutil.DecodeJSON(t, rec, &list)
	return ownLabels(list)
}

func valueByID(t *testing.T, l labelItem, id int64) valueItem {
	t.Helper()
	for _, v := range l.Values {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("value %d not in label %+v", id, l)
	return valueItem{}
}

// otherValue returns the one value of a two-value label that is not id.
func otherValue(t *testing.T, l labelItem, id int64) valueItem {
	t.Helper()
	for _, v := range l.Values {
		if v.ID != id {
			return v
		}
	}
	t.Fatalf("label %+v has no value other than %d", l, id)
	return valueItem{}
}
