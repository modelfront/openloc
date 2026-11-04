package invite_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type acceptedPage struct {
	Data []struct {
		ID            int64   `json:"id"`
		Email         string  `json:"email"`
		Role          string  `json:"role"`
		Kind          string  `json:"kind"`
		VendorOrgName *string `json:"vendor_org_name"`
	} `json:"data"`
	Cursor  int64 `json:"cursor"`
	HasMore bool  `json:"has_more"`
	Total   int64 `json:"total"`
}

// TestIntegrationAcceptedInvites: the Members tab's Accepted invites list,
// newest acceptance first; search on the email or the vendor name; pages; only
// accepted invites of this org; a project manager sees only linguist invites.
func TestIntegrationAcceptedInvites(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	org := testutil.SeedTenant(t, q, "Invite Org")
	owner := testutil.SeedUser(t, q, org.ID, "owner@invites.test", "pw")
	pm := testutil.SeedUserWithRole(t, q, org.ID, "pm@invites.test", "pw", "project_manager")

	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	accept := func(inv db.Invite, day int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE invites SET accepted_at = $1 WHERE id = $2`, base.AddDate(0, 0, day), inv.ID); err != nil {
			t.Fatalf("accept: %v", err)
		}
	}
	accept(seedInvite(t, q, org.ID, owner.ID, "ada@team.test", "admin"), 1)
	accept(seedInvite(t, q, org.ID, owner.ID, "bo@team.test", "linguist"), 3)
	accept(seedInvite(t, q, org.ID, owner.ID, "cy@team.test", "project_manager"), 2)
	vendorName := "Lingua Partners"
	token, _ := testutil.RandomToken()
	vendorInv, err := q.CreateInvite(ctx, db.CreateInviteParams{
		Kind: "vendor_org", VendorOrgName: &vendorName, TenantID: org.ID, Email: "boss@lingua.test",
		Role: "admin", InvitedBy: owner.ID, Token: token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("vendor invite: %v", err)
	}
	accept(vendorInv, 4)
	seedInvite(t, q, org.ID, owner.ID, "pending@team.test", "linguist") // not accepted
	other := testutil.SeedTenant(t, q, "Other Invite Org")
	otherOwner := testutil.SeedUser(t, q, other.ID, "owner@other-invites.test", "pw")
	accept(seedInvite(t, q, other.ID, otherOwner.ID, "ada@elsewhere.test", "linguist"), 5)

	get := func(userID int64, role string, params url.Values) acceptedPage {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/invites/accepted?"+params.Encode(), nil, userID, org.ID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var p acceptedPage
		testutil.DecodeJSON(t, rec, &p)
		return p
	}
	emails := func(p acceptedPage) string {
		out := []string{}
		for _, i := range p.Data {
			out = append(out, i.Email)
		}
		return strings.Join(out, ",")
	}

	all := get(owner.ID, "owner", url.Values{})
	if got := emails(all); got != "boss@lingua.test,bo@team.test,cy@team.test,ada@team.test" {
		t.Errorf("all = %s, want newest acceptance first, accepted and this org only", got)
	}
	if all.Total != 4 || all.HasMore {
		t.Errorf("all total=%d has_more=%v, want 4 and false", all.Total, all.HasMore)
	}
	if v := all.Data[0]; v.Kind != "vendor_org" || v.VendorOrgName == nil || *v.VendorOrgName != vendorName {
		t.Errorf("vendor row = %+v, want the vendor org invite with its name", v)
	}

	p1 := get(owner.ID, "owner", url.Values{"limit": {"3"}})
	if got := emails(p1); got != "boss@lingua.test,bo@team.test,cy@team.test" || !p1.HasMore || p1.Total != 4 {
		t.Errorf("page 1 = %s has_more=%v total=%d", got, p1.HasMore, p1.Total)
	}
	if got := emails(get(owner.ID, "owner", url.Values{"limit": {"3"}, "cursor": {strconv.FormatInt(p1.Cursor, 10)}})); got != "ada@team.test" {
		t.Errorf("page 2 = %s, want ada@team.test", got)
	}

	if got := emails(get(owner.ID, "owner", url.Values{"q": {"ADA@"}})); got != "ada@team.test" {
		t.Errorf("q=ADA@ = %s", got)
	}
	if got := emails(get(owner.ID, "owner", url.Values{"q": {"lingua part"}})); got != "boss@lingua.test" {
		t.Errorf("q=vendor name = %s", got)
	}

	// A project manager handles only linguist invites.
	if got := emails(get(pm.ID, "project_manager", url.Values{})); got != "bo@team.test" {
		t.Errorf("pm sees %s, want only the linguist invite", got)
	}
}
