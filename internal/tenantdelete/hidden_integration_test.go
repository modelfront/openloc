package tenantdelete_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// An organization being deleted is gone for its members at once: it is not
// in the org picker, cannot be entered, its pending invites are not honoured,
// and /auth/me sends a member holding its token to the org picker instead of
// a waiting screen. Operators do not list it either. The member's other,
// live organization is untouched.
func TestIntegrationDeleteTenant_ClosedOrgIsHidden(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenantID, ownerID := seedOrg(t, pool, q, "closing", 0, 0, 0)
	live := testutil.SeedTenant(t, q, "Live Org")
	testutil.SeedMembership(t, q, ownerID, live.ID, "owner")
	invite, err := q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID: tenantID, Email: "invitee@test.com", Role: "linguist", InvitedBy: ownerID,
		Token: "closing-invite-token", ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkTenantDeleting(ctx, tenantID); err != nil {
		t.Fatal(err)
	}

	var me struct {
		Data struct {
			NeedsOrgSelection bool   `json:"needs_org_selection"`
			TenantID          *int64 `json:"tenant_id"`
			OrgPending        bool   `json:"org_pending"`
			Impersonating     bool   `json:"impersonating"`
		} `json:"data"`
	}
	rec := testutil.DoRequest(t, router, "GET", "/api/auth/me", nil, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &me)
	if !me.Data.NeedsOrgSelection || me.Data.TenantID != nil || me.Data.OrgPending {
		t.Errorf("/auth/me for a deleting org = %+v, want the org picker (needs_org_selection, no tenant)", me.Data)
	}

	// An operator viewing the org keeps the impersonation flag, so the app
	// still shows the way back.
	impToken, err := auth.GenerateImpersonationToken(ownerID, tenantID, "owner", testutil.TestJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/auth/me", nil, impToken)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &me)
	if !me.Data.Impersonating {
		t.Errorf("/auth/me for an operator viewing a deleting org lost the impersonation flag: %+v", me.Data)
	}

	var orgs struct {
		Data []struct {
			TenantID int64 `json:"tenant_id"`
		} `json:"data"`
	}
	rec = testutil.DoRequest(t, router, "GET", "/api/auth/orgs", nil, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &orgs)
	var picker []int64
	for _, o := range orgs.Data {
		picker = append(picker, o.TenantID)
	}
	if len(picker) != 1 || picker[0] != live.ID {
		t.Errorf("org picker = %v, want only the live org %d", picker, live.ID)
	}

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/auth/orgs/%d/select", tenantID), nil, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	listed, err := q.ListTenantsForPlatform(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var operator []int64
	for _, o := range listed {
		operator = append(operator, o.TenantID)
	}
	if fmt.Sprint(operator) != fmt.Sprint([]int64{live.ID}) {
		t.Errorf("operator list = %v, want only the live org %d", operator, live.ID)
	}

	if invites, err := q.GetPendingInvitesByEmail(ctx, invite.Email); err != nil || len(invites) != 0 {
		t.Errorf("pending invites of a deleting org = %v (%v), want none", invites, err)
	}
	if _, err := q.GetPendingUnexpiredInviteByToken(ctx, invite.Token); err == nil {
		t.Error("the invite link of a deleting org still resolves")
	}
}
