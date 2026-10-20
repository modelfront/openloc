package testutil

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	db "openloc/internal/database/sqlcgen"
)

// RegisterUser bootstraps a test tenant + owner in one call: creates a tenant
// (name "<prefix> Corp"), a global identity ("<prefix>@test.com"), and an owner
// membership, returning (userID, tenantID). Pair with
// testutil.TestJWT(userID, tenantID) / DoRequest for authenticated calls.
func RegisterUser(t testing.TB, q db.Querier, prefix string) (userID, tenantID int64) {
	t.Helper()
	tenant := SeedTenant(t, q, prefix+" Corp")
	// Name = prefix (not the email) to match the old register-over-HTTP behavior
	// that some tests assert on (e.g. edited_by_name in the conflict banner).
	user, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Email: prefix + "@test.com",
		Name:  prefix,
	})
	if err != nil {
		t.Fatalf("RegisterUser(%q) CreateUser: %v", prefix, err)
	}
	if err := q.CreateMembership(context.Background(), db.CreateMembershipParams{
		UserID:   user.ID,
		TenantID: tenant.ID,
		Role:     "owner",
	}); err != nil {
		t.Fatalf("RegisterUser(%q) CreateMembership: %v", prefix, err)
	}
	return user.ID, tenant.ID
}

// RandomToken returns a 32-byte crypto-random URL-safe token — the shape the
// product uses for invite and reset tokens. For seeding token-bearing rows.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

// SeedTenant creates a tenant with the given name and an auto-generated slug.
func SeedTenant(t testing.TB, q db.Querier, name string) db.Tenant {
	t.Helper()
	slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
	tenant, err := q.CreateTenant(context.Background(), db.CreateTenantParams{
		Name: name,
		Slug: slug,
	})
	if err != nil {
		t.Fatalf("SeedTenant(%q): %v", name, err)
	}
	return tenant
}

// SeedAIVendorToken ensures an AI vendor row (tenant, provider) and creates a
// named token under it. encKey is stored verbatim as api_key_encrypted (tests
// pair with an identity decryptor). Returns the created token.
func SeedAIVendorToken(t testing.TB, q db.Querier, tenantID int64, provider, name, encKey string) db.AiVendorToken {
	t.Helper()
	return SeedAIVendorTokenBase(t, q, tenantID, provider, name, encKey, "")
}

// SeedAIVendorTokenBase is SeedAIVendorToken with a custom endpoint (an Azure
// OpenAI resource, a self-hosted gateway). Empty baseURL = the vendor's public
// API. Creates the CONNECTION the token needs — one per
// call, since a test asking for a distinct endpoint wants a distinct connection.
func SeedAIVendorTokenBase(t testing.TB, q db.Querier, tenantID int64, provider, name, encKey, baseURL string) db.AiVendorToken {
	t.Helper()
	v, err := q.UpsertAIVendor(context.Background(), db.UpsertAIVendorParams{TenantID: tenantID, Provider: provider})
	if err != nil {
		t.Fatalf("SeedAIVendorToken upsert vendor %q: %v", provider, err)
	}
	conn, err := q.CreateAIConnection(context.Background(), db.CreateAIConnectionParams{
		VendorID: v.ID, Name: name, BaseUrl: baseURL,
	})
	if err != nil {
		t.Fatalf("SeedAIVendorToken create connection: %v", err)
	}
	tok, err := q.CreateVendorToken(context.Background(), db.CreateVendorTokenParams{
		VendorID: v.ID, Name: name, ApiKeyEncrypted: encKey, ConnectionID: conn.ID,
	})
	if err != nil {
		t.Fatalf("SeedAIVendorToken create token: %v", err)
	}
	return tok
}

// SeedUser creates a user with the given email and password (auto-hashed).
// Defaults to role "owner".
func SeedUser(t testing.TB, q db.Querier, tenantID int64, email, password string) db.User {
	t.Helper()
	return SeedUserWithRole(t, q, tenantID, email, password, "owner")
}

// SeedUserWithRole creates a global identity and a membership in the given
// tenant with the specified role. The membership is what carries the
// role and the org scoping; the returned db.User is the identity.
func SeedUserWithRole(t testing.TB, q db.Querier, tenantID int64, email, password, role string) db.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost) // MinCost for speed
	if err != nil {
		t.Fatalf("SeedUser: bcrypt hash failed: %v", err)
	}
	hashStr := string(hash)
	user, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: &hashStr,
		Name:         email,
	})
	if err != nil {
		t.Fatalf("SeedUser(%q): %v", email, err)
	}
	if err := q.CreateMembership(context.Background(), db.CreateMembershipParams{
		UserID:   user.ID,
		TenantID: tenantID,
		Role:     role,
	}); err != nil {
		t.Fatalf("SeedUser(%q) CreateMembership: %v", email, err)
	}
	return user
}

// SeedIdentity creates a global identity with NO membership — used by tests that
// exercise the join/auto-provision seams (domain auto-join, invite acceptance).
func SeedIdentity(t testing.TB, q db.Querier, email string) db.User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), db.CreateUserParams{Email: email, Name: email})
	if err != nil {
		t.Fatalf("SeedIdentity(%q): %v", email, err)
	}
	return u
}

// SeedMembership adds an existing identity to another org with a role — used by
// multi-org tests (the same user belonging to two tenants).
func SeedMembership(t testing.TB, q db.Querier, userID, tenantID int64, role string) {
	t.Helper()
	if err := q.CreateMembership(context.Background(), db.CreateMembershipParams{
		UserID:   userID,
		TenantID: tenantID,
		Role:     role,
	}); err != nil {
		t.Fatalf("SeedMembership(user=%d tenant=%d role=%s): %v", userID, tenantID, role, err)
	}
}

// SeedProject creates a project with the given languages and a default settings profile.
func SeedProject(t testing.TB, q db.Querier, tenantID int64, name, srcLang string, tgtLangs []string) db.Project {
	t.Helper()
	sp, err := q.CreateSettingsProfile(context.Background(), db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: 0.75,
		MtOnUpload:     false,
		TmOnUpload:     false,
	})
	if err != nil {
		t.Fatalf("SeedProject(%q) CreateSettingsProfile: %v", name, err)
	}
	project, err := q.CreateProject(context.Background(), db.CreateProjectParams{
		Name:              name,
		SourceLanguage:    srcLang,
		TargetLanguages:   tgtLangs,
		TenantID:          tenantID,
		SettingsProfileID: sp.ID,
		// Mirrors the prod default in project.Handler.HandleCreate. Without
		// this, the projects.status enum (added in migration 025) rejects
		// the empty zero-value with SQLSTATE 22P02.
		Status: db.ProjectStatusInProgress,
	})
	if err != nil {
		t.Fatalf("SeedProject(%q): %v", name, err)
	}
	return project
}

// SeedProjectSystemLabel gives a project its Customer or Domain pick — the two
// built-in labels. The definitions themselves come from the
// tenant trigger, so this only adds the value and the pick. Use it instead of
// writing projects.customer_id: nothing reads that column any more, so a test
// that seeds it proves nothing about what the app shows.
func SeedProjectSystemLabel(t testing.TB, q db.Querier, tenantID, projectID int64, systemKey, value string) db.LabelValue {
	t.Helper()
	ctx := context.Background()
	defs, err := q.ListLabelDefinitions(ctx, tenantID)
	if err != nil {
		t.Fatalf("SeedProjectSystemLabel(%q) list definitions: %v", systemKey, err)
	}
	var defID int64
	for _, d := range defs {
		if d.SystemKey != nil && *d.SystemKey == systemKey {
			defID = d.ID
			break
		}
	}
	if defID == 0 {
		t.Fatalf("SeedProjectSystemLabel: tenant %d has no %q label — the seed trigger should have created it", tenantID, systemKey)
	}
	lv, err := q.CreateLabelValue(ctx, db.CreateLabelValueParams{LabelDefinitionID: defID, Value: value})
	if err != nil {
		t.Fatalf("SeedProjectSystemLabel(%q=%q) create value: %v", systemKey, value, err)
	}
	if err := q.SetProjectLabel(ctx, db.SetProjectLabelParams{
		ProjectID: projectID, LabelDefinitionID: defID, LabelValueID: lv.ID,
	}); err != nil {
		t.Fatalf("SeedProjectSystemLabel(%q=%q) set pick: %v", systemKey, value, err)
	}
	return lv
}

// SeedTemplate creates a template with default fuzzy threshold (0.75).
func SeedTemplate(t testing.TB, q db.Querier, tenantID int64, name string) db.Template {
	t.Helper()
	sp, err := q.CreateSettingsProfile(context.Background(), db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: 0.75,
		MtOnUpload:     false,
		TmOnUpload:     false,
	})
	if err != nil {
		t.Fatalf("SeedTemplate(%q) CreateSettingsProfile: %v", name, err)
	}
	tmpl, err := q.CreateTemplate(context.Background(), db.CreateTemplateParams{
		Name:              name,
		TenantID:          tenantID,
		SettingsProfileID: sp.ID,
	})
	if err != nil {
		t.Fatalf("SeedTemplate(%q): %v", name, err)
	}
	return tmpl
}

// SeedTM creates a translation memory collection.
func SeedTM(t testing.TB, q db.Querier, tenantID int64, name string) db.TmCollection {
	t.Helper()
	tm, err := q.CreateTMCollection(context.Background(), db.CreateTMCollectionParams{
		Name:     name,
		TenantID: tenantID,
	})
	if err != nil {
		t.Fatalf("SeedTM(%q): %v", name, err)
	}
	return tm
}

// SeedTMWithLangs creates a TM with declared source + target languages.
// Use this when a test relies on the per-TM assignment lookup
// resolving by the TM's own language coverage rather than the NULL-wildcard.
func SeedTMWithLangs(t testing.TB, q db.Querier, tenantID int64, name, source string, targets []string) db.TmCollection {
	t.Helper()
	tm, err := q.CreateTMCollection(context.Background(), db.CreateTMCollectionParams{
		Name:            name,
		TenantID:        tenantID,
		SourceLanguage:  &source,
		TargetLanguages: targets,
	})
	if err != nil {
		t.Fatalf("SeedTMWithLangs(%q): %v", name, err)
	}
	return tm
}

// SeedMTEngine creates an MT engine.
func SeedMTEngine(t testing.TB, q db.Querier, tenantID int64, name, engineType string) db.MtEngine {
	t.Helper()
	engine, err := q.CreateMTEngine(context.Background(), db.CreateMTEngineParams{
		Name:       name,
		EngineType: engineType,
		Enabled:    true,
		TenantID:   tenantID,
	})
	if err != nil {
		t.Fatalf("SeedMTEngine(%q): %v", name, err)
	}
	return engine
}

// SeedTermBase creates a term base. tgtLang is accepted for call-site
// convenience but no longer stored on the base — target_languages derive from
// the base's terms (and can be set explicitly by tests that need them).
func SeedTermBase(t testing.TB, q db.Querier, tenantID int64, name, srcLang, tgtLang string) db.TermBasis {
	t.Helper()
	_ = tgtLang
	tb, err := q.CreateTermBase(context.Background(), db.CreateTermBaseParams{
		TenantID:       tenantID,
		Name:           name,
		SourceLanguage: srcLang,
	})
	if err != nil {
		t.Fatalf("SeedTermBase(%q): %v", name, err)
	}
	return tb
}

// SeedFile creates a file record (no actual S3 upload).
func SeedFile(t testing.TB, q db.Querier, projectID int64, name string, format db.FileFormat) db.File {
	t.Helper()
	f, err := q.CreateFile(context.Background(), db.CreateFileParams{
		ProjectID:   projectID,
		Name:        name,
		Format:      format,
		StoragePath: "test/" + name,
	})
	if err != nil {
		t.Fatalf("SeedFile(%q): %v", name, err)
	}
	return f
}

// SeedSegment creates a segment.
func SeedSegment(t testing.TB, q db.Querier, fileID int64, sourceText string, position int32) db.Segment {
	t.Helper()
	seg, err := q.CreateSegment(context.Background(), db.CreateSegmentParams{
		FileID:     fileID,
		SourceText: sourceText,
		Position:   position,
	})
	if err != nil {
		t.Fatalf("SeedSegment(%q): %v", sourceText, err)
	}
	return seg
}

// SeedTranslation creates a translation for a segment.
func SeedTranslation(t testing.TB, q db.Querier, segmentID int64, targetLang string, status db.TranslationStatus) db.Translation {
	t.Helper()
	tr, err := q.CreateTranslation(context.Background(), db.CreateTranslationParams{
		SegmentID:      segmentID,
		TargetLanguage: targetLang,
		Status:         status,
	})
	if err != nil {
		t.Fatalf("SeedTranslation: %v", err)
	}
	return tr
}

// SeedHold makes userID hold the translation, as the editor's tab "tab-1" does,
// for 90 seconds.
func SeedHold(t testing.TB, q db.Querier, tenantID, jobID, translationID, userID int64) {
	t.Helper()
	if _, err := q.AcquireHold(context.Background(), db.AcquireHoldParams{
		TranslationID: translationID, TenantID: tenantID, JobID: jobID, UserID: userID, TabID: "tab-1", TtlSeconds: 90,
	}); err != nil {
		t.Fatalf("SeedHold: %v", err)
	}
}

// SeedJob creates a job.
func SeedJob(t testing.TB, q db.Querier, fileID int64, targetLang string) db.Job {
	t.Helper()
	job, err := q.CreateJob(context.Background(), db.CreateJobParams{
		FileID:         fileID,
		TargetLanguage: targetLang,
		WordCount:      0,
	})
	if err != nil {
		t.Fatalf("SeedJob: %v", err)
	}
	return job
}

// SeedJobStep creates a workflow step on the settings profile, an in_progress
// job_step_assignment for the job assigned to `assignee` (nil = unassigned),
// and marks it the job's current step. Returns the assignment ID. This is the
// canonical "job assigned to user" setup used by Tasks + vendor-access tests.
func SeedJobStep(t testing.TB, q db.Querier, settingsProfileID, jobID int64, assignee *int64) int64 {
	t.Helper()
	step, err := q.CreateSettingsWorkflowStep(context.Background(), db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: settingsProfileID,
		Name:              "translate",
		StepOrder:         0,
		RoleRequired:      "linguist",
	})
	if err != nil {
		t.Fatalf("SeedJobStep CreateSettingsWorkflowStep: %v", err)
	}
	a, err := q.CreateJobStepAssignment(context.Background(), db.CreateJobStepAssignmentParams{
		JobID:          jobID,
		WorkflowStepID: step.ID,
		AssignedTo:     assignee,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("SeedJobStep CreateJobStepAssignment: %v", err)
	}
	if err := q.SetJobCurrentStep(context.Background(), db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &a.ID}); err != nil {
		t.Fatalf("SeedJobStep SetJobCurrentStep: %v", err)
	}
	return a.ID
}

// SeedAIJobStep is SeedJobStep for an AI step: an `ai` workflow step on its
// own AI router, and an unassigned in_progress assignment that is the job's
// current step. Returns the assignment ID.
func SeedAIJobStep(t testing.TB, q db.Querier, tenantID, settingsProfileID, jobID int64) int64 {
	t.Helper()
	router := SeedAIRouter(t, q, tenantID, "QE+APE", nil)
	return SeedAIJobStepWithRouter(t, q, settingsProfileID, jobID, router.ID)
}

// SeedAIJobStepWithRouter is SeedAIJobStep on a given AI router, so the step
// resolves to a real engine (see SeedRouterForEngine).
func SeedAIJobStepWithRouter(t testing.TB, q db.Querier, settingsProfileID, jobID, routerID int64) int64 {
	t.Helper()
	ctx := context.Background()
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: settingsProfileID, Name: "Auto APE", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &routerID, StepType: "ai",
	})
	if err != nil {
		t.Fatalf("SeedAIJobStep CreateSettingsWorkflowStep: %v", err)
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: step.ID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("SeedAIJobStep CreateJobStepAssignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &a.ID}); err != nil {
		t.Fatalf("SeedAIJobStep SetJobCurrentStep: %v", err)
	}
	return a.ID
}

// SeedTMProfile creates a TM profile with its own settings row carrying the
// given fuzzy threshold. Attach TMs with CreateSettingsTMAssignment on
// tp.SettingsProfileID; link a project or template with
// SetSettingsProfileTMProfile on ITS settings row.
func SeedTMProfile(t testing.TB, q db.Querier, tenantID int64, name string, fuzzyThreshold float32) db.TmProfile {
	t.Helper()
	ctx := context.Background()
	sp, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: fuzzyThreshold,
	})
	if err != nil {
		t.Fatalf("SeedTMProfile(%q) CreateSettingsProfile: %v", name, err)
	}
	tp, err := q.CreateTMProfile(ctx, db.CreateTMProfileParams{
		TenantID:          tenantID,
		SettingsProfileID: sp.ID,
		Name:              name,
	})
	if err != nil {
		t.Fatalf("SeedTMProfile(%q): %v", name, err)
	}
	return tp
}

// LinkTMProfile points a project's or template's settings row at a TM profile.
func LinkTMProfile(t testing.TB, q db.Querier, settingsProfileID, tmProfileID int64) {
	t.Helper()
	if _, err := q.SetSettingsProfileTMProfile(context.Background(), db.SetSettingsProfileTMProfileParams{
		ID: settingsProfileID, TmProfileID: &tmProfileID,
	}); err != nil {
		t.Fatalf("LinkTMProfile(%d → %d): %v", settingsProfileID, tmProfileID, err)
	}
}

// SetTMLookupOrder sets a settings row's TM lookup order ("match_percent" or
// "tm_order"), leaving every other column as stored.
func SetTMLookupOrder(t testing.TB, q db.Querier, settingsProfileID int64, order string) {
	t.Helper()
	ctx := context.Background()
	sp, err := q.GetSettingsProfile(ctx, settingsProfileID)
	if err != nil {
		t.Fatalf("SetTMLookupOrder: get %d: %v", settingsProfileID, err)
	}
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID: sp.ID, FuzzyThreshold: sp.FuzzyThreshold, MtOnUpload: sp.MtOnUpload,
		TmOnUpload: sp.TmOnUpload, QaProfileID: sp.QaProfileID, TmLookupOrder: &order,
	}); err != nil {
		t.Fatalf("SetTMLookupOrder(%d, %q): %v", settingsProfileID, order, err)
	}
}

// SeedAIRouter creates an AI router whose default is
// defaultEngineID (nil = no default), with no per-language rules.
func SeedAIRouter(t testing.TB, q db.Querier, tenantID int64, name string, defaultEngineID *int64) db.AiRouter {
	t.Helper()
	r, err := q.CreateAIRouter(context.Background(), db.CreateAIRouterParams{
		TenantID: tenantID, Name: name, DefaultEngineID: defaultEngineID,
	})
	if err != nil {
		t.Fatalf("SeedAIRouter %q: %v", name, err)
	}
	return r
}

// SeedAIRouterRule adds one target language → engine rule to a router.
func SeedAIRouterRule(t testing.TB, q db.Querier, routerID int64, targetLang string, engineID int64) db.AiRouterRule {
	t.Helper()
	rr, err := q.CreateAIRouterRule(context.Background(), db.CreateAIRouterRuleParams{
		RouterID: routerID, TargetLang: targetLang, EngineID: engineID,
	})
	if err != nil {
		t.Fatalf("SeedAIRouterRule %s → %d: %v", targetLang, engineID, err)
	}
	return rr
}

// SeedRouterForEngine is the one-liner most tests need: a router named after
// the engine, defaulting to it. Returns the router id.
func SeedRouterForEngine(t testing.TB, q db.Querier, tenantID, engineID int64) int64 {
	t.Helper()
	return SeedAIRouter(t, q, tenantID, fmt.Sprintf("router-for-engine-%d", engineID), &engineID).ID
}

// SeedStepDefinition creates a library step definition.
func SeedStepDefinition(t testing.TB, q db.Querier, tenantID int64, name, stepType, role string, aiRouterID *int64) db.StepDefinition {
	t.Helper()
	if role == "" {
		role = "linguist"
	}
	sd, err := q.CreateStepDefinition(context.Background(), db.CreateStepDefinitionParams{
		TenantID: tenantID, Name: name, StepType: stepType, RoleRequired: role, AiRouterID: aiRouterID,
	})
	if err != nil {
		t.Fatalf("SeedStepDefinition %q: %v", name, err)
	}
	return sd
}

// LibraryStep returns one item of a workflow-steps request body pointing at
// the tenant's step definition called name, creating the definition when it
// does not exist yet. Extras are merged into the item, except that
// "role_required" and "ai_router_id" describe the definition (used only when
// it is created — an existing definition keeps its own) and never reach the
// request, which the API would reject.
func LibraryStep(t testing.TB, q db.Querier, tenantID int64, name string, extra ...map[string]any) map[string]any {
	t.Helper()
	merged := map[string]any{}
	for _, m := range extra {
		for k, v := range m {
			merged[k] = v
		}
	}
	role, _ := merged["role_required"].(string)
	delete(merged, "role_required")
	var router *int64
	switch v := merged["ai_router_id"].(type) {
	case int64:
		router = &v
	case *int64:
		router = v
	}
	delete(merged, "ai_router_id")

	def, err := q.GetStepDefinitionByName(context.Background(), db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: name})
	if err != nil {
		stepType := "human"
		if router != nil {
			stepType = "hybrid"
		}
		def = SeedStepDefinition(t, q, tenantID, name, stepType, role, router)
	}
	merged["step_definition_id"] = def.ID
	return merged
}

// SeedMTRouter creates an MT router whose default is the given classic MT
// engine or LLM translation engine (exactly one non-nil), with no
// per-language rules. Returns the router id.
func SeedMTRouter(t testing.TB, q db.Querier, tenantID int64, name string, mtEngineID, aiEngineID *int64) int64 {
	t.Helper()
	r, err := q.CreateMTRouter(context.Background(), db.CreateMTRouterParams{
		TenantID: tenantID, Name: name, DefaultMtEngineID: mtEngineID, DefaultAiEngineID: aiEngineID,
	})
	if err != nil {
		t.Fatalf("SeedMTRouter %q: %v", name, err)
	}
	return r.ID
}

// SeedMTRouterRule adds one target language → engine rule to an MT router.
func SeedMTRouterRule(t testing.TB, q db.Querier, routerID int64, targetLang string, mtEngineID, aiEngineID *int64) db.MtRouterRule {
	t.Helper()
	rr, err := q.CreateMTRouterRule(context.Background(), db.CreateMTRouterRuleParams{
		RouterID: routerID, TargetLang: targetLang, MtEngineID: mtEngineID, AiEngineID: aiEngineID,
	})
	if err != nil {
		t.Fatalf("SeedMTRouterRule %s: %v", targetLang, err)
	}
	return rr
}

// SeedMTRouterForProfile is the one-liner most tests need: a router defaulting
// to mtEngineID (or aiEngineID), pointed at by the profile. Returns the router
// id.
func SeedMTRouterForProfile(t testing.TB, q db.Querier, tenantID, profileID int64, mtEngineID, aiEngineID *int64) int64 {
	t.Helper()
	id := SeedMTRouter(t, q, tenantID, fmt.Sprintf("router-for-profile-%d", profileID), mtEngineID, aiEngineID)
	SetProfileMTRouter(t, q, profileID, &id)
	return id
}
