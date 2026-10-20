// Package demoorg keeps the one shared, read-only demo organization: a
// tenant of kind 'demo' filled with the whole internal/demodata dataset and
// four synthetic members. Every signed-in user
// may enter it and acts there as its guide, Alex Morgan, the tenant's owner.
// The names it is built with live in internal/demoorg/demoid.
//
// Ensure is the only writer. It runs at server start, daily from the scheduler
// and from `make seed`, and rebuilds the organization when the dataset version
// changed or the data is older than MaxAge.
package demoorg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/aiprompt"
	"openloc/internal/auth"
	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/demoorg/demoid"
	"openloc/internal/mtdefaults"
)

// DatasetVersion names the demo dataset. Bump it with any change to
// internal/demodata or to the members below, and every deployment rebuilds the
// demo organization at its next Ensure.
const DatasetVersion = 7

// datasetVersion is what Ensure compares against; tests change it to force a
// rebuild.
var datasetVersion = DatasetVersion

// MaxAge is how long a build lives. Its dates are relative to the build time,
// so an older build shows overdue deadlines and flat recent charts.
const MaxAge = 30 * 24 * time.Hour

// member is one synthetic user. The addresses are on example.com, a reserved
// domain, and the users have no password, so none of them can sign in.
type member struct {
	email, name, role string
	pairs             []string // target languages, from English
}

var (
	guide    = member{demoid.GuideEmail, "Alex Morgan", auth.RoleOwner, []string{"de", "fr"}}
	manager  = member{"priya.shah@demo.example.com", "Priya Shah", auth.RoleProjectManager, nil}
	german   = member{"jonas.weber@demo.example.com", "Jonas Weber", auth.RoleLinguist, []string{"de"}}
	french   = member{"camille.laurent@demo.example.com", "Camille Laurent", auth.RoleLinguist, []string{"fr"}}
	members  = []member{guide, manager, german, french}
	lifetime = int(MaxAge / (24 * time.Hour))
)

// Ensure makes sure the demo organization exists and is current, building or
// rebuilding it when it is missing, built from another DatasetVersion, or
// older than MaxAge at now. A rebuild deletes the old tenant (the same cascade
// as closing an organization) and builds the new one in one transaction, so
// readers see either the old organization or the new one. Safe to call from
// several replicas at once: a global advisory lock serialises them.
func Ensure(ctx context.Context, pool *pgxpool.Pool, now time.Time) (tenantID int64, rebuilt bool, err error) {
	if pool == nil {
		return 0, false, errors.New("demoorg: no database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	q := db.New(tx)
	if err := q.LockDemoOrganization(ctx); err != nil {
		return 0, false, fmt.Errorf("lock demo organization: %w", err)
	}
	current, err := q.GetDemoTenant(ctx)
	switch {
	case err == nil && !stale(current, now):
		return current.ID, false, tx.Commit(ctx)
	case err == nil:
		if err := q.DeleteTenant(ctx, current.ID); err != nil {
			return 0, false, fmt.Errorf("delete stale demo organization: %w", err)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, false, fmt.Errorf("find demo organization: %w", err)
	}

	id, err := build(ctx, tx, now)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("commit: %w", err)
	}
	return id, true, nil
}

func stale(t db.Tenant, now time.Time) bool {
	if t.DemoVersion == nil || int(*t.DemoVersion) != datasetVersion || !t.DemoBuiltAt.Valid {
		return true
	}
	return now.Sub(t.DemoBuiltAt.Time) > MaxAge
}

// build creates the demo tenant, its members and its dataset on tx.
func build(ctx context.Context, tx pgx.Tx, now time.Time) (int64, error) {
	q := db.New(tx)
	version := int32(datasetVersion)
	tenant, err := q.CreateDemoTenant(ctx, db.CreateDemoTenantParams{
		Name: demoid.Name, Slug: demoid.Slug, DemoVersion: &version, DemoBuiltAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("create demo tenant: %w", err)
	}
	ids := map[string]int64{}
	for _, m := range members {
		if ids[m.email], err = ensureMember(ctx, q, tenant.ID, m); err != nil {
			return 0, err
		}
	}
	guideID := ids[guide.email]
	// What every new organization is provisioned with, as on signup.
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		return 0, fmt.Errorf("built-in mt engines: %w", err)
	}
	if err := dashboarddefaults.InstallDefaults(ctx, q, tenant.ID, &guideID); err != nil {
		return 0, fmt.Errorf("default dashboards: %w", err)
	}
	// The built-in prompts, which a customer org gets on its first visit to
	// the AI pages; the demo org skips that lazy write, so it gets them here.
	if err := aiprompt.EnsureDefaultPrompts(ctx, q, tenant.ID); err != nil {
		return 0, fmt.Errorf("built-in prompts: %w", err)
	}
	if _, err := demodata.CreateTx(ctx, tx, tenant.ID, guideID, demodata.Options{
		LinguistUserID: ids[german.email],
		OmitDemoLabel:  true,
		Now:            now,
		Organization: &demodata.Organization{
			ManagerUserID:        ids[manager.email],
			FrenchLinguistUserID: ids[french.email],
			LifetimeDays:         lifetime,
		},
	}); err != nil {
		return 0, fmt.Errorf("demo dataset: %w", err)
	}
	return tenant.ID, nil
}

// ensureMember finds or creates the synthetic user, makes them a member of the
// demo tenant only, and records their language pairs.
func ensureMember(ctx context.Context, q *db.Queries, tenantID int64, m member) (int64, error) {
	u, err := q.GetUserByEmail(ctx, m.email)
	if errors.Is(err, pgx.ErrNoRows) {
		u, err = q.CreateUser(ctx, db.CreateUserParams{Email: m.email, Name: m.name})
	}
	if err != nil {
		return 0, fmt.Errorf("demo user %s: %w", m.email, err)
	}
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: u.ID, TenantID: tenantID, Role: m.role}); err != nil {
		return 0, fmt.Errorf("demo membership %s: %w", m.email, err)
	}
	for _, target := range m.pairs {
		if err := q.InsertUserLanguagePair(ctx, db.InsertUserLanguagePairParams{
			UserID: u.ID, SourceLang: "en", TargetLang: target, TenantID: tenantID,
		}); err != nil {
			return 0, fmt.Errorf("demo language pair %s: %w", m.email, err)
		}
	}
	return u.ID, nil
}
