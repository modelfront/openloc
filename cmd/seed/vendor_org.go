package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/seedid"
)

// ensureSeedUser returns the identity with this email, creating it (no
// password: dev login and Google only) when it is missing.
func ensureSeedUser(ctx context.Context, q *db.Queries, email, name string) (db.User, error) {
	user, err := q.GetUserByEmail(ctx, email)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, fmt.Errorf("look up %s: %w", email, err)
	}
	user, err = q.CreateUser(ctx, db.CreateUserParams{Email: email, Name: name})
	if err != nil {
		return db.User{}, fmt.Errorf("create %s: %w", email, err)
	}
	return user, nil
}

// ensureSeedMember gives the user exactly this role in the tenant.
func ensureSeedMember(ctx context.Context, q *db.Queries, userID, tenantID int64, role string) error {
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: userID, TenantID: tenantID, Role: role}); err != nil {
		return err
	}
	return q.UpdateMembershipRole(ctx, db.UpdateMembershipRoleParams{UserID: userID, TenantID: tenantID, Role: role})
}

// seedVendorOrg makes "Acme Vendors" a vendor org of the seed organization
// (clientID), with an admin, a project manager and a linguist, plus idle
// people: a disabled admin, a disabled linguist and one about to be warned.
// A vendor org has no owner. Returns its id, 0 when it could not be made.
func seedVendorOrg(ctx context.Context, q *db.Queries, clientID int64) int64 {
	tenant, err := q.GetTenantBySlug(ctx, seedid.VendorOrgSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		tenant, err = q.CreateVendorTenant(ctx, db.CreateVendorTenantParams{
			Name: seedid.VendorOrgName, Slug: seedid.VendorOrgSlug, ClientTenantID: clientID,
		})
	}
	if err != nil {
		log.Printf("⚠ Failed to create the vendor org: %v", err)
		return 0
	}
	if tenant.Kind != auth.TenantKindVendor || tenant.ClientTenantID == nil || *tenant.ClientTenantID != clientID {
		log.Printf("⚠ Slug %q is taken by an org that is not the seed's vendor org; skipped", seedid.VendorOrgSlug)
		return 0
	}
	// The pairs give the client's Vendors list a Languages column to show and filter.
	// unusedDays dates the last use; disabled marks the membership disabled
	// after the warning, as the inactivity sweep would.
	people := []struct {
		email, name, role string
		pairs             [][2]string
		unusedDays        int
		disabled          bool
	}{
		{seedid.VendorAdminEmail, seedid.VendorAdminName, auth.RoleAdmin, nil, 0, false},
		{seedid.VendorPMEmail, seedid.VendorPMName, auth.RoleProjectManager, [][2]string{{"de", "en"}}, 0, false},
		{seedid.VendorLinguistEmail, seedid.VendorLinguistName, auth.RoleLinguist, [][2]string{{"en", "de"}, {"en", "ja"}}, 0, false},
		{seedid.VendorIdleAdminEmail, seedid.VendorIdleAdminName, auth.RoleAdmin, nil, 120, true},
		{seedid.VendorIdleLinguistEmail, seedid.VendorIdleLinguistName, auth.RoleLinguist, [][2]string{{"en", "fr"}}, 100, true},
		{seedid.VendorDueLinguistEmail, seedid.VendorDueLinguistName, auth.RoleLinguist, [][2]string{{"en", "es"}}, 85, false},
	}
	for _, p := range people {
		user, err := ensureSeedUser(ctx, q, p.email, p.name)
		if err == nil {
			err = ensureSeedMember(ctx, q, user.ID, tenant.ID, p.role)
		}
		for _, pair := range p.pairs {
			if err != nil {
				break
			}
			err = q.InsertUserLanguagePair(ctx, db.InsertUserLanguagePairParams{
				UserID: user.ID, SourceLang: pair[0], TargetLang: pair[1], TenantID: tenant.ID,
			})
		}
		// Reseeding restores the dates, so the demo always shows one of each.
		if err == nil {
			err = q.SeedMembershipIdle(ctx, seedIdle(tenant.ID, user.ID, p.unusedDays, p.disabled))
		}
		if err != nil {
			log.Printf("⚠ Failed to add %s to the vendor org: %v", p.email, err)
		}
	}
	log.Printf("✓ Vendor org: %s (ID: %d), vendor of %s", tenant.Name, tenant.ID, seedid.OrgName)
	return tenant.ID
}

// seedIdle dates a membership unused for unusedDays; a disabled one was warned
// at 83 days and disabled at 90, as the inactivity sweep does.
func seedIdle(tenantID, userID int64, unusedDays int, disabled bool) db.SeedMembershipIdleParams {
	daysAgo := func(d int) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -d), Valid: true}
	}
	p := db.SeedMembershipIdleParams{TenantID: tenantID, UserID: userID, LastActiveAt: daysAgo(unusedDays)}
	if disabled {
		p.WarnedAt = daysAgo(unusedDays - 83)
		p.DisabledAt = daysAgo(unusedDays - 90)
	}
	return p
}
