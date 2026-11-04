package tenantdelete_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/tenantdelete"
	"openloc/internal/testutil"
)

// seedVendorOrg is a vendor org of clientID with an admin, an invite, an MT
// engine, and a project of files files, a TM and a term base like seedOrg's.
func seedVendorOrg(t *testing.T, pool *pgxpool.Pool, q db.Querier, clientID int64, name string, files int) int64 {
	t.Helper()
	ctx := context.Background()
	vendor := testutil.SeedVendorTenant(t, q, clientID, name)
	admin := testutil.SeedUserWithRole(t, q, vendor.ID, name+"-admin@vendor.example.com", "pass", "admin")
	if _, err := q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID: vendor.ID, Email: name + "-new@vendor.example.com", Role: "linguist", InvitedBy: admin.ID,
		Token: name + "-invite", Kind: auth.InviteKindMember,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	testutil.SeedMTEngine(t, q, vendor.ID, name+" MT", "deepl")
	project := testutil.SeedProject(t, q, vendor.ID, name+" project", "en", []string{"de"})
	for i := 0; i < files; i++ {
		file := testutil.SeedFile(t, q, project.ID, fmt.Sprintf("%s-%d.txt", name, i), db.FileFormatTxt)
		testutil.SeedSegmentsBulk(t, pool, file.ID, "de", 0, 20, "Ziel")
	}
	tm := testutil.SeedTM(t, q, vendor.ID, name+" TM")
	testutil.SeedTMEntriesBulk(t, pool, tm.ID, "en", "de", 30, func(i int) (string, string) {
		return fmt.Sprintf("source %d", i), fmt.Sprintf("Ziel %d", i)
	})
	return vendor.ID
}

// Closing a client org closes its vendor orgs too. Each vendor org is
// deleted on the same bounded path as the client, before the client row
// goes, so the client's last statement never cascades through a whole
// vendor org. Another client's vendor org is left alone.
func TestIntegrationDeleteTenant_DeletesTheClientsVendorOrgsFirst(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	clientID, _ := seedOrg(t, pool, q, "client", 1, 10, 10)
	vendorA := seedVendorOrg(t, pool, q, clientID, "vendor-a", 2)
	vendorB := seedVendorOrg(t, pool, q, clientID, "vendor-b", 3)
	otherClient, _ := seedOrg(t, pool, q, "staying", 1, 10, 10)
	otherVendor := seedVendorOrg(t, pool, q, otherClient, "vendor-c", 1)
	otherBefore := orgRows(t, pool, otherVendor)
	if _, err := q.MarkTenantDeleting(ctx, clientID); err != nil {
		t.Fatal(err)
	}

	countPool, log := testutil.CountingPool(t)
	if err := tenantdelete.New(countPool, nil).Delete(ctx, clientID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, id := range []int64{clientID, vendorA, vendorB} {
		if left := orgRows(t, pool, id); left != 0 {
			t.Errorf("%d rows of tenant %d left", left, id)
		}
	}
	for _, table := range []string{"invites", "mt_engines", "memberships"} {
		if n := testutil.CountRows(t, pool, `SELECT count(*) FROM `+table+` WHERE tenant_id = ANY($1)`, []int64{vendorA, vendorB}); n != 0 {
			t.Errorf("%d %s rows of the vendor orgs left", n, table)
		}
	}
	// One file statement per file, the vendor orgs' included: 1 + 2 + 3.
	if rows := log.Rows("DeleteProjectFileBatch"); rows != 6 {
		t.Errorf("file deletes removed %d files one by one, want 6 (the vendor orgs' went by cascade)", rows)
	}
	if after := orgRows(t, pool, otherVendor); after != otherBefore {
		t.Errorf("another client's vendor org lost %d rows", otherBefore-after)
	}
}

// While a vendor org's delete runs elsewhere, the client's delete fails and
// keeps the client, so a retry finishes it once the vendor org is gone.
func TestIntegrationDeleteTenant_WaitsForARunningVendorDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	clientID, _ := seedOrg(t, pool, q, "client", 1, 10, 10)
	vendorID := seedVendorOrg(t, pool, q, clientID, "vendor-a", 1)
	if _, err := q.MarkTenantDeleting(ctx, clientID); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('tenant-delete:' || $1::bigint, 0))`, vendorID).Scan(&locked); err != nil || !locked {
		t.Fatalf("take the vendor org's delete lock: %v %v", locked, err)
	}

	if err := tenantdelete.New(pool, nil).Delete(ctx, clientID); err == nil {
		t.Fatal("the client delete succeeded while its vendor org's delete held the lock")
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM tenants WHERE id = $1`, clientID); n != 1 {
		t.Error("the client row went while its vendor org's delete was running")
	}

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('tenant-delete:' || $1::bigint, 0))`, vendorID); err != nil {
		t.Fatal(err)
	}
	if err := tenantdelete.New(pool, nil).Delete(ctx, clientID); err != nil {
		t.Fatalf("retried Delete: %v", err)
	}
	for _, id := range []int64{clientID, vendorID} {
		if left := orgRows(t, pool, id); left != 0 {
			t.Errorf("%d rows of tenant %d left after the retry", left, id)
		}
	}
}
