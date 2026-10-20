// Package demodata builds a realistic sample workspace inside one tenant:
// three customers, four projects in different states, files with real UI
// strings, two translation memories, a brand glossary, a QA profile and, when
// the tenant has the provider connections for them, two AI review engines.
//
// It is the dataset behind the help-page screenshots (cmd/seed-docs) and, with
// Options.Organization, the whole shared demo organization (internal/demoorg).
// Everything is written in one transaction and recorded in
// demo_workspace_items, so a later "remove demo data" action knows exactly
// what was created. Demo projects also carry the project label "Demo" unless
// Options.OmitDemoLabel is set.
//
// The package also holds the sample-data builders `make seed` shares
// (dashboards, workflow library, routers, job timings, notification rules).
package demodata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
)

// DemoLabelName and DemoLabelValue are the project label every demo project
// carries, so the project list can filter on it.
const (
	DemoLabelName  = "Demo"
	DemoLabelValue = "Demo workspace"
)

// Entity types stored in demo_workspace_items.entity_type.
const (
	ItemProject         = "project"
	ItemTM              = "tm"
	ItemTermBase        = "term_base"
	ItemQAProfile       = "qa_profile"
	ItemAIEngine        = "ai_engine"
	ItemAIPrompt        = "ai_prompt"
	ItemMTRouter        = "mt_router"
	ItemCustomer        = "customer"
	ItemLabelDefinition = "label_definition"
	ItemLabelValue      = "label_value"
)

// ErrOwnerNotMember means the owner user id is not a member of the tenant.
var ErrOwnerNotMember = errors.New("demodata: owner is not a member of the tenant")

// Options tunes who the dataset names. The zero value is valid.
type Options struct {
	// LinguistUserID is who gets the linguist steps. 0 picks the tenant's first
	// active linguist, and the owner when there is none.
	LinguistUserID int64
	// EntryAuthor is the name written on translation memory entries. Empty
	// uses the owner's display name.
	EntryAuthor string
	// SkipAIEngines leaves out the AI review engines and their prompts. The
	// caller names the reason in Result.Skipped.
	SkipAIEngines bool
	// OmitDemoLabel leaves the "Demo" project label off, for the help-page
	// screenshots, which show projects the way a customer's look. The manifest
	// still records every entity.
	OmitDemoLabel bool
	// Now anchors every date: deadlines, backdated history, notifications.
	// Zero uses the current time.
	Now time.Time
	// Organization, when set, builds a whole working organization around the
	// projects: keyless AI engines and prompts, a keyless MT engine and routers
	// with rules, the step and workflow library, templates, a TM profile,
	// dashboards, notifications, analyses, AI review results, QA findings on
	// every live project, project history and job timings.
	Organization *Organization
}

// Organization names the members of a whole demo organization. LinguistUserID
// in Options is the German linguist.
type Organization struct {
	// ManagerUserID creates the managed projects. 0 leaves them to the owner.
	ManagerUserID int64
	// FrenchLinguistUserID takes the French steps. 0 leaves them as in the
	// sample workspace.
	FrenchLinguistUserID int64
	// LifetimeDays is how long the data lives before it is rebuilt; future
	// deadlines move out by it so none is overdue before then.
	LifetimeDays int
}

// Entity is one created thing, by id and name.
type Entity struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Project is one created project with its jobs (one per file and language).
type Project struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
	JobIDs []int64 `json:"job_ids"`
}

// Result says what Create built. With AlreadyPresent set, nothing was written
// and every other field is empty.
type Result struct {
	AlreadyPresent      bool      `json:"already_present"`
	Projects            []Project `json:"projects"`
	TranslationMemories []Entity  `json:"translation_memories"`
	TermBases           []Entity  `json:"term_bases"`
	QAProfiles          []Entity  `json:"qa_profiles"`
	AIEngines           []Entity  `json:"ai_engines"`
	// Skipped lists the parts left out and why, one sentence each.
	Skipped []string `json:"skipped"`
	// LinguistUserID is who got the linguist steps (the owner when the tenant
	// has no linguist).
	LinguistUserID int64 `json:"linguist_user_id"`
}

// Create builds the dataset in tenantID, owned by ownerUserID. It returns
// Result{AlreadyPresent: true} without writing anything when the demo projects
// already exist in the tenant. Nothing outside the tenant is read or written,
// and no existing user is changed.
func Create(ctx context.Context, pool *pgxpool.Pool, tenantID, ownerUserID int64, opts Options) (Result, error) {
	if pool == nil {
		return Result{}, errors.New("demodata: no database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	res, err := CreateTx(ctx, tx, tenantID, ownerUserID, opts)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// CreateTx is Create on the caller's transaction, for a caller that writes
// more in the same one. The caller commits.
func CreateTx(ctx context.Context, tx pgx.Tx, tenantID, ownerUserID int64, opts Options) (Result, error) {
	if tenantID == 0 || ownerUserID == 0 {
		return Result{}, errors.New("demodata: tenant and owner are required")
	}
	q := db.New(tx)
	if err := q.LockDemoWorkspace(ctx, tenantID); err != nil {
		return Result{}, fmt.Errorf("lock demo workspace: %w", err)
	}
	present, err := q.DemoWorkspacePresent(ctx, db.DemoWorkspacePresentParams{TenantID: tenantID, MarkerProjectName: projectBooking})
	if err != nil {
		return Result{}, fmt.Errorf("check demo workspace: %w", err)
	}
	if present {
		return Result{AlreadyPresent: true}, nil
	}

	b, err := newBuilder(ctx, q, tx, tenantID, ownerUserID, opts)
	if err != nil {
		return Result{}, err
	}
	if err := b.build(); err != nil {
		return Result{}, err
	}
	return b.res, nil
}

func newBuilder(ctx context.Context, q *db.Queries, tx pgx.Tx, tenantID, ownerID int64, opts Options) (*builder, error) {
	if err := requireMember(ctx, q, tenantID, ownerID); err != nil {
		return nil, err
	}
	owner, err := q.GetUser(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("get owner: %w", err)
	}
	author := opts.EntryAuthor
	if author == "" {
		author = owner.Name
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	b := &builder{ctx: ctx, q: q, tx: tx, now: now, tenantID: tenantID, ownerID: ownerID, author: author,
		skipAI: opts.SkipAIEngines, omitLabel: opts.OmitDemoLabel, org: opts.Organization}
	if b.org != nil {
		for _, id := range []int64{b.org.ManagerUserID, b.org.FrenchLinguistUserID} {
			if id == 0 {
				continue
			}
			if err := requireMember(ctx, q, tenantID, id); err != nil {
				return nil, err
			}
		}
	}

	b.linguistID, err = pickLinguist(ctx, q, tenantID, ownerID, opts.LinguistUserID)
	if err != nil {
		return nil, err
	}
	if b.linguistID == ownerID {
		b.res.Skipped = append(b.res.Skipped, "The organization has no linguist, so every workflow step is assigned to you.")
	}
	b.res.LinguistUserID = b.linguistID
	return b, nil
}

func requireMember(ctx context.Context, q *db.Queries, tenantID, userID int64) error {
	if _, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: userID, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOwnerNotMember
		}
		return fmt.Errorf("get membership: %w", err)
	}
	return nil
}

// pickLinguist returns the explicit choice (after a membership check), else the
// tenant's first active linguist, else the owner.
func pickLinguist(ctx context.Context, q *db.Queries, tenantID, ownerID, explicit int64) (int64, error) {
	if explicit != 0 {
		if err := requireMember(ctx, q, tenantID, explicit); err != nil {
			if errors.Is(err, ErrOwnerNotMember) {
				return 0, errors.New("demodata: linguist is not a member of the tenant")
			}
			return 0, err
		}
		return explicit, nil
	}
	members, err := q.ListMembersByTenant(ctx, db.ListMembersByTenantParams{TenantID: tenantID})
	if err != nil {
		return 0, fmt.Errorf("list members: %w", err)
	}
	for _, m := range members {
		if m.Role == auth.RoleLinguist && m.ID != ownerID {
			return m.ID, nil
		}
	}
	return ownerID, nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
