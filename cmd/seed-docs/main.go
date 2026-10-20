// Command seed-docs adds a realistic customer dataset to the dev database so
// the help-page screenshots (make docs-screenshots) show a product in use —
// named customers, several projects in different states, files with real UI
// strings, a marketing translation memory with content, a brand glossary and
// a QA profile — instead of "Demo Project / demo.txt / Default TM".
//
//	make kill && make reset && make fresh && make docs-screenshots
//
// Runs on top of `make seed` (it needs the seed organization, the owner and the
// vendor user that seed creates). The dataset itself is demodata.Create; this
// command adds the one thing only screenshots want: display names for the two
// seed identities. Refuses to run twice: if the dataset is already present it
// says so and exits 0. Reset the database to start over.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/config"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/seedid"
)

// Display names for the two seed identities (emails stay: sign-in keys on
// them). The base seed's names read as test fixtures.
const (
	ownerName  = "Anna Miller"
	vendorName = "Daniel Weber"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	tenant, err := q.GetTenantBySlug(ctx, seedid.OrgSlug)
	if err != nil {
		log.Fatalf("seed org not found — run `make seed` first: %v", err)
	}
	owner, err := q.GetUserByEmail(ctx, seedid.OwnerEmail)
	if err != nil {
		log.Fatalf("seed owner not found — run `make seed` first: %v", err)
	}
	opts := demodata.Options{EntryAuthor: ownerName, OmitDemoLabel: true}
	var vendorID *int64
	if v, err := q.GetUserByEmail(ctx, seedid.VendorEmail); err == nil {
		vendorID = &v.ID
		opts.LinguistUserID = v.ID
	}

	res, err := demodata.Create(ctx, pool, tenant.ID, owner.ID, opts)
	if err != nil {
		log.Fatalf("docs dataset: %v", err)
	}
	if res.AlreadyPresent {
		fmt.Printf("✓ Docs dataset already present in %q — nothing to do (make reset to start over)\n", tenant.Name)
		return
	}

	if err := rename(ctx, pool, owner.ID, ownerName); err != nil {
		log.Fatal(err)
	}
	if vendorID != nil {
		if err := rename(ctx, pool, *vendorID, vendorName); err != nil {
			log.Fatal(err)
		}
	}
	printSummary(tenant, res)
}

func rename(ctx context.Context, pool *pgxpool.Pool, userID int64, name string) error {
	if _, err := pool.Exec(ctx, `UPDATE users SET name = $1 WHERE id = $2`, name, userID); err != nil {
		return fmt.Errorf("rename user %d: %w", userID, err)
	}
	return nil
}

func names(es []demodata.Entity) string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return strings.Join(out, " · ")
}

func printSummary(tenant db.Tenant, res demodata.Result) {
	projects := make([]string, len(res.Projects))
	for i, p := range res.Projects {
		projects[i] = fmt.Sprintf("%s (%s)", p.Name, strings.ReplaceAll(p.Status, "_", " "))
	}
	fmt.Printf(`
✓ Docs dataset seeded into %q (tenant %d)
  Projects:  %s
  Memories:  %s
  Glossary:  %s · QA profile: %s
  AI:        %s
  People:    %s (owner) · %s (linguist)
`, tenant.Name, tenant.ID, strings.Join(projects, " · "),
		names(res.TranslationMemories), names(res.TermBases), names(res.QAProfiles),
		names(res.AIEngines), ownerName, vendorName)
	for _, s := range res.Skipped {
		fmt.Printf("  Skipped:   %s\n", s)
	}
	fmt.Println("\n  Now: make docs-screenshots")
}
