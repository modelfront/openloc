package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtdefaults"
)

// provisionError names the provisioning step that failed, so HTTP callers can
// report it without leaking the database error.
type provisionError struct {
	what string
	err  error
}

func (e *provisionError) Error() string { return e.what + ": " + e.err.Error() }
func (e *provisionError) Unwrap() error { return e.err }

// provisionApprovedTenant creates an approved tenant with everything a new org
// starts with: the deployment's built-in MT engine, the default dashboards and
// the default workflow. It runs on q, so it shares the caller's transaction;
// a half-provisioned org cannot exist.
func provisionApprovedTenant(ctx context.Context, q db.Querier, name string, domain *string) (db.Tenant, error) {
	tenant, err := createTenantWithSlugRetry(ctx, name, func(slug string) (db.Tenant, error) {
		return q.CreateApprovedTenant(ctx, db.CreateApprovedTenantParams{
			Name:   name,
			Slug:   slug,
			Domain: domain,
		})
	})
	if err != nil {
		return db.Tenant{}, &provisionError{"organization", err}
	}
	// The built-in MT engine is created now, not at the next server restart: the
	// AI settings page shows it as built in and ready, so without the row it
	// advertises a vendor that cannot be assigned to anything.
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		return db.Tenant{}, &provisionError{"organization", err}
	}
	if err := dashboarddefaults.InstallDefaults(ctx, q, tenant.ID, nil); err != nil {
		return db.Tenant{}, &provisionError{"organization", err}
	}
	if err := ensureDefaultWorkflow(ctx, q, tenant.ID); err != nil {
		return db.Tenant{}, &provisionError{"default workflow", err}
	}
	return tenant, nil
}

// ErrAccountExists is returned by CreateOrgWithOwner when the owner email
// already has an account.
var ErrAccountExists = errors.New("an account for this email already exists")

// CreateOrgWithOwner creates an approved organization and its owner, who signs
// in with the given password. Tenant, owner and membership are written in one
// transaction. This is the first-run path for a self-hosted install, which has
// no OAuth and no email set up yet: password sign-in is always available.
func CreateOrgWithOwner(ctx context.Context, pool *pgxpool.Pool, name, ownerEmail, password string) (db.Tenant, db.User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.Tenant{}, db.User{}, errors.New("organization name is required")
	}
	email := normalizeEmail(ownerEmail)
	if email == "" || !strings.Contains(email, "@") {
		return db.Tenant{}, db.User{}, errors.New("a valid owner email is required")
	}
	if err := ValidatePassword(password); err != nil {
		return db.Tenant{}, db.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return db.Tenant{}, db.User{}, fmt.Errorf("hash password: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.Tenant{}, db.User{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	if _, err := q.GetUserByEmail(ctx, email); err == nil {
		return db.Tenant{}, db.User{}, ErrAccountExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Tenant{}, db.User{}, fmt.Errorf("load user: %w", err)
	}

	tenant, err := provisionApprovedTenant(ctx, q, name, nil)
	if err != nil {
		return db.Tenant{}, db.User{}, err
	}
	user, err := q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: &hash, Name: email})
	if err != nil {
		return db.Tenant{}, db.User{}, fmt.Errorf("create user: %w", err)
	}
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: user.ID, TenantID: tenant.ID, Role: RoleOwner}); err != nil {
		return db.Tenant{}, db.User{}, fmt.Errorf("create membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Tenant{}, db.User{}, fmt.Errorf("commit: %w", err)
	}
	return tenant, user, nil
}
