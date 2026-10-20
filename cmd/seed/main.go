package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	"openloc/internal/auth"
	"openloc/internal/config"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/demoorg"
	"openloc/internal/langcode"
	"openloc/internal/mtdefaults"
	"openloc/internal/projectevents"
	"openloc/internal/ptr"
	"openloc/internal/secrets"
	"openloc/internal/seedid"
	"openloc/internal/settings"
	"openloc/internal/strdist"
	"openloc/internal/systemworkflow"
	"openloc/internal/terminology"
	"openloc/internal/tmprofile"
	"openloc/internal/translationmemory"
	"openloc/internal/wordcount"
)

func main() {
	cfg := config.Load()
	// Seed data is fiction: it must never land in a real organisation's tables.
	if cfg.Environment == "production" {
		log.Fatal("refusing to seed: ENV=production")
	}

	ctx := context.Background()
	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	q := db.New(pool)

	// Self-healing: check each component and create/fix what's missing.
	// This makes `make seed` safe to run at any time, not just on a fresh DB.

	healLanguages(ctx, pool)

	tenantID, userID := seedCoreData(ctx, q)

	// Second org so the Slack-style org picker is exercised in dev: the owner
	// is an admin (not owner) here, so dev-login lands on the picker with 2 orgs.
	acmeID := seedSecondOrg(ctx, q, userID)
	operatorID := seedOperator(ctx, q, acmeID)

	healTenantFields(ctx, pool, tenantID)

	healMTEngine(ctx, q, tenantID)

	// AI engines can only reference a *connected* provider, so demo QA needs one seeded.
	healAIRegistry(ctx, q, tenantID, cfg.AISecretsKey)

	healTMEntries(ctx, q, tenantID, userID)
	healTMProfile(ctx, q, tenantID, userID)

	healTerminology(ctx, q, tenantID, userID)

	healQAProfile(ctx, q, tenantID)

	healTemplateAndProject(ctx, q, tenantID, userID)

	healDemoFile(ctx, q, pool, tenantID)

	// Workflow steps first: the step / workflow libraries link to them
	healWorkflowSteps(ctx, q, pool, tenantID)
	healWorkflowLibrary(ctx, q, pool, tenantID)

	// Vendor user + one assigned job for the Tasks/Vendors demo.
	healVendor(ctx, q, pool, tenantID)
	seedDemoIssues(ctx, q, tenantID, userID, operatorID)

	healNotifications(ctx, q, pool, tenantID)

	// Backdated translation history so metric charts have curves, not single
	// dots.
	healDashboard(ctx, q, pool, tenantID)
	healPersonalDashboard(ctx, q, tenantID)

	// Processing-time panel demo data.
	healJobTimings(ctx, q, pool, tenantID)

	// A project in the second org filled by Apply TM and partly edited, for
	// the History panel. Last, so the main org's ids stay where they were.
	healHistoryDemo(ctx, q, pool, userID)
	// "Strings contain HTML" off: a template and a plain-text JSON file.
	healPlainTextDemo(ctx, q, pool, userID)

	// After every step that writes or backdates history: seeded rows get the
	// origin event pre-translation writes, and no translation is newer than its
	// first event.
	repairSeededHistory(ctx, pool)

	// 11. The shared, read-only demo organization
	if cfg.DemoOrg {
		ensureDemoOrg(ctx, pool)
	}

	log.Println("")
	log.Println("=== Seed complete ===")
	log.Println("Sign in with the Dev Login button (or Google). No passwords.")
	log.Printf("  Identity: %s — owner of %s, admin of Acme Localization", seedid.OwnerEmail, seedid.OrgName)
	log.Println("            → dev-login lands on the org picker (2 orgs)")
	log.Printf("  Vendor:   %s — linguist in %s", seedid.VendorEmail, seedid.OrgName)
}

// seededOrgSlugs are the organizations the seed creates (the demo org repairs
// its own data when it is built).
var seededOrgSlugs = []string{seedid.OrgSlug, "acme"}

// repairSeededHistory runs demodata.HealOriginHistory and
// demodata.AlignTranslationDates for the seed's own organizations only, so a
// run against the wrong database cannot rewrite anyone else's history.
func repairSeededHistory(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `SELECT id FROM tenants WHERE slug = ANY($1)`, seededOrgSlugs)
	if err != nil {
		log.Printf("  ! align translation dates: %v", err)
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		log.Printf("  ! align translation dates: %v", err)
		return
	}
	for _, id := range ids {
		if err := demodata.HealOriginHistory(ctx, pool, id); err != nil {
			log.Printf("  ! heal origin history (tenant %d): %v", id, err)
			return
		}
		if err := demodata.AlignTranslationDates(ctx, pool, id); err != nil {
			log.Printf("  ! align translation dates (tenant %d): %v", id, err)
			return
		}
	}
	log.Printf("  ✓ Seeded translations have their origin event and date (%d org(s))", len(ids))
}

// ensureDemoOrg builds the demo organization, or rebuilds it when its dataset
// version changed or it is older than demoorg.MaxAge.
func ensureDemoOrg(ctx context.Context, pool *pgxpool.Pool) {
	id, rebuilt, err := demoorg.Ensure(ctx, pool, time.Now())
	if err != nil {
		log.Printf("⚠ Could not ensure the demo organization: %v", err)
		return
	}
	if rebuilt {
		log.Printf("✓ Demo organization built (tenant %d, dataset v%d)", id, demoorg.DatasetVersion)
		return
	}
	log.Printf("✓ Demo organization is current (tenant %d)", id)
}

// healVendor creates a demo vendor user and assigns one job to them so the
// Tasks page (vendor's home) and the Vendors admin page have data to show.
func healVendor(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	var vendorID int64
	if v, err := q.GetUserByEmail(ctx, seedid.VendorEmail); err == nil {
		vendorID = v.ID
		log.Printf("✓ Vendor identity exists: %s", seedid.VendorEmail)
	} else {
		created, cerr := q.CreateUser(ctx, db.CreateUserParams{
			Email:        seedid.VendorEmail,
			PasswordHash: nil,
			Name:         seedid.VendorName,
		})
		if cerr != nil {
			log.Printf("⚠ Failed to create vendor user: %v", cerr)
			return
		}
		vendorID = created.ID
		log.Printf("✓ Vendor identity created: %s", seedid.VendorEmail)
	}
	// Linguist membership in the seed org (create-if-missing, then heal role).
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: vendorID, TenantID: tenantID, Role: "linguist"}); err != nil {
		log.Printf("⚠ Failed to create vendor membership: %v", err)
		return
	}
	if err := q.UpdateMembershipRole(ctx, db.UpdateMembershipRoleParams{UserID: vendorID, TenantID: tenantID, Role: "linguist"}); err != nil {
		log.Printf("⚠ Failed to heal vendor role: %v", err)
	}

	// Assign the current active step of one demo job to the vendor so their
	// Tasks page isn't empty.
	var currentStepID *int64
	row := pool.QueryRow(ctx, `
		SELECT j.current_step_id FROM jobs j
		JOIN files f ON f.id = j.file_id
		JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1 AND j.current_step_id IS NOT NULL
		ORDER BY j.id LIMIT 1`, tenantID)
	if err := row.Scan(&currentStepID); err != nil || currentStepID == nil {
		log.Printf("  ⚠ No assignable job found for vendor: %v", err)
		return
	}
	if err := q.AssignStepToUser(ctx, db.AssignStepToUserParams{ID: *currentStepID, AssignedTo: &vendorID}); err != nil {
		log.Printf("  ⚠ Failed to assign job to vendor: %v", err)
		return
	}
	log.Printf("  ✓ Assigned one demo job to %s", seedid.VendorEmail)
}

// healLanguages ensures the languages table is populated. If empty, inserts common ISO 639-1 languages.
func healLanguages(ctx context.Context, pool *pgxpool.Pool) {
	var count int64
	err := pool.QueryRow(ctx, "SELECT count(*) FROM languages").Scan(&count)
	if err != nil {
		log.Printf("⚠ Could not check languages table: %v", err)
		return
	}
	// Always upsert — ON CONFLICT DO NOTHING handles existing rows.
	// This ensures new regional variants are added on subsequent seeds.
	langs := [][2]string{
		{"af", "Afrikaans"}, {"sq", "Albanian"}, {"am", "Amharic"}, {"ar", "Arabic"},
		{"hy", "Armenian"}, {"az", "Azerbaijani"}, {"eu", "Basque"}, {"be", "Belarusian"},
		{"bn", "Bengali"}, {"bs", "Bosnian"}, {"bg", "Bulgarian"}, {"ca", "Catalan"},
		{"zh", "Chinese"}, {"hr", "Croatian"}, {"cs", "Czech"}, {"da", "Danish"},
		{"nl", "Dutch"}, {"en", "English"}, {"et", "Estonian"}, {"fi", "Finnish"},
		{"fr", "French"}, {"gl", "Galician"}, {"ka", "Georgian"}, {"de", "German"},
		{"el", "Greek"}, {"gu", "Gujarati"}, {"ht", "Haitian Creole"}, {"he", "Hebrew"},
		{"hi", "Hindi"}, {"hu", "Hungarian"}, {"is", "Icelandic"}, {"id", "Indonesian"},
		{"ga", "Irish"}, {"it", "Italian"}, {"ja", "Japanese"}, {"kn", "Kannada"},
		{"kk", "Kazakh"}, {"km", "Khmer"}, {"ko", "Korean"}, {"ku", "Kurdish"},
		{"ky", "Kyrgyz"}, {"lo", "Lao"}, {"lv", "Latvian"}, {"lt", "Lithuanian"},
		{"mk", "Macedonian"}, {"ms", "Malay"}, {"ml", "Malayalam"}, {"mt", "Maltese"},
		{"mr", "Marathi"}, {"mn", "Mongolian"}, {"ne", "Nepali"}, {"nb", "Norwegian"},
		{"or", "Odia"}, {"ps", "Pashto"}, {"fa", "Persian"}, {"pl", "Polish"},
		{"pt", "Portuguese"}, {"pa", "Punjabi"}, {"ro", "Romanian"}, {"ru", "Russian"},
		{"sr", "Serbian"}, {"si", "Sinhala"}, {"sk", "Slovak"}, {"sl", "Slovenian"},
		{"so", "Somali"}, {"es", "Spanish"}, {"sw", "Swahili"}, {"sv", "Swedish"},
		{"tl", "Tagalog"}, {"tg", "Tajik"}, {"ta", "Tamil"}, {"te", "Telugu"},
		{"th", "Thai"}, {"tr", "Turkish"}, {"uk", "Ukrainian"}, {"ur", "Urdu"},
		{"uz", "Uzbek"}, {"vi", "Vietnamese"}, {"cy", "Welsh"}, {"xh", "Xhosa"},
		{"yi", "Yiddish"}, {"yo", "Yoruba"}, {"zu", "Zulu"},
		// Regional variants commonly used in translation
		{"en-us", "English (US)"}, {"en-gb", "English (UK)"}, {"en-au", "English (Australia)"},
		{"es-mx", "Spanish (Mexico)"}, {"es-us", "Spanish (US)"}, {"es-es", "Spanish (Spain)"},
		{"es-419", "Spanish (Latin America)"}, {"es-ar", "Spanish (Argentina)"}, {"es-co", "Spanish (Colombia)"},
		{"fr-fr", "French (France)"}, {"fr-ca", "French (Canada)"}, {"fr-be", "French (Belgium)"},
		{"pt-br", "Portuguese (Brazil)"}, {"pt-pt", "Portuguese (Portugal)"},
		{"zh-hans", "Chinese (Simplified)"}, {"zh-hant", "Chinese (Traditional)"},
		{"zh-cn", "Chinese (China)"}, {"zh-tw", "Chinese (Taiwan)"}, {"zh-hk", "Chinese (Hong Kong)"},
		{"de-de", "German (Germany)"}, {"de-at", "German (Austria)"}, {"de-ch", "German (Switzerland)"},
		{"it-it", "Italian (Italy)"}, {"nl-nl", "Dutch (Netherlands)"}, {"nl-be", "Dutch (Belgium)"},
		{"ja-jp", "Japanese (Japan)"}, {"ko-kr", "Korean (Korea)"},
		{"ar-sa", "Arabic (Saudi Arabia)"}, {"ar-ae", "Arabic (UAE)"}, {"ar-eg", "Arabic (Egypt)"},
		{"hi-in", "Hindi (India)"}, {"ru-ru", "Russian (Russia)"},
		{"sv-se", "Swedish (Sweden)"}, {"da-dk", "Danish (Denmark)"}, {"nb-no", "Norwegian (Norway)"},
		{"fi-fi", "Finnish (Finland)"}, {"pl-pl", "Polish (Poland)"}, {"cs-cz", "Czech (Czech Republic)"},
		{"ro-ro", "Romanian (Romania)"}, {"hu-hu", "Hungarian (Hungary)"}, {"tr-tr", "Turkish (Turkey)"},
		{"th-th", "Thai (Thailand)"}, {"vi-vn", "Vietnamese (Vietnam)"}, {"id-id", "Indonesian (Indonesia)"},
	}
	for _, l := range langs {
		if _, err := pool.Exec(ctx,
			"INSERT INTO languages (code, name) VALUES ($1, $2) ON CONFLICT DO NOTHING",
			l[0], l[1],
		); err != nil {
			log.Printf("⚠ Failed to insert language %s: %v", l[0], err)
		}
	}
	log.Printf("✓ Languages: seeded %d rows", len(langs))
}

// seedCoreData ensures the seed tenant, the owner identity, and the owner's
// membership all exist. Self-healing: safe to run repeatedly.
// Returns (tenantID, userID).
func seedCoreData(ctx context.Context, q *db.Queries) (int64, int64) {
	// Tenant — look up by slug so a re-seed reuses the existing org.
	tenant, err := q.GetTenantBySlug(ctx, seedid.OrgSlug)
	if err != nil {
		tenant, err = q.CreateTenant(ctx, db.CreateTenantParams{Name: seedid.OrgName, Slug: seedid.OrgSlug})
		if err != nil {
			log.Fatalf("Failed to create tenant: %v", err)
		}
		log.Printf("✓ Tenant created: %s (slug: %s, ID: %d)", tenant.Name, tenant.Slug, tenant.ID)
	} else {
		log.Printf("✓ Tenant exists: %s (slug: %s, ID: %d)", tenant.Name, tenant.Slug, tenant.ID)
	}

	// Identity — global user, no password (Google/dev-only sign-in).
	user, err := q.GetUserByEmail(ctx, seedid.OwnerEmail)
	if err != nil {
		user, err = q.CreateUser(ctx, db.CreateUserParams{
			Email:        seedid.OwnerEmail,
			PasswordHash: nil,
			Name:         seedid.OwnerName,
		})
		if err != nil {
			log.Fatalf("Failed to create user: %v", err)
		}
		log.Printf("✓ Identity created: %s <%s> (ID: %d)", user.Name, user.Email, user.ID)
	} else {
		log.Printf("✓ Identity exists: %s <%s> (ID: %d)", user.Name, user.Email, user.ID)
	}

	// Owner membership in the seed org (create-if-missing, then heal role → owner).
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: user.ID, TenantID: tenant.ID, Role: "owner"}); err != nil {
		log.Fatalf("Failed to create owner membership: %v", err)
	}
	if err := q.UpdateMembershipRole(ctx, db.UpdateMembershipRoleParams{UserID: user.ID, TenantID: tenant.ID, Role: "owner"}); err != nil {
		log.Printf("⚠ Failed to heal owner role: %v", err)
	}
	log.Printf("✓ Membership: %s → %s (owner)", seedid.OwnerEmail, seedid.OrgName)

	return tenant.ID, user.ID
}

// seedSecondOrg creates a second tenant ("Acme Localization") and makes the
// owner identity an ADMIN there. Purpose: exercise the org picker in dev —
// with 2 memberships, dev-login shows the Slack-style workspace list instead of
// dropping straight into the app. Returns its id, 0 when it could not be made.
func seedSecondOrg(ctx context.Context, q *db.Queries, userID int64) int64 {
	tenant, err := q.GetTenantBySlug(ctx, "acme")
	if err != nil {
		tenant, err = q.CreateTenant(ctx, db.CreateTenantParams{Name: "Acme Localization", Slug: "acme"})
		if err != nil {
			log.Printf("⚠ Failed to create second org: %v", err)
			return 0
		}
		log.Printf("✓ Second org created: %s (slug: %s, ID: %d)", tenant.Name, tenant.Slug, tenant.ID)
	}
	// Every org has the built-in Pre-translation workflow.
	if err := systemworkflow.EnsurePretranslate(ctx, q, tenant.ID); err != nil {
		log.Printf("⚠ Failed to ensure Acme's Pre-translation workflow: %v", err)
	}
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: userID, TenantID: tenant.ID, Role: "admin"}); err != nil {
		log.Printf("⚠ Failed to create Acme membership: %v", err)
		return tenant.ID
	}
	log.Printf("✓ Membership: %s → Acme Localization (admin)", seedid.OwnerEmail)
	return tenant.ID
}

// healTenantFields ensures the tenant has slug, domain and open_registration
// set.
//
// open_registration MUST be true on the seed tenant so that any user on its
// domain (e.g. a fresh QA tester) can log in via Google OAuth
// without needing a manual invite. Without it, tryOpenRegistration finds no
// tenant with a matching domain + open_registration and the login redirects to
// /login?error=no_account. Every seed run enforces it, so a DB reset cannot
// drop it.
func healTenantFields(ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	var slug, domain *string
	var openReg bool
	err := pool.QueryRow(ctx,
		"SELECT slug, domain, open_registration FROM tenants WHERE id = $1",
		tenantID,
	).Scan(&slug, &domain, &openReg)
	if err != nil {
		log.Printf("⚠ Could not check tenant fields: %v", err)
		return
	}
	slugOK := slug != nil && *slug != ""
	domainOK := domain != nil && *domain != ""
	if slugOK && domainOK {
		log.Printf("✓ Tenant slug: %s, domain: %s", *slug, *domain)
	} else {
		_, err = pool.Exec(ctx, "UPDATE tenants SET slug = $2, domain = $3 WHERE id = $1", tenantID, seedid.OrgSlug, seedid.EmailDomain)
		if err != nil {
			log.Printf("⚠ Failed to set tenant fields: %v", err)
		} else {
			log.Printf("  ↳ Fixed tenant slug → %s, domain → %s", seedid.OrgSlug, seedid.EmailDomain)
		}
	}

	// Ensure open_registration is enabled so users on the seed tenant's domain can
	// self-register via Google OAuth without an invite.
	if openReg {
		log.Printf("✓ Tenant open_registration: true")
	} else {
		_, err = pool.Exec(ctx, "UPDATE tenants SET open_registration = true WHERE id = $1", tenantID)
		if err != nil {
			log.Printf("⚠ Failed to enable open_registration: %v", err)
		} else {
			log.Printf("  ↳ Enabled open_registration (lets @%s users auto-register via Google OAuth)", seedid.EmailDomain)
		}
	}
}

// healMTEngine ensures at least one MT engine exists for the tenant.
func healMTEngine(ctx context.Context, q *db.Queries, tenantID int64) {
	// Delegate to the same provisioning the server runs at startup and on tenant
	// creation, rather than hand-rolling a row here. A seeded row that is not
	// marked deployment-managed would make startup add a SECOND one beside it;
	// one provisioning path means one row, whoever runs first.
	if err := mtdefaults.EnsureForTenant(ctx, q, tenantID); err != nil {
		log.Printf("⚠ Could not provision built-in MT engines: %v", err)
		return
	}
	engines, err := q.ListMTEngines(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check MT engines: %v", err)
		return
	}
	for _, e := range engines {
		if e.DeploymentManaged {
			log.Printf("✓ MT Engine: %s (ID: %d)", e.Name, e.ID)
		}
	}
}

// healAIRegistry seeds a connected AI provider plus a demo prompt + engine so
// the AI feature is demoable end-to-end. AI engines require a
// *connected* provider, so without a seeded provider row the AI Engines section
// can't create anything. The stored key is an encrypted placeholder, so calls
// to the provider fail until a real key is entered.
func healAIRegistry(ctx context.Context, q *db.Queries, tenantID int64, secretsKey string) {
	enc, err := secrets.NewEncryptor(secretsKey)
	if err != nil {
		log.Printf("⚠ Could not build encryptor for AI provider seed: %v", err)
		return
	}
	demoKey := "demo-placeholder-key"
	placeholder, err := enc.Encrypt(demoKey)
	if err != nil {
		log.Printf("⚠ Could not encrypt AI vendor placeholder: %v", err)
		return
	}
	tokens, err := q.ListVendorTokens(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check AI vendor tokens: %v", err)
		return
	}
	hasToken := map[string]bool{}
	for _, t := range tokens {
		hasToken[t.Provider] = true
	}
	// Every vendor row exists.
	for _, prov := range []string{"openai", "claude", ai.ProviderDeepL} {
		if _, err := q.UpsertAIVendor(ctx, db.UpsertAIVendorParams{TenantID: tenantID, Provider: prov}); err != nil {
			log.Printf("⚠ Could not seed AI vendor %s: %v", prov, err)
			return
		}
	}
	// Demo: give every vendor a Default token (placeholder key) so the demo
	// engines have a key to point at.
	//
	// DeepL is here so the MT half of the AI settings grid has a configured
	// vendor to look at — a key entered through the UI is verified live, so a
	// dummy one can only come from the seed.
	for _, prov := range []string{"openai", "claude", ai.ProviderDeepL} {
		if hasToken[prov] {
			log.Printf("✓ AI Vendor: %s (has token)", prov)
			continue
		}
		vendor, err := q.GetAIVendor(ctx, db.GetAIVendorParams{TenantID: tenantID, Provider: prov})
		if err != nil {
			log.Printf("⚠ Could not load vendor %s: %v", prov, err)
			return
		}
		// A token needs a connection to hang off. The demo
		// uses each vendor's public API, so the connection carries no endpoint.
		//
		// REUSE an existing connection for this vendor rather than making a
		// second one. "No token" does not mean "no connection": delete a key in
		// the UI and the connection stays, so an unconditional create left the
		// tenant with two connections for one vendor — and the settings grid
		// renders one tile each, so the vendor appeared twice.
		connID, err := existingConnectionID(ctx, q, tenantID, prov)
		if err != nil {
			log.Printf("⚠ Could not look up connections for %s: %v", prov, err)
			return
		}
		if connID == 0 {
			conn, err := q.CreateAIConnection(ctx, db.CreateAIConnectionParams{
				VendorID: vendor.ID, Name: ai.DisplayName(prov), BaseUrl: "",
			})
			if err != nil {
				log.Printf("⚠ Could not seed connection %s: %v", prov, err)
				return
			}
			connID = conn.ID
		}
		tok, err := q.CreateVendorToken(ctx, db.CreateVendorTokenParams{
			VendorID: vendor.ID, Name: ai.DefaultTokenName, ApiKeyEncrypted: placeholder,
			ConnectionID: connID, KeyLast4: demoKey[len(demoKey)-4:],
		})
		if err != nil {
			log.Printf("⚠ Could not seed vendor token %s: %v", prov, err)
			return
		}
		// A key does nothing on its own — an MT engine is what a router routes
		// to, and what the pipeline reads. Give each MT vendor one, so manual QA
		// has something to assign without building it first.
		if ai.IsMTEngineProvider(prov) {
			if _, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
				Name:       ai.DisplayName(prov),
				EngineType: prov,
				Enabled:    true,
				TenantID:   tenantID,
				TokenID:    &tok.ID,
				Settings:   []byte(`{}`),
			}); err != nil {
				log.Printf("⚠ Could not seed an MT engine for %s: %v", prov, err)
				return
			}
		}
		log.Printf("✓ AI Vendor token created: %s / Default (demo placeholder key)", prov)
	}

	// Demo prompt + engine (idempotent by name). Prompts pin a provider + model:
	// OpenAI's gpt-4o here.
	openaiModel := "gpt-4o"
	prompts, err := q.ListAIPrompts(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check AI prompts: %v", err)
		return
	}
	var promptID int64
	for _, p := range prompts {
		if p.Name == "Demo QE reviewer" {
			promptID = p.ID
		}
	}
	if promptID == 0 {
		p, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
			TenantID:   tenantID,
			Name:       "Demo QE reviewer",
			PromptText: "Assess whether the translation is acceptable. Answer approve or reject.",
			OutStatus:  true,
		})
		if err != nil {
			log.Printf("⚠ Could not seed AI prompt: %v", err)
			return
		}
		promptID = p.ID
		log.Printf("✓ AI Prompt created: Demo QE reviewer (ID: %d)", promptID)
	} else {
		log.Printf("✓ AI Prompt: Demo QE reviewer (ID: %d)", promptID)
	}

	// Demo MT translation prompt (idempotent by name) — outputs text, used by
	// the AI-as-MT engine below.
	var mtPromptID int64
	for _, p := range prompts {
		if p.Name == "Demo MT translator" {
			mtPromptID = p.ID
		}
	}
	if mtPromptID == 0 {
		p, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
			TenantID:   tenantID,
			Name:       "Demo MT translator",
			PromptText: "You are a professional translator. Translate faithfully and idiomatically.",
			OutText:    true,
		})
		if err != nil {
			log.Printf("⚠ Could not seed AI MT prompt: %v", err)
			return
		}
		mtPromptID = p.ID
		log.Printf("✓ AI Prompt created: Demo MT translator (ID: %d)", mtPromptID)
	} else {
		log.Printf("✓ AI Prompt: Demo MT translator (ID: %d)", mtPromptID)
	}

	// Demo segment prompts — created before the engines so their rows can
	// reference them. The upsert below is idempotent, so no pre-read is needed.
	//
	// Demo engines (idempotent by name). An engine is exactly one row
	// applying to all language pairs. The demo data seeds one of each purpose:
	//   - "OpenAI review"          — an LLM QE row (the Demo QE reviewer prompt).
	//   - "OpenAI translation"     — an LLM MT row (usable in MT assignments).
	// Named by what runs them, not "Default" — no provider is the recommended
	// one, and "Default" would collide with the built-in "Default" PROMPT, so
	// two different things would read as the same string.
	engines, err := q.ListAIEngines(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check AI engines: %v", err)
		return
	}
	haveByName := map[string]bool{}
	for _, e := range engines {
		haveByName[e.Name] = true
	}
	comment := "comment"
	seedEngine := func(name, purpose string, rows []db.CreateAIEngineRowParams) {
		if haveByName[name] {
			log.Printf("✓ AI Engine: %s", name)
			return
		}
		e, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenantID, Name: name, Purpose: purpose})
		if err != nil {
			log.Printf("⚠ Could not seed AI engine %q: %v", name, err)
			return
		}
		for _, row := range rows {
			row.EngineID = e.ID
			if _, err := q.CreateAIEngineRow(ctx, row); err != nil {
				log.Printf("⚠ Could not seed AI engine row: %v", err)
				return
			}
		}
		log.Printf("✓ AI Engine created: %s (ID: %d, %d rows)", name, e.ID, len(rows))
	}
	// System rows carry their own token + model + settings,
	// so resolve the two vendor tokens directly.
	openaiTok, err := q.GetFirstVendorToken(ctx, db.GetFirstVendorTokenParams{TenantID: tenantID, Provider: "openai"})
	if err != nil {
		log.Printf("⚠ Could not load OpenAI token for AI engine rows: %v", err)
		return
	}
	openaiTokID := openaiTok.ID
	seedEngine("OpenAI review", "qe_ape", []db.CreateAIEngineRowParams{
		{SourceLang: langcode.Wildcard, TargetLangs: []string{langcode.Wildcard}, SystemPromptID: &promptID, BadOutputAction: &comment, SortOrder: 0,
			TokenID: &openaiTokID, Model: openaiModel, Settings: []byte("{}")},
	})
	seedEngine("OpenAI translation", "mt", []db.CreateAIEngineRowParams{
		{SourceLang: langcode.Wildcard, TargetLangs: []string{langcode.Wildcard}, SystemPromptID: &mtPromptID, BadOutputAction: &comment, SortOrder: 0,
			TokenID: &openaiTokID, Model: openaiModel, Settings: []byte("{}")},
	})
}

// healTMEntries ensures the Default TM and its entries exist.
func healTMEntries(ctx context.Context, q *db.Queries, tenantID, userID int64) {
	tms, err := q.ListTMCollections(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check TMs: %v", err)
		return
	}

	var tmID int64
	if len(tms) > 0 {
		tmID = tms[0].ID
		log.Printf("✓ TM: %s (ID: %d)", tms[0].Name, tmID)
	} else {
		sourceLang := "en"
		tm, err := q.CreateTMCollection(ctx, db.CreateTMCollectionParams{
			Name:            "Default TM",
			TenantID:        tenantID,
			SourceLanguage:  &sourceLang,
			TargetLanguages: []string{"es", "fr", "de"},
			// Customer left null — the demo project's customer is optional too.
			CreatedBy: auth.UserIDPtr(userID),
		})
		if err != nil {
			log.Fatalf("Failed to create TM: %v", err)
		}
		tmID = tm.ID
		log.Printf("✓ TM created: %s (ID: %d)", tm.Name, tmID)
	}

	entries, err := q.ListTMEntries(ctx, db.ListTMEntriesParams{TmID: tmID, Limit: 1})
	if err != nil {
		log.Printf("⚠ Could not check TM entries: %v", err)
		return
	}
	if len(entries) > 0 {
		log.Println("✓ TM entries already seeded")
		return
	}

	seedTMEntries(ctx, q, tmID)
}

// healTerminology ensures the Default Term Base and its entries exist.
func healTerminology(ctx context.Context, q *db.Queries, tenantID, userID int64) {
	bases, err := q.ListTermBases(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check term bases: %v", err)
		return
	}

	if len(bases) > 0 {
		log.Printf("✓ Term Base: %s (ID: %d)", bases[0].Name, bases[0].ID)
		return
	}

	// Create term bases for en→es, en→fr, en→de
	type langTermBase struct {
		targetLang string
		entries    []struct{ src, tgt, status string }
	}
	termBases := []langTermBase{
		{"es", []struct{ src, tgt, status string }{
			{"password", "contraseña", "approved"},
			{"email address", "dirección de correo electrónico", "approved"},
			{"two-factor authentication", "autenticación de dos factores", "approved"},
			{"login", "iniciar sesión", "approved"},
			{"dashboard", "panel de control", "approved"},
			{"password", "clave", "forbidden"}, // forbidden: don't translate "password" as "clave"
		}},
		{"fr", []struct{ src, tgt, status string }{
			{"password", "mot de passe", "approved"},
			{"email address", "adresse e-mail", "approved"},
			{"two-factor authentication", "authentification à deux facteurs", "approved"},
			{"login", "connexion", "approved"},
			{"dashboard", "tableau de bord", "approved"},
		}},
		{"de", []struct{ src, tgt, status string }{
			{"password", "Passwort", "approved"},
			{"email address", "E-Mail-Adresse", "approved"},
			{"two-factor authentication", "Zwei-Faktor-Authentifizierung", "approved"},
			{"login", "Anmeldung", "approved"},
			{"dashboard", "Übersicht", "approved"},
		}},
	}

	for _, g := range termBases {
		base, err := q.CreateTermBase(ctx, db.CreateTermBaseParams{
			TenantID:       tenantID,
			Name:           "Default Term Base (en→" + g.targetLang + ")",
			Description:    "Seed term base for en → " + g.targetLang,
			SourceLanguage: "en",
			CreatedBy:      auth.UserIDPtr(userID),
		})
		if err != nil {
			log.Printf("⚠ Failed to create term base en→%s: %v", g.targetLang, err)
			continue
		}

		for _, e := range g.entries {
			ci := terminology.ConceptInput{Terms: []terminology.ConceptTermInput{
				{Language: "en", Text: e.src, Status: "approved"},
				{Language: g.targetLang, Text: e.tgt, Status: e.status},
			}}
			if _, err := terminology.CreateConcept(ctx, q, base, ci, nil); err != nil {
				log.Printf("  ⚠ Failed to seed term %q: %v", e.src, err)
			}
		}
		// Derive target_languages from the seeded terms.
		if err := q.SyncTermBaseTargetLanguages(ctx, base.ID); err != nil {
			log.Printf("  ⚠ Failed to sync target_languages for %s: %v", base.Name, err)
		}
		log.Printf("✓ Term Base created: %s (ID: %d, %d entries)", base.Name, base.ID, len(g.entries))
	}
}

// demoProjectName is the seed's own project. Demo data (demo.txt, its jobs,
// workflow assignments, deadlines) is bound to the project with THIS name —
// never to "whichever project is newest", which would dump seed data into a
// dev's own project or an E2E leftover.
const demoProjectName = "Demo Project"

// findDemoProject returns the tenant's demo project, or false if it's missing.
// Deliberately matches by name rather than taking ListProjects()[0]: that query
// is ORDER BY created_at DESC, so [0] is whatever project was created last.
func findDemoProject(ctx context.Context, q *db.Queries, tenantID int64) (db.Project, bool) {
	projects, err := q.ListProjects(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check projects: %v", err)
		return db.Project{}, false
	}
	for _, p := range projects {
		if p.Name == demoProjectName {
			return p, true
		}
	}
	return db.Project{}, false
}

// healTemplateAndProject ensures template + project exist.
func healTemplateAndProject(ctx context.Context, q *db.Queries, tenantID, userID int64) {
	templates, err := q.ListTemplates(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check templates: %v", err)
		return
	}

	if len(templates) > 0 {
		log.Printf("✓ Template: %s (ID: %d)", templates[0].Name, templates[0].ID)
		// Heal: ensure terminology assignments exist on existing template
		healTemplateTermAssignments(ctx, q, templates[0], tenantID)
		// Heal: ensure QA profile is assigned
		healTemplateQAProfile(ctx, q, templates[0], tenantID)
	} else {
		// Need MT engine + TM IDs for assignments
		engines, _ := q.ListMTEngines(ctx, tenantID)
		tms, _ := q.ListTMCollections(ctx, tenantID)
		if len(engines) == 0 || len(tms) == 0 {
			log.Println("⚠ Skipping template creation — missing MT engine or TM")
			return
		}
		createTemplateWithAssignments(ctx, q, tenantID, engines[0].ID, tms[0].ID)
	}

	if project, ok := findDemoProject(ctx, q, tenantID); ok {
		log.Printf("✓ Project: %s (ID: %d)", project.Name, project.ID)
		healProjectAssignments(ctx, q, project, tenantID)
	} else {
		templates, _ = q.ListTemplates(ctx, tenantID)
		createProject(ctx, q, tenantID, userID, templates)
	}
}

// healTemplateTermAssignments ensures the existing template has term base assignments.
func healTemplateTermAssignments(ctx context.Context, q *db.Queries, template db.Template, tenantID int64) {
	existing, err := q.ListSettingsTermAssignments(ctx, template.SettingsProfileID)
	if err != nil {
		log.Printf("⚠ Could not check term assignments: %v", err)
		return
	}
	if len(existing) > 0 {
		log.Printf("  ✓ Template term assignments: %d", len(existing))
		return
	}

	// Add term assignments for each term base — one per target language present.
	termBases, _ := q.ListTermBases(ctx, tenantID)
	for _, g := range termBases {
		for _, tgt := range g.TargetLanguages {
			_, err := q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
				SettingsProfileID: template.SettingsProfileID,
				TermBaseID:        g.ID,
				SourceLanguage:    g.SourceLanguage,
				TargetLanguage:    tgt,
				Mode:              "read",
				Priority:          1,
			})
			if err != nil {
				log.Printf("  ⚠ Failed to assign term base %d: %v", g.ID, err)
			} else {
				log.Printf("  ✓ Template term assignment: %s → %s (term base: %s)", g.SourceLanguage, tgt, g.Name)
			}
		}
	}
}

func createTemplateWithAssignments(ctx context.Context, q *db.Queries, tenantID, engineID, tmID int64) {
	profile, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: 0.50,
		MtOnUpload:     true,
		TmOnUpload:     true,
	})
	if err != nil {
		log.Fatalf("Failed to create settings profile for template: %v", err)
	}

	templateProjectName := "Default Template"
	tmpl, err := q.CreateTemplate(ctx, db.CreateTemplateParams{
		Name:              "Default Template",
		TenantID:          tenantID,
		SettingsProfileID: profile.ID,
		ProjectName:       &templateProjectName,
	})
	if err != nil {
		log.Fatalf("Failed to create template: %v", err)
	}
	log.Printf("✓ Template created: %s (ID: %d, profile: %d)", tmpl.Name, tmpl.ID, profile.ID)

	termBases, _ := q.ListTermBases(ctx, tenantID)
	termBaseByLang := make(map[string]int64)
	for _, g := range termBases {
		for _, tgt := range g.TargetLanguages {
			termBaseByLang[tgt] = g.ID
		}
	}

	targetLangs := []string{"es", "fr", "de"}
	// TM assignment is per-TM (its languages come from the TM's own
	// metadata) — one row. MT + Term stay per-(source,target).
	q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: profile.ID, TmID: tmID,
		Mode: "read_write", Priority: 1,
	})
	if router, ok := ensureMTRouter(ctx, q, tenantID, demodata.MTRouterName, engineID); ok {
		pointProfileAtMTRouter(ctx, q, profile.ID, router.ID)
	}
	for _, lang := range targetLangs {
		if gbID, ok := termBaseByLang[lang]; ok {
			q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
				SettingsProfileID: profile.ID, TermBaseID: gbID,
				SourceLanguage: "en", TargetLanguage: lang,
				Mode: "read", Priority: 1,
			})
		}
		log.Printf("  ✓ Template assignments: en → %s (MT + Term Base)", lang)
	}
	log.Printf("  ✓ Template TM assignment: Default TM (per-TM)")
}

func createProject(ctx context.Context, q *db.Queries, tenantID, userID int64, templates []db.Template) {
	profile, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: 0.50,
		MtOnUpload:     true,
		TmOnUpload:     true,
	})
	if err != nil {
		log.Fatalf("Failed to create settings profile for project: %v", err)
	}

	targetLangs := []string{"es", "fr", "de"}
	project, err := q.CreateProject(ctx, db.CreateProjectParams{
		Name:              "Demo Project",
		SourceLanguage:    "en",
		TargetLanguages:   targetLangs,
		TenantID:          tenantID,
		SettingsProfileID: profile.ID,
		// Status is a non-null enum (`project_status` from migration 025).
		// SQLC can't elide it from the INSERT despite the column having
		// `DEFAULT 'in_progress'` — the Go zero value `""` would fail
		// the enum check. Pass the constant explicitly so a fresh
		// `make reset` succeeds end-to-end.
		Status:    db.ProjectStatusInProgress,
		CreatedBy: auth.UserIDPtr(userID),
	})
	if err != nil {
		log.Fatalf("Failed to create project: %v", err)
	}
	if len(templates) > 0 {
		q.SetProjectTemplate(ctx, db.SetProjectTemplateParams{
			ID: project.ID, TemplateID: &templates[0].ID, TenantID: tenantID,
		})
	}

	healProjectAssignments(ctx, q, project, tenantID)

	// Demo history: the seed writes rows directly, not
	// through the HTTP handlers, so the History tab would be empty on a fresh
	// seed without these. Attribute them to the demo identity when it exists.
	var seedActor *int64
	seedActorName := ""
	if u, err := q.GetUserByEmail(ctx, seedid.OwnerEmail); err == nil {
		seedActor = &u.ID
		seedActorName = u.Email
	}
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: project.ID,
		TenantID:  tenantID,
		Type:      projectevents.TypeProjectCreated,
		ActorID:   seedActor,
		ActorName: seedActorName,
		Details: map[string]any{
			"name":             project.Name,
			"source_language":  project.SourceLanguage,
			"target_languages": project.TargetLanguages,
		},
	})
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: project.ID,
		TenantID:  tenantID,
		Type:      projectevents.TypeSettingsUpdated,
		ActorID:   seedActor,
		ActorName: seedActorName,
		Details: map[string]any{
			"changes": map[string]projectevents.Change{
				"fuzzy_threshold": {From: 0.75, To: 0.50},
				"mt_on_upload":    {From: false, To: true},
			},
		},
	})

	log.Printf("✓ Project created: %s (ID: %d, profile: %d)", project.Name, project.ID, profile.ID)
}

// healProjectAssignments ensures project-level TM/MT/term/workflow assignments exist.
func healProjectAssignments(ctx context.Context, q *db.Queries, project db.Project, tenantID int64) {
	spID := project.SettingsProfileID
	targetLangs := []string{"es", "fr", "de"}

	engines, _ := q.ListMTEngines(ctx, tenantID)
	tms, _ := q.ListTMCollections(ctx, tenantID)
	termBases, _ := q.ListTermBases(ctx, tenantID)
	termBaseByLang := make(map[string]int64)
	for _, g := range termBases {
		for _, tgt := range g.TargetLanguages {
			termBaseByLang[tgt] = g.ID
		}
	}

	existingTMs, _ := q.ListSettingsTMAssignments(ctx, spID)
	if len(existingTMs) == 0 && len(tms) > 0 {
		// One per-TM assignment; the TM's declared targets cover the pairs.
		q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
			SettingsProfileID: spID, TmID: tms[0].ID,
			Mode: "read_write", Priority: 1,
		})
		log.Printf("  ✓ Project TM assignment created (Default TM, per-TM)")
	}

	if len(engines) > 0 {
		if router, ok := ensureMTRouter(ctx, q, tenantID, demodata.MTRouterName, engines[0].ID); ok {
			pointProfileAtMTRouter(ctx, q, spID, router.ID)
		}
	}

	existingTerms, _ := q.ListSettingsTermAssignments(ctx, spID)
	if len(existingTerms) == 0 && len(termBases) > 0 {
		for _, lang := range targetLangs {
			if gbID, ok := termBaseByLang[lang]; ok {
				q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
					SettingsProfileID: spID, TermBaseID: gbID,
					SourceLanguage: "en", TargetLanguage: lang,
					Mode: "read", Priority: 1,
				})
			}
		}
		log.Printf("  ✓ Project term assignments created")
	}

	// Heal project settings (including QA profile assignment)
	profile, err := q.GetSettingsProfile(ctx, spID)
	if err != nil {
		return
	}
	qaProfileID := profile.QaProfileID
	if qaProfileID == nil {
		qaProfiles, _ := q.ListQAProfiles(ctx, tenantID)
		if len(qaProfiles) > 0 {
			qaProfileID = &qaProfiles[0].ID
			log.Printf("  ✓ Project QA profile assigned: %s (ID: %d)", qaProfiles[0].Name, qaProfiles[0].ID)
		}
	}
	needsUpdate := !profile.MtOnUpload || !profile.TmOnUpload || qaProfileID != profile.QaProfileID
	if needsUpdate {
		q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
			ID: spID, FuzzyThreshold: 0.5, MtOnUpload: true, TmOnUpload: true, QaProfileID: qaProfileID,
		})
		log.Printf("  ✓ Project settings: fuzzy=0.5, mt_on_upload=true, tm_on_upload=true, qa_profile=%v", qaProfileID)
	}

	existingSteps, _ := q.ListSettingsWorkflowSteps(ctx, spID)
	if len(existingSteps) == 0 {
		createDefaultSteps(ctx, q, tenantID, spID)
	}
}

func seedTMEntries(ctx context.Context, q *db.Queries, tmID int64) {
	type tmEntry struct {
		srcLang, tgtLang, src, tgt string
	}
	entries := []tmEntry{
		// ── Exact matches (100%) ──────────────────────────────────────
		{"en", "es", "We thank you for your patience.", "Le agradecemos su paciencia."},
		{"en", "es", "Reset your password.", "Restablezca su contraseña."},
		{"en", "es", "Enter your email address.", "Introduzca su dirección de correo electrónico."},
		{"en", "es", "Enable Two-Factor Authentication.", "Active la autenticación de dos factores."},
		{"en", "es", "We appreciate your patience.", "Apreciamos su paciencia."},

		{"en", "fr", "We thank you for your patience.", "Nous vous remercions de votre patience."},
		{"en", "fr", "Reset your password.", "Réinitialisez votre mot de passe."},
		{"en", "fr", "Enter your email address.", "Saisissez votre adresse e-mail."},
		{"en", "fr", "Enable Two-Factor Authentication.", "Activez l'authentification à deux facteurs."},
		{"en", "fr", "We appreciate your patience.", "Nous apprécions votre patience."},

		{"en", "de", "We thank you for your patience.", "Wir danken Ihnen für Ihre Geduld."},
		{"en", "de", "Reset your password.", "Setzen Sie Ihr Passwort zurück."},
		{"en", "de", "Enter your email address.", "Geben Sie Ihre E-Mail-Adresse ein."},
		{"en", "de", "Enable Two-Factor Authentication.", "Aktivieren Sie die Zwei-Faktor-Authentifizierung."},
		{"en", "de", "We appreciate your patience.", "Wir schätzen Ihre Geduld."},

		// ── 95-99% fuzzy matches ──────────────────────────────────────
		{"en", "es", "• Enter your email address and we'll send a link to reset your password", "• Introduzca su dirección de correo electrónico y le enviaremos un enlace para restablecer su contraseña"},
		{"en", "fr", "• Enter your email address and we'll send a link to reset your password", "• Saisissez votre adresse e-mail et nous vous enverrons un lien pour réinitialiser votre mot de passe"},
		{"en", "de", "• Enter your email address and we'll send a link to reset your password", "• Geben Sie Ihre E-Mail-Adresse ein und wir senden Ihnen einen Link zum Zurücksetzen Ihres Passworts"},
		{"en", "es", "• Enter your email addres and we'll send you a link to reset your password", "• Introduzca su dirección de correo y le enviaremos un enlace para restablecer su contraseña"},
		{"en", "fr", "• Enter your email addres and we'll send you a link to reset your password", "• Entrez votre adresse e-mail et nous vous enverrons un lien pour réinitialiser votre mot de passe"},
		{"en", "de", "• Enter your email addres and we'll send you a link to reset your password", "• Geben Sie Ihre E-Mail-Adresse ein und wir senden Ihnen einen Link zum Zurücksetzen Ihres Kennworts"},

		// ── 85-94% fuzzy matches ──────────────────────────────────────
		{"en", "es", "We have taken the immediate steps.", "Hemos tomado las medidas inmediatas."},
		{"en", "fr", "We have taken the immediate steps.", "Nous avons pris les mesures immédiates."},
		{"en", "de", "We have taken the immediate steps.", "Wir haben die sofortigen Maßnahmen ergriffen."},
		{"en", "es", "Online fraud and attempts.", "Fraude en línea e intentos."},
		{"en", "fr", "Online fraud and attempts.", "Fraude en ligne et tentatives."},
		{"en", "de", "Online fraud and attempts.", "Online-Betrug und Versuche."},
		{"en", "es", "To protect you and your datas.", "Para protegerle a usted y sus datos."},
		{"en", "fr", "To protect you and your datas.", "Pour vous protéger, vous et vos données."},
		{"en", "de", "To protect you and your datas.", "Um Sie und Ihre Daten zu schützen."},

		// ── 75-84% fuzzy matches ──────────────────────────────────────
		{"en", "es", "Confirm your identity now.", "Confirme su identidad ahora."},
		{"en", "fr", "Confirm your identity now.", "Confirmez votre identité maintenant."},
		{"en", "de", "Confirm your identity now.", "Bestätigen Sie jetzt Ihre Identität."},
		{"en", "es", "We apologize for any inconvenience.", "Lamentamos cualquier inconveniente."},
		{"en", "fr", "We apologize for any inconvenience.", "Nous nous excusons pour tout désagrément."},
		{"en", "de", "We apologize for any inconvenience.", "Wir entschuldigen uns für etwaige Unannehmlichkeiten."},
		{"en", "es", "We recently became fully aware.", "Recientemente nos dimos plena cuenta."},
		{"en", "fr", "We recently became fully aware.", "Nous en avons récemment pris pleinement conscience."},
		{"en", "de", "We recently became fully aware.", "Wir wurden vor kurzem vollständig darauf aufmerksam."},
		{"en", "es", "Enhanced data protection.", "Protección de datos mejorada."},
		{"en", "fr", "Enhanced data protection.", "Protection des données renforcée."},
		{"en", "de", "Enhanced data protection.", "Verbesserter Datenschutz."},
		{"en", "es", "In response to this.", "En respuesta a esto."},
		{"en", "fr", "In response to this.", "En réponse à cela."},
		{"en", "de", "In response to this.", "Als Reaktion darauf."},
		{"en", "es", "Your report of a concern received.", "Su reporte de preocupación recibido."},
		{"en", "fr", "Your report of a concern received.", "Votre signalement de préoccupation reçu."},
		{"en", "de", "Your report of a concern received.", "Ihr Bericht über ein Anliegen erhalten."},
		{"en", "es", "We kindly ask you.", "Le pedimos amablemente."},
		{"en", "fr", "We kindly ask you.", "Nous vous demandons aimablement."},
		{"en", "de", "We kindly ask you.", "Wir bitten Sie freundlich."},
		{"en", "es", "Following actions taken.", "Acciones siguientes tomadas."},
		{"en", "fr", "Following actions taken.", "Actions suivantes prises."},
		{"en", "de", "Following actions taken.", "Folgende Maßnahmen ergriffen."},

		// ── 50-74% fuzzy matches ──────────────────────────────────────
		{"en", "es", "We have been looking into this matter.", "Hemos estado investigando este asunto."},
		{"en", "fr", "We have been looking into this matter.", "Nous avons examiné cette affaire."},
		{"en", "de", "We have been looking into this matter.", "Wir haben diese Angelegenheit untersucht."},
		{"en", "es", "Change your passwords.", "Cambie sus contraseñas."},
		{"en", "fr", "Change your passwords.", "Changez vos mots de passe."},
		{"en", "de", "Change your passwords.", "Ändern Sie Ihre Passwörter."},
		{"en", "es", "We have taken immediate action.", "Hemos tomado acción inmediata."},
		{"en", "fr", "We have taken immediate action.", "Nous avons pris des mesures immédiates."},
		{"en", "de", "We have taken immediate action.", "Wir haben sofortige Maßnahmen ergriffen."},
		{"en", "es", "We will send you a verification code.", "Le enviaremos un código de verificación."},
		{"en", "fr", "We will send you a verification code.", "Nous vous enverrons un code de vérification."},
		{"en", "de", "We will send you a verification code.", "Wir senden Ihnen einen Bestätigungscode."},
		{"en", "es", "Account and Wallet Balance", "Saldo de cuenta y billetera"},
		{"en", "fr", "Account and Wallet Balance", "Solde du compte et du portefeuille"},
		{"en", "de", "Account and Wallet Balance", "Konto- und Wallet-Guthaben"},
	}

	for _, e := range entries {
		_, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID:           tmID,
			SourceLanguage: e.srcLang,
			TargetLanguage: e.tgtLang,
			SourceText:     e.src,
			TargetText:     e.tgt,
			CreatedBy:      "seed",
			TuID:           "",
		})
		if err != nil {
			log.Fatalf("Failed to seed TM entry %q: %v", e.src, err)
		}
	}
	log.Printf("✓ TM entries: %d entries seeded (en→es/fr/de, exact + fuzzy)", len(entries))
}

// healQAProfile ensures a "Standard QA" profile exists with all checks enabled.
func healQAProfile(ctx context.Context, q *db.Queries, tenantID int64) {
	profiles, err := q.ListQAProfiles(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check QA profiles: %v", err)
		return
	}
	if len(profiles) > 0 {
		log.Printf("✓ QA Profile: %s (ID: %d)", profiles[0].Name, profiles[0].ID)
		return
	}

	profile, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{
		TenantID:    tenantID,
		Name:        "Standard QA",
		Description: "All checks enabled with sensible defaults",
	})
	if err != nil {
		log.Printf("⚠ Failed to create QA profile: %v", err)
		return
	}

	checks := []struct {
		checkType string
		severity  string
		config    string
	}{
		{"tag_checks", "error", `{"missing":true,"extra":true,"unclosed":true,"unopened":true}`},
		{"number_consistency", "error", "{}"},
		{"empty_target", "error", "{}"},
		{"terminology", "warning", "{}"},
		{"forbidden_terms", "error", "{}"},
		{"consistency", "warning", "{}"},
		{"punctuation", "warning", "{}"},
	}
	for _, c := range checks {
		_, err := q.UpsertQARule(ctx, db.UpsertQARuleParams{
			ProfileID: profile.ID,
			CheckType: c.checkType,
			Enabled:   true,
			Severity:  c.severity,
			Config:    []byte(c.config),
		})
		if err != nil {
			log.Printf("  ⚠ Failed to create QA rule %s: %v", c.checkType, err)
		}
	}
	log.Printf("✓ QA Profile created: %s (ID: %d) with %d rules", profile.Name, profile.ID, len(checks))
}

// healTemplateQAProfile ensures the existing template has a QA profile assigned.
func healTemplateQAProfile(ctx context.Context, q *db.Queries, tmpl db.Template, tenantID int64) {
	sp, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
	if err != nil {
		return
	}
	if sp.QaProfileID != nil {
		log.Printf("  ✓ Template QA profile: ID %d", *sp.QaProfileID)
		return
	}

	profiles, _ := q.ListQAProfiles(ctx, tenantID)
	if len(profiles) == 0 {
		return
	}

	_, err = q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:             sp.ID,
		FuzzyThreshold: sp.FuzzyThreshold,
		MtOnUpload:     sp.MtOnUpload,
		TmOnUpload:     sp.TmOnUpload,
		QaProfileID:    &profiles[0].ID,
	})
	if err != nil {
		log.Printf("  ⚠ Failed to assign QA profile to template: %v", err)
		return
	}
	log.Printf("  ✓ Template QA profile assigned: %s", profiles[0].Name)
}

// --- Demo file seeding ---
// Inserts demo.txt segments + pre-computed translations directly into the DB.
// No HTTP/S3 needed — storage_path is a placeholder (file won't be exportable but editor works).

var demoSegments = []string{
	"We thank you for your patience.",
	"We have been looking into this activity.",
	"In response to.",
	"We have taken immediate steps.",
	"Following actions.",
	"To protect you and your data.",
	"Temporarily locked.",
	"We kindly ask.",
	"Reset your password.",
	"Instructions below:",
	"• Select 'Sign in' at the top of the homepage",
	"• Click the 'Forgotten your password?' link",
	"• Enter your email address and follow the instructions",
	"We will monitor.",
	"We apologize.",
	"Thank you for your cooperation.",
	"Please do not reply.",
	"This email was sent to.",
	"For more information.",
	"If you need further assistance.",
	"Please contact our support team.",
	"Our team is available.",
	"Please review the following:\n\t• Account settings\n\t• Security options\n\nContact us for help.",
	// Whitespace test segments — various Unicode whitespace and invisible characters
	"Zero\u200Bwidth\u200Bspace between words",   // U+200B zero-width space
	"Soft\u00ADhyphen\u00ADin\u00ADwords",        // U+00AD soft hyphen
	"En\u2002space\u2003em\u2003space\u2009thin", // U+2002 en space, U+2003 em space, U+2009 thin space
	"BOM\uFEFF character and ZWNJ\u200C here",    // U+FEFF BOM, U+200C ZWNJ
	"Ideographic\u3000space\u3000CJK",            // U+3000 ideographic space
	// XLIFF tag test segments — inline tags rendered as non-editable chips in the editor
	`Click here to continue.`,             // plain text for tag test (paired with rich text below)
	`Your order number is {orderNumber}.`, // plain text for tag test
	`Press the Submit button.`,            // plain text for tag test
}

// demoSegmentsRichText maps position → source_rich_text for segments that have inline XLIFF tags.
// Only set for segments where the rich text differs from the plain text.
// Indices correspond to the last 3 entries in demoSegments (XLIFF tag test segments).
var demoSegmentsRichText = map[int]string{
	28: `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue.`,
	29: `Your order number is <ph id="2">{orderNumber}</ph>.`,
	30: `Press the <bpt id="3">&lt;i&gt;</bpt>Submit<ept id="3">&lt;/i&gt;</ept> button.`,
}

// neighborHashesFor returns md5(prev_source)/md5(next_source) for a demo
// segment's document neighbors (md5("") at a boundary) — identical to
// pipeline.buildNeighborHashes. Used to make the seeded "Reset your password."
// TM entry a genuine 101% in-context exact match against the demo file.
func neighborHashesFor(target string) (prevHash, nextHash string) {
	for i, s := range demoSegments {
		if s != target {
			continue
		}
		prev, next := "", ""
		if i > 0 {
			prev = demoSegments[i-1]
		}
		if i < len(demoSegments)-1 {
			next = demoSegments[i+1]
		}
		return translationmemory.ContextHash(prev), translationmemory.ContextHash(next)
	}
	return translationmemory.ContextHash(""), translationmemory.ContextHash("")
}

// Pre-computed translations for key segments that need TM matches.
// Only segments with TM matches are pre-filled; the rest get MT translations.
type seedTranslation struct {
	lang     string
	text     string // plain text (no XLIFF tags)
	richText string // XLIFF-tagged text (empty if no tags)
	engine   string
	score    *float32
	origin   string
	status   string
}

var f32 = ptr.To[float32]

// tmTranslations maps source_text → per-language translations (TM matches + MT fallbacks).
var tmTranslations = map[string][]seedTranslation{
	"We thank you for your patience.": {
		{lang: "es", text: "Le agradecemos su paciencia.", engine: "tm", score: f32(1.0), origin: "tm", status: "approved"},
		{lang: "fr", text: "Nous vous remercions de votre patience.", engine: "tm", score: f32(1.0), origin: "tm", status: "approved"},
		{lang: "de", text: "Wir danken Ihnen für Ihre Geduld.", engine: "tm", score: f32(1.0), origin: "tm", status: "approved"},
	},
	// Demo: 101% in-context exact match. score 1.01 → tm_match_percent
	// 101 (the score→percent scaler rounds 1.01*100 to 101). The matching
	// context is stamped onto this entry's TM rows in healDemoFile, so the
	// chip is a genuine context match (re-running pre-translate reproduces 101).
	"Reset your password.": {
		{lang: "es", text: "Restablezca su contraseña.", engine: "tm", score: f32(1.01), origin: "tm", status: "approved"},
		{lang: "fr", text: "Réinitialisez votre mot de passe.", engine: "tm", score: f32(1.01), origin: "tm", status: "approved"},
		{lang: "de", text: "Setzen Sie Ihr Passwort zurück.", engine: "tm", score: f32(1.01), origin: "tm", status: "approved"},
	},
	"We have been looking into this activity.": {
		{lang: "es", text: "Hemos estado investigando este asunto.", engine: "tm", score: f32(0.66), origin: "tm", status: "needs_review"},
		{lang: "fr", text: "Nous avons examiné cette activité.", engine: "tm", score: f32(0.66), origin: "tm", status: "needs_review"},
		{lang: "de", text: "Wir haben diese Aktivität untersucht.", engine: "tm", score: f32(0.66), origin: "tm", status: "needs_review"},
	},
	"In response to.": {
		{lang: "es", text: "En respuesta a esto.", engine: "tm", score: f32(0.79), origin: "tm", status: "needs_review"},
		{lang: "fr", text: "En réponse à cela.", engine: "tm", score: f32(0.79), origin: "tm", status: "needs_review"},
		{lang: "de", text: "Als Reaktion darauf.", engine: "tm", score: f32(0.79), origin: "tm", status: "needs_review"},
	},
	"We have taken immediate steps.": {
		{lang: "es", text: "Hemos tomado las medidas inmediatas.", engine: "tm", score: f32(0.91), origin: "tm", status: "needs_review"},
		{lang: "fr", text: "Nous avons pris des mesures immédiates.", engine: "tm", score: f32(0.91), origin: "tm", status: "needs_review"},
		{lang: "de", text: "Wir haben die sofortigen Maßnahmen ergriffen.", engine: "tm", score: f32(0.91), origin: "tm", status: "needs_review"},
	},
	"Following actions.": {
		{lang: "es", text: "Acciones siguientes tomadas.", engine: "tm", score: f32(0.75), origin: "tm", status: "needs_review"},
		{lang: "fr", text: "Actions suivantes prises.", engine: "tm", score: f32(0.75), origin: "tm", status: "needs_review"},
		{lang: "de", text: "Folgende Maßnahmen ergriffen.", engine: "tm", score: f32(0.75), origin: "tm", status: "needs_review"},
	},
	"To protect you and your data.": {
		{lang: "es", text: "Para protegerle a usted y sus datos.", engine: "tm", score: f32(0.89), origin: "tm", status: "needs_review"},
		{lang: "fr", text: "Pour vous protéger et protéger vos données.", engine: "tm", score: f32(0.89), origin: "tm", status: "needs_review"},
		{lang: "de", text: "Um Sie und Ihre Daten zu schützen.", engine: "tm", score: f32(0.89), origin: "tm", status: "needs_review"},
	},
	"We kindly ask.": {
		{lang: "es", text: "Le pedimos amablemente.", engine: "tm", score: f32(0.78), origin: "tm", status: "needs_review"},
		{lang: "fr", text: "Nous vous prions de bien vouloir.", engine: "tm", score: f32(0.78), origin: "tm", status: "needs_review"},
		{lang: "de", text: "Wir bitten Sie freundlich.", engine: "tm", score: f32(0.78), origin: "tm", status: "needs_review"},
	},
	"• Click the 'Forgotten your password?' link": {
		{lang: "es", text: "• Haga clic en el enlace '¿Olvidó su contraseña?'", engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "fr", text: "• Cliquez sur le lien 'Mot de passe oublié ?'", engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "de", text: "• Klicken Sie auf den Link 'Passwort vergessen?'", engine: "libretranslate", origin: "mt", status: "needs_review"},
	},
	"• Enter your email address and follow the instructions": {
		{lang: "es", text: "• Ingrese su dirección de correo electrónico y siga las instrucciones", engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "fr", text: "• Entrez votre adresse e-mail et suivez les instructions", engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "de", text: "• Geben Sie Ihre E-Mail-Adresse ein und folgen Sie den Anweisungen", engine: "libretranslate", origin: "mt", status: "needs_review"},
	},
	// XLIFF tag test segments — MT translations with inline tags preserved.
	// text = plain text (for display/search), richText = XLIFF-tagged (for round-trip/chips).
	"Click here to continue.": {
		{lang: "es", text: "Haga clic aquí para continuar.", richText: `Haga clic <bpt id="1">&lt;b&gt;</bpt>aquí<ept id="1">&lt;/b&gt;</ept> para continuar.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "fr", text: "Cliquez ici pour continuer.", richText: `Cliquez <bpt id="1">&lt;b&gt;</bpt>ici<ept id="1">&lt;/b&gt;</ept> pour continuer.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "de", text: "Klicken Sie hier um fortzufahren.", richText: `Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> um fortzufahren.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
	},
	"Your order number is {orderNumber}.": {
		{lang: "es", text: "Su número de pedido es {orderNumber}.", richText: `Su número de pedido es <ph id="2">{orderNumber}</ph>.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "fr", text: "Votre numéro de commande est {orderNumber}.", richText: `Votre numéro de commande est <ph id="2">{orderNumber}</ph>.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "de", text: "Ihre Bestellnummer ist {orderNumber}.", richText: `Ihre Bestellnummer ist <ph id="2">{orderNumber}</ph>.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
	},
	"Press the Submit button.": {
		{lang: "es", text: "Presione el botón Enviar.", richText: `Presione el botón <bpt id="3">&lt;i&gt;</bpt>Enviar<ept id="3">&lt;/i&gt;</ept>.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "fr", text: "Appuyez sur le bouton Soumettre.", richText: `Appuyez sur le bouton <bpt id="3">&lt;i&gt;</bpt>Soumettre<ept id="3">&lt;/i&gt;</ept>.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
		{lang: "de", text: "Drücken Sie die Taste Senden.", richText: `Drücken Sie die Taste <bpt id="3">&lt;i&gt;</bpt>Senden<ept id="3">&lt;/i&gt;</ept>.`, engine: "libretranslate", origin: "mt", status: "needs_review"},
	},
}

// mtFallback generates a simple MT-style translation for segments without TM matches.
func mtFallback(source, lang string) string {
	// Simple prefix to make translations identifiable in tests
	switch lang {
	case "es":
		return "[ES] " + source
	case "fr":
		return "[FR] " + source
	case "de":
		return "[DE] " + source
	}
	return source
}

func healDemoFile(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	project, ok := findDemoProject(ctx, q, tenantID)
	if !ok {
		log.Printf("⚠ Skipping demo file — %q not found", demoProjectName)
		return
	}

	// Credited as the confirmer of the approved demo segments. Nil is
	// tolerated: the rows still read as approved, just unattributed.
	var demoEditor *int64
	if u, err := q.GetUserByEmail(ctx, seedid.OwnerEmail); err == nil {
		demoEditor = &u.ID
	}

	// Always recreate demo file — ensures seed data changes (new segments, updated
	// translations) are applied without manual DELETE. Seed data is disposable.
	files, _ := q.ListFilesByProject(ctx, project.ID)
	for _, f := range files {
		if f.Name == "demo.txt" {
			log.Printf("↻ Recreating demo file (ensures seed data is fresh)")
			_, _ = pool.Exec(ctx, "DELETE FROM files WHERE id = $1", f.ID)
			break
		}
	}

	targetLangs := []string{"es", "fr", "de"}

	// storage_path is a placeholder — the file is not S3-exportable.
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID:    project.ID,
		Name:         "demo.txt",
		Format:       "txt",
		StoragePath:  fmt.Sprintf("seed/demo-%d.txt", project.ID),
		SegmentCount: int32(len(demoSegments)),
	})
	if err != nil {
		log.Printf("⚠ Failed to create demo file: %v", err)
		return
	}

	totalWords := 0
	for pos, text := range demoSegments {
		wc := wordcount.CountWords(text)
		totalWords += wc

		var segID int64
		richText := demoSegmentsRichText[pos] // "" if not set
		var richTextPtr *string
		if richText != "" {
			richTextPtr = &richText
		}
		err := pool.QueryRow(ctx,
			"INSERT INTO segments (file_id, source_text, source_rich_text, segment_key, position, word_count) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
			file.ID, text, richTextPtr, fmt.Sprintf("%d", pos+1), pos, wc,
		).Scan(&segID)
		if err != nil {
			log.Printf("⚠ Failed to insert segment %d: %v", pos, err)
			continue
		}

		for _, lang := range targetLangs {
			var activeText, activeRichText, origin, status string
			var tmScore *float32

			if translations, ok := tmTranslations[text]; ok {
				for _, t := range translations {
					if t.lang == lang {
						activeText = t.text
						activeRichText = t.richText
						tmScore = t.score
						origin = t.origin
						status = t.status
						break
					}
				}
			}
			if activeText == "" {
				activeText = mtFallback(text, lang)
				origin = "mt"
				// An MT-filled, unreviewed row sits at needs_review;
				// origin='mt' + no edited_by makes it derive as
				// "pre_translated" in the UI.
				status = "needs_review"
			}

			var richTextPtr *string
			if activeRichText != "" {
				richTextPtr = &activeRichText
			}

			// tm_match_percent is smallint 0..101; seed scores are
			// 0.0..1.0 reals scaled to an integer percentage. The 101%
			// in-context-exact tier uses score 1.01 → 101. NULL for
			// MT rows (no TM match).
			var tmPercent *int32
			if tmScore != nil {
				p := int32(*tmScore*100 + 0.5)
				tmPercent = &p
			}

			// Provenance lives on origin + tm_match_percent. The
			// visible target is active_text / active_rich_text.
			// `approved` is only reachable through a human confirm, and that
			// path always stamps edited_by (UpdateTranslationHuman).
			var editedBy *int64
			if status == "approved" {
				editedBy = demoEditor
			}

			_, err := pool.Exec(ctx,
				`INSERT INTO translations (segment_id, target_language, status, active_text, active_rich_text, origin, tm_match_percent, edited_by)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				segID, lang, status, activeText, richTextPtr, origin, tmPercent, editedBy,
			)
			if err != nil {
				log.Printf("⚠ Failed to insert translation seg=%d lang=%s: %v", segID, lang, err)
			}
		}
	}

	// 2b. Demo: stamp the "Reset your password." TM entries with the
	// context hashes of its demo-file neighbors so it's a genuine 101%
	// in-context exact match (the pre-filled translation above already shows
	// 101%; this makes re-running pre-translate reproduce it). Scoped to ALL of
	// the tenant's TMs (not a guessed one) so whichever TM the demo project has
	// assigned gets the context. Idempotent.
	prevHash, nextHash := neighborHashesFor("Reset your password.")
	if _, err := pool.Exec(ctx,
		`UPDATE translation_memory tm SET prev_source_hash = $1, next_source_hash = $2
		 FROM tm_collections c
		 WHERE tm.tm_id = c.id AND c.tenant_id = $3
		   AND tm.source_language = 'en' AND tm.source_text = $4`,
		prevHash, nextHash, tenantID, "Reset your password.",
	); err != nil {
		log.Printf("⚠ Failed to stamp 101%% context on TM entry: %v", err)
	}

	for _, lang := range targetLangs {
		_, err := q.CreateJob(ctx, db.CreateJobParams{
			FileID:         file.ID,
			TargetLanguage: lang,
			WordCount:      int32(totalWords),
		})
		if err != nil && !strings.Contains(err.Error(), "duplicate key") {
			log.Printf("⚠ Failed to create job for %s: %v", lang, err)
		}
	}

	// Mark jobs as ready for workflow (not 'completed' — workflow tests need active steps)
	_, _ = pool.Exec(ctx, "UPDATE jobs SET status = 'needs_attention' WHERE file_id = $1", file.ID)

	log.Printf("✓ Demo file: %s (%d segments, %d words, %d jobs)", file.Name, len(demoSegments), totalWords, len(targetLangs))
}

// healWorkflowSteps ensures the default template has workflow steps and demo jobs have step assignments.
func healWorkflowSteps(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	// Step names display in Title Case. Rename any
	// lower-case names across every profile in the tenant — the demo project,
	// the template, and any other profile. Updates only `name`, so ids (and their job_step_assignments) and every other
	// column are untouched. Idempotent: a second run matches nothing.
	if tag, err := pool.Exec(ctx,
		`UPDATE workflow_steps ws
		 SET name = CASE lower(ws.name) WHEN 'review' THEN 'Review' ELSE 'Translation' END
		 FROM settings_profiles sp
		 WHERE sp.id = ws.settings_profile_id AND sp.tenant_id = $1
		   AND ws.name IN ('translate', 'translation', 'review')`,
		tenantID,
	); err != nil {
		log.Printf("⚠ Could not Title-Case workflow step names: %v", err)
	} else if n := tag.RowsAffected(); n > 0 {
		log.Printf("  ✓ Workflow step names Title-Cased: %d step(s)", n)
	}

	templates, err := q.ListTemplates(ctx, tenantID)
	if err != nil || len(templates) == 0 {
		return
	}
	spID := templates[0].SettingsProfileID

	// Check if workflow steps exist
	steps, err := q.ListSettingsWorkflowSteps(ctx, spID)
	if err != nil {
		log.Printf("⚠ Could not check workflow steps: %v", err)
		return
	}
	if len(steps) == 0 {
		createDefaultSteps(ctx, q, tenantID, spID)
		if steps, err = q.ListSettingsWorkflowSteps(ctx, spID); err != nil || len(steps) == 0 {
			log.Printf("⚠ Default workflow steps missing after create: %v", err)
			return
		}
	} else {
		log.Printf("✓ Workflow steps: %d steps on settings profile %d", len(steps), spID)
	}

	// Heal: ensure demo jobs have step assignments
	user, err := q.GetUserByEmail(ctx, seedid.OwnerEmail)
	if err != nil {
		return
	}

	// Rebuild step assignments: delete stale ones (pointing to deleted workflow steps), recreate fresh.
	// This heals state from previous seeds that created workflow steps with different IDs.
	_, _ = pool.Exec(ctx, `
		DELETE FROM job_step_assignments jsa
		WHERE jsa.job_id IN (
			SELECT j.id FROM jobs j JOIN files f ON f.id = j.file_id
			JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1
		)
	`, tenantID)
	// Force-clear current_step_id so it can be re-assigned below
	_, _ = pool.Exec(ctx, `
		UPDATE jobs j SET current_step_id = NULL
		FROM files f JOIN projects p ON p.id = f.project_id
		WHERE f.id = j.file_id AND p.tenant_id = $1
	`, tenantID)
	// Every tenant job with the settings profile of ITS project: a job's step
	// assignments must hang off its own project's workflow_steps rows — that is
	// what upload does and what the workflow-edit safety rule reads
	type jobProfile struct{ jobID, profileID int64 }
	var jobs []jobProfile
	jRows, _ := pool.Query(ctx, `
		SELECT j.id, p.settings_profile_id FROM jobs j JOIN files f ON f.id = j.file_id
		JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 ORDER BY j.id
	`, tenantID)
	if jRows != nil {
		for jRows.Next() {
			var jp jobProfile
			if jRows.Scan(&jp.jobID, &jp.profileID) == nil {
				jobs = append(jobs, jp)
			}
		}
		jRows.Close()
	}
	if len(jobs) == 0 {
		log.Printf("  ✓ All jobs have workflow assignments")
		return
	}

	stepsByProfile := map[int64][]db.WorkflowStep{}
	for _, jp := range jobs {
		projectSteps, ok := stepsByProfile[jp.profileID]
		if !ok {
			var err error
			if projectSteps, err = q.ListSettingsWorkflowSteps(ctx, jp.profileID); err != nil {
				log.Printf("  ⚠ Could not list steps for profile %d: %v", jp.profileID, err)
				continue
			}
			stepsByProfile[jp.profileID] = projectSteps
		}
		var firstAssignID int64
		for i, step := range projectSteps {
			status := "pending"
			var assignedTo *int64
			if i == 0 {
				status = "in_progress"
				if step.StepType != "ai" {
					assignedTo = &user.ID
				}
			}
			a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
				JobID: jp.jobID, WorkflowStepID: step.ID, AssignedTo: assignedTo, Status: status,
			})
			if err != nil {
				log.Printf("  ⚠ Failed to create step assignment for job %d: %v", jp.jobID, err)
				continue
			}
			if i == 0 {
				firstAssignID = a.ID
			}
		}
		if firstAssignID > 0 {
			_ = q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jp.jobID, CurrentStepID: &firstAssignID})
		}
	}
	log.Printf("  ✓ Workflow assignments created for %d jobs on their projects' steps", len(jobs))
}

// healNotifications ensures seed user has notification preferences and demo jobs have deadlines.
func healNotifications(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	user, err := q.GetUserByEmail(ctx, seedid.OwnerEmail)
	if err != nil {
		return
	}

	if created, err := demodata.EnsureNotificationRules(ctx, q, tenantID); err != nil {
		log.Printf("⚠ Failed to ensure org notification rules: %v", err)
	} else if created {
		log.Println("✓ Org notification rules created (all enabled)")
	}
	if created, err := demodata.EnsureNotificationPreferences(ctx, q, user.ID, true); err != nil {
		log.Printf("⚠ Failed to ensure notification preferences: %v", err)
	} else if created {
		log.Println("✓ Notification preferences created (all enabled)")
	}

	// Set demo deadlines on the ACTIVE step assignment of each demo job. The
	// effective job deadline lives on job_step_assignments
	// (jobs.current_step_id → jsa.id), not on jobs.
	tag, err := pool.Exec(ctx, `
		UPDATE job_step_assignments jsa SET deadline = now() + interval '3 days'
		FROM jobs j
		WHERE jsa.id = j.current_step_id
		  AND jsa.deadline IS NULL
		  AND j.file_id IN (SELECT id FROM files WHERE project_id IN (SELECT id FROM projects WHERE tenant_id = $1))
	`, tenantID)
	if err != nil {
		log.Printf("⚠ Failed to set demo job deadlines: %v", err)
	} else {
		log.Printf("✓ Demo job deadlines set on %d active step assignment(s) (3 days from now)", tag.RowsAffected())
	}

	// Also set a project-level deadline on Demo Project so the
	// project list pill is visible without manual setup. 5 days out so it
	// lands in the "soon" band (1-7d) and doesn't compete with the 3d-out
	// job deadlines (which become overdue after a few days of seed staleness).
	_, err = pool.Exec(ctx, `
		UPDATE projects SET deadline = now() + interval '5 days'
		WHERE tenant_id = $1 AND name = 'Demo Project' AND deadline IS NULL
	`, tenantID)
	if err != nil {
		log.Printf("⚠ Failed to set demo project deadline: %v", err)
	} else {
		log.Println("✓ Demo project deadline set (5 days from now)")
	}

	// Blueprint due dates on Demo Project's workflow steps, so the Workflow tab
	// and the upload dialog show a filled-in schedule rather than empty fields.
	// Human and hybrid steps only — an ai step runs by itself and a DB
	// constraint refuses it a deadline.
	tag, err = pool.Exec(ctx, `
		UPDATE workflow_steps ws SET deadline = now() + interval '4 days'
		FROM projects p
		WHERE p.settings_profile_id = ws.settings_profile_id
		  AND p.tenant_id = $1 AND p.name = 'Demo Project'
		  AND ws.step_type <> 'ai' AND ws.deadline IS NULL
	`, tenantID)
	if err != nil {
		log.Printf("⚠ Failed to set demo workflow step deadlines: %v", err)
	} else {
		log.Printf("✓ Demo workflow step deadlines set on %d step(s) (4 days from now)", tag.RowsAffected())
	}

	// One language pulled a day earlier than the rest, so the per-language
	// override is visible in the UI without anyone having to create one.
	tag, err = pool.Exec(ctx, `
		INSERT INTO workflow_step_assignments (workflow_step_id, target_language, deadline)
		SELECT ws.id, p.target_languages[1], now() + interval '3 days'
		FROM workflow_steps ws
		JOIN projects p ON p.settings_profile_id = ws.settings_profile_id
		WHERE p.tenant_id = $1 AND p.name = 'Demo Project'
		  AND ws.step_type <> 'ai' AND ws.step_order = 0
		  AND array_length(p.target_languages, 1) > 0
		ON CONFLICT DO NOTHING
	`, tenantID)
	if err != nil {
		log.Printf("⚠ Failed to set demo per-language step deadline: %v", err)
	} else {
		log.Printf("✓ Demo per-language step deadline set on %d step(s) (3 days from now)", tag.RowsAffected())
	}
}

// healDashboard seeds the shared "Operations" dashboard
// (demodata.EnsureOperationsDashboard) and enough BACKDATED translation history
// for its charts to draw real curves. History and charts heal independently:
// a dashboard that exists over wiped history still gets its history back.
func healDashboard(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	healDashboardHistory(ctx, q, pool, tenantID)
	if _, added, err := demodata.EnsureOperationsDashboard(ctx, q, tenantID, nil); err != nil {
		log.Printf("  ! dashboard: %v", err)
	} else {
		log.Printf("  ✓ Dashboard %q (%d chart(s) added)", demodata.DashboardOperations, added)
	}
}

// healPersonalDashboard gives the seed owner one dashboard of their own, so the
// "My dashboards" tab has something in it for manual QA.
func healPersonalDashboard(ctx context.Context, q *db.Queries, tenantID int64) {
	owner, err := q.GetUserByEmail(ctx, seedid.OwnerEmail)
	if err != nil {
		return // no seed owner yet; the shared dashboard is enough
	}
	if _, err := demodata.EnsurePersonalDashboard(ctx, q, tenantID, owner.ID); err != nil {
		log.Printf("  ! personal dashboard: %v", err)
		return
	}
	log.Printf("  ✓ Personal dashboard %q", demodata.DashboardPersonal)
}

// healDashboardHistory backfills backdated demo history when the tenant has
// NONE — history is append-only, so the zero-check keeps this idempotent
// (re-running on seeded data would double editing-time sums).
func healDashboardHistory(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID int64) {
	var existing int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history th
		JOIN translations t ON t.id = th.translation_id
		JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id
		JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1`, tenantID).Scan(&existing); err != nil {
		log.Printf("  ! dashboard history count: %v", err)
		return
	}
	if existing > 0 {
		log.Println("  ✓ Demo history exists")
		return
	}

	// Backdated history over the demo project's translations, spread across the
	// last 8 weeks. The rows are read by the dashboard measures AND rendered in
	// the CAT editor's segment History section, so they have to be true of the
	// translation they hang off:
	//
	//   - every row gets the event that filled it, carrying its own target
	//     text: `tm` for a TM-filled row (with its match percent), `mt` otherwise
	//   - only rows an actual human confirmed (edited_by set) get a `human`
	//     event, credited to that user
	//
	// Fabricating a `human` event on an untouched segment makes the panel claim
	// work nobody did, on a row whose status and editor say otherwise.
	rows, err := pool.Query(ctx, `
		SELECT t.id, COALESCE(NULLIF(t.active_text, ''), s.source_text), t.edited_by,
		       (s.source_text ILIKE '%patience%') AS showcase, t.target_language,
		       t.origin = 'tm' AS from_tm, t.tm_match_percent
		FROM translations t
		JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id
		JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1
		ORDER BY t.id LIMIT 40`, tenantID)
	if err != nil {
		log.Printf("  ! dashboard history query: %v", err)
		return
	}
	var ids []int64
	var texts []string
	var editors []*int64
	var showcase []bool
	var langs []string
	var fromTM []bool
	var tmPercents []*int16
	for rows.Next() {
		var id int64
		var text, lang string
		var editedBy *int64
		var isShowcase bool
		var isTM *bool
		var tmPercent *int16
		if err := rows.Scan(&id, &text, &editedBy, &isShowcase, &lang, &isTM, &tmPercent); err != nil {
			rows.Close()
			log.Printf("  ! dashboard history scan: %v", err)
			return
		}
		ids = append(ids, id)
		texts = append(texts, text)
		editors = append(editors, editedBy)
		showcase = append(showcase, isShowcase)
		langs = append(langs, lang)
		fromTM = append(fromTM, isTM != nil && *isTM)
		tmPercents = append(tmPercents, tmPercent)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("  ! dashboard history rows: %v", err)
		return
	}

	var tmID *int64
	if tms, err := q.ListTMCollections(ctx, tenantID); err == nil && len(tms) > 0 {
		tmID = &tms[0].ID
	}
	showcaseTMPercent := int16(82)

	// A nil engineID leaves the "MT volume by engine" chart empty (the
	// mt_engine_usage measure only counts attributed rows), so say so loudly.
	var engineID *int64
	if e, err := q.FindTenantMTEngine(ctx, tenantID); err == nil {
		engineID = &e.ID
	} else {
		log.Printf("  ⚠ dashboard history: no MT engine for tenant %d (%v) — "+
			"MT history will be unattributed and the 'MT volume by engine' chart will be empty", tenantID, err)
	}
	// Confirms are spread across the 8-week window on their own schedule rather
	// than following the row index, so the "Words processed per week" chart gets
	// a point per week instead of two clusters. Only a handful of demo segments
	// were genuinely confirmed, and WHEN a demo confirm happened is fixture data
	// either way — what stays honest is that the event exists only where a human
	// really did the work.
	edited := 0
	for _, e := range editors {
		if e != nil {
			edited++
		}
	}
	confirmIdx := 0

	for i, tid := range ids {
		daysAgo := (i * 56) / max(len(ids), 1) // spread over ~8 weeks
		if editors[i] != nil {
			daysAgo = (confirmIdx * 56) / max(edited-1, 1)
			confirmIdx++
		}
		text := texts[i]
		backdated := []int64{}

		// One segment per language walks the whole pipeline — file import, a
		// fuzzy TM hit, MT, an APE rewrite — so the History panel has a real
		// lineage to show during manual QA instead of a single MT event.
		//
		// Events are inserted in chronological order: the panel sorts newest
		// first on (created_at, id), and these all share one backdated
		// timestamp, so insertion order is what decides how the chain reads.
		draft, isShowcase := showcaseDrafts[langs[i]]
		isShowcase = isShowcase && showcase[i]
		if isShowcase {
			for _, ev := range []db.InsertTranslationHistoryParams{
				{TranslationID: tid, Kind: "file_import", Text: &draft.fileImport, Details: []byte("{}")},
				{TranslationID: tid, Kind: "tm", Text: &draft.tm, TmID: tmID, TmMatchPercent: &showcaseTMPercent,
					Details: []byte(`{"source":"pipeline"}`)},
			} {
				row, err := q.InsertTranslationHistory(ctx, ev)
				if err != nil {
					log.Printf("  ! showcase history insert (%s): %v", ev.Kind, err)
					return
				}
				backdated = append(backdated, row.ID)
			}
			text = draft.mt // the MT step produced its own draft, not the final text
		}

		// A TM-filled row was never sent to MT: its filling event is `tm`.
		fill := db.InsertTranslationHistoryParams{
			TranslationID: tid, Kind: "mt", Text: &text, MtEngineID: engineID, Details: []byte("{}"),
		}
		if fromTM[i] && !isShowcase {
			fill = db.InsertTranslationHistoryParams{
				TranslationID: tid, Kind: "tm", Text: &text, TmID: tmID, TmMatchPercent: tmPercents[i],
				Details: []byte(`{"source":"pipeline"}`),
			}
		}
		mt, err := q.InsertTranslationHistory(ctx, fill)
		if err != nil {
			log.Printf("  ! dashboard history insert: %v", err)
			return
		}
		backdated = append(backdated, mt.ID)

		if isShowcase {
			ape, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
				TranslationID: tid, Kind: "ape", Text: &draft.ape, MtEngineID: engineID,
				Details: []byte(`{"predict_outcome":"ape_approved"}`),
			})
			if err != nil {
				log.Printf("  ! showcase history insert (ape): %v", err)
				return
			}
			backdated = append(backdated, ape.ID)
		}
		text = texts[i] // the human event below confirms the segment's real target

		// The confirm that produced `approved`. Most demo segments were
		// confirmed as-is, so there is no distance to record and the panel
		// shows no Δ; on the showcase chain the human did rewrite APE's
		// version, so the real distance between the two is stamped.
		if editors[i] != nil {
			editMs := int64(20_000 + i*3_000)
			var dist *int32
			if isShowcase {
				d := int32(strdist.Levenshtein(draft.ape, text))
				dist = &d
			}
			human, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
				TranslationID: tid, Kind: "human", Text: &text,
				CreatedBy: editors[i], EditingTimeMs: &editMs, EditDistance: dist, Details: []byte("{}"),
			})
			if err != nil {
				log.Printf("  ! dashboard history insert: %v", err)
				return
			}
			backdated = append(backdated, human.ID)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE translation_history SET created_at = now() - make_interval(days => $1) WHERE id = ANY($2)`,
			daysAgo, backdated); err != nil {
			log.Printf("  ! dashboard history backdate: %v", err)
			return
		}
	}

	log.Printf("  ✓ Backfilled demo history (%d translations)", len(ids))
}

// showcaseDrafts are the earlier versions the showcase chain walks through,
// per target language. They translate demo.txt's "We thank you for your
// patience." — which is why the segment pick filters on that source text. The
// final `human` event uses the segment's real active_text, so the chain reads
// as one sentence being refined. Fixed drafts on an arbitrary segment would
// instead look like a bug in the panel.
//
// A language with no entry here is skipped, as is a demo file that no longer
// carries the sentence.
var showcaseDrafts = map[string]struct{ fileImport, tm, mt, ape string }{
	"es": {
		fileImport: "Gracias por esperar.",
		tm:         "Gracias por su paciencia.",
		mt:         "Le damos las gracias por su paciencia.",
		ape:        "Le agradecemos mucho su paciencia.",
	},
	"de": {
		fileImport: "Danke fürs Warten.",
		tm:         "Vielen Dank für Ihre Geduld.",
		mt:         "Wir danken Ihnen für Ihre Geduld.",
		ape:        "Wir danken Ihnen vielmals für Ihre Geduld.",
	},
}

// healJobTimings gives the project's Processing time panel
// something to render during manual QA, through
// demodata.WriteJobTimings. The merge accumulates, so the rows are cleared
// first.
func healJobTimings(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	rows, err := pool.Query(ctx, `
		SELECT j.id FROM jobs j
		JOIN files f ON f.id = j.file_id
		JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1
		ORDER BY j.id
		LIMIT 12
	`, tenantID)
	if err != nil {
		log.Printf("  ⚠ Job timings: job lookup failed: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("  ⚠ Job timings: scan failed: %v", err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return
	}
	if _, err := pool.Exec(ctx, `DELETE FROM job_timings WHERE job_id = ANY($1)`, ids); err != nil {
		log.Printf("  ⚠ Job timings: clear failed: %v", err)
		return
	}
	if err := demodata.WriteJobTimings(ctx, q, tenantID, ids); err != nil {
		log.Printf("  ⚠ Job timings: %v", err)
		return
	}
	log.Printf("  ✓ Job timings seeded for %d jobs", len(ids))
}

// existingConnectionID returns this tenant's first connection for a provider,
// or 0 if it has none. Seeding is re-run against dirty databases, so it must
// reuse what is already there rather than pile up duplicates.
func existingConnectionID(ctx context.Context, q db.Querier, tenantID int64, provider string) (int64, error) {
	conns, err := q.ListAIConnections(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	for _, c := range conns {
		if c.Provider == provider {
			return c.ID, nil
		}
	}
	return 0, nil
}

// healTMProfile ensures the demo TM profiles exist: "Standard TM profile" at
// the column defaults, and "Client-first TM profile" with lookup order "By TM
// order" so the setting has a non-default example. Their
// names and descriptions avoid the words "Default TM" on purpose: the TM page
// lists both, and E2E specs locate the TM by that text. Left unlinked — the
// demo project and Default Template keep their own rows, so the settings UI
// shows an editable list.
func healTMProfile(ctx context.Context, q *db.Queries, tenantID, userID int64) {
	profiles, err := q.ListTMProfiles(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not check TM profiles: %v", err)
		return
	}
	existing := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		existing[p.Name] = true
	}
	tmOrder := settings.TMLookupOrderTMOrder
	wanted := []struct {
		name, description string
		scoring           tmprofile.Scoring
	}{
		{"Standard TM profile", "One shared memory, read and write, 75% fuzzy threshold", tmprofile.Scoring{}},
		{"Client-first TM profile", "Fills each segment from the first memory before trying the next", tmprofile.Scoring{TMLookupOrder: &tmOrder}},
	}
	var tms []db.ListTMCollectionsRow
	for _, w := range wanted {
		if existing[w.name] {
			log.Printf("✓ TM profile: %s", w.name)
			continue
		}
		if tms == nil {
			if tms, err = q.ListTMCollections(ctx, tenantID); err != nil || len(tms) == 0 {
				log.Printf("⚠ No TM to bundle into a TM profile (err=%v)", err)
				return
			}
		}
		tp, err := tmprofile.Create(ctx, q, tenantID, auth.UserIDPtr(userID), w.name, w.description, w.scoring,
			[]settings.TMAssignmentInput{{TmID: tms[0].ID, Mode: "read_write", Priority: 0}})
		if err != nil {
			log.Fatalf("Failed to create TM profile: %v", err)
		}
		log.Printf("✓ TM profile created: %s (ID: %d)", tp.Name, tp.ID)
	}
}
