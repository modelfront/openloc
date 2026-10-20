package devseed

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/seedid"
	"openloc/internal/wordcount"
)

// RouterName is the engine-less AI router every seeded project's AI and
// hybrid steps point at.
const RouterName = "Showcase QE+APE"

// File is one source file and a scenario per target language, in the spec's
// TargetLangs order.
type File struct {
	Name      string
	Segments  []string
	Scenarios []Scenario
}

// Spec describes a seeded project. Seeding is idempotent per ProjectName: the
// project (and its URL) is kept, its files and workflow steps are rebuilt, so
// edits made while testing are thrown away.
type Spec struct {
	ProjectName string
	TargetLangs []string
	Steps       []Step
	Files       []File
	// Translations writes a translation row per segment and language, from
	// each scenario's TextPlan, so the editor can save edits. Without it the
	// segments have no rows and the grid reads "Click to translate".
	Translations bool
}

// Result is where the seeded project landed.
type Result struct {
	TenantID   int64
	TenantName string
	ProjectID  int64
}

// Seed builds spec's project in the seed org, for its owner. `make seed`
// must have run: the tenant and its people come from there.
func Seed(ctx context.Context, pool *pgxpool.Pool, spec Spec) (Result, error) {
	for _, f := range spec.Files {
		if len(f.Scenarios) != len(spec.TargetLangs) {
			return Result{}, fmt.Errorf("file %s: %d scenarios for %d target languages", f.Name, len(f.Scenarios), len(spec.TargetLangs))
		}
	}
	q := db.New(pool)
	tenantID, tenantName, err := SeedOrg(ctx, pool)
	if err != nil {
		return Result{}, fmt.Errorf("find tenant: %w (run `make seed` first)", err)
	}
	people, err := tenantPeople(ctx, pool, tenantID)
	if err != nil {
		return Result{}, fmt.Errorf("find people to assign: %w", err)
	}
	if len(people) == 0 {
		return Result{}, fmt.Errorf("no people to assign (run `make seed` first)")
	}
	owner := tenantOwner(ctx, pool, tenantID)

	// The project is the owner's, like `make seed`'s demo data, so the seed
	// makes nobody else a "creator" (creator-attribution.spec relies on it).
	project, err := ensureProject(ctx, q, pool, tenantID, owner, spec)
	if err != nil {
		return Result{}, fmt.Errorf("ensure project: %w", err)
	}
	stepIDs, err := rebuildWorkflow(ctx, q, pool, tenantID, project.SettingsProfileID, spec.Steps)
	if err != nil {
		return Result{}, fmt.Errorf("workflow: %w", err)
	}
	now := time.Now()
	for _, f := range spec.Files {
		if err := seedFile(ctx, q, pool, project.ID, f, spec, stepIDs, people, owner, now); err != nil {
			return Result{}, fmt.Errorf("file %s: %w", f.Name, err)
		}
	}
	return Result{TenantID: tenantID, TenantName: tenantName, ProjectID: project.ID}, nil
}

// BaseURL is the app URL the seeders print links to.
func BaseURL() string {
	if v := os.Getenv("BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:9000"
}

type person struct {
	id   int64
	name string
}

// tenantPeople is who the scenarios hand tasks to: the tenant's active members
// from `make seed` (its users are on seedid.EmailDomain; the ones tests leave
// behind are not), owner last so the matrix shows several names.
func tenantPeople(ctx context.Context, pool *pgxpool.Pool, tenantID int64) ([]person, error) {
	rows, err := pool.Query(ctx,
		`SELECT u.id, u.name FROM memberships m JOIN users u ON u.id = m.user_id
		 WHERE m.tenant_id = $1 AND u.is_active AND u.email LIKE '%@' || $2
		 ORDER BY (m.role = 'owner'), u.id LIMIT 4`, tenantID, seedid.EmailDomain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []person
	for rows.Next() {
		var p person
		if err := rows.Scan(&p.id, &p.name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// rebuildWorkflow replaces the project's workflow steps. The old steps'
// assignments went with the files already.
func rebuildWorkflow(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID, profileID int64, steps []Step) ([]int64, error) {
	if _, err := pool.Exec(ctx, "DELETE FROM workflow_steps WHERE settings_profile_id = $1", profileID); err != nil {
		return nil, fmt.Errorf("clear steps: %w", err)
	}
	routerID, err := ensureRouter(ctx, q, pool, tenantID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(steps))
	for i, s := range steps {
		var router *int64
		if s.UsesRouter {
			router = &routerID
		}
		st, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: profileID, Name: s.Name, StepOrder: int32(i),
			RoleRequired: s.Role, AiRouterID: router, StepType: s.Type,
		})
		if err != nil {
			return nil, fmt.Errorf("step %s: %w", s.Name, err)
		}
		ids[i] = st.ID
	}
	return ids, nil
}

func ensureRouter(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, "SELECT id FROM ai_routers WHERE tenant_id = $1 AND name = $2", tenantID, RouterName).Scan(&id); err == nil {
		return id, nil
	}
	r, err := q.CreateAIRouter(ctx, db.CreateAIRouterParams{TenantID: tenantID, Name: RouterName})
	if err != nil {
		return 0, fmt.Errorf("ai router: %w", err)
	}
	return r.ID, nil
}

type segment struct {
	id     int64
	source string
}

func seedFile(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, projectID int64, f File, spec Spec,
	stepIDs []int64, people []person, owner int64, now time.Time) error {
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID: projectID, Name: f.Name, Format: "txt",
		StoragePath: fmt.Sprintf("seed/devseed/%d/%s", projectID, f.Name), SegmentCount: int32(len(f.Segments)),
	})
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	words := 0
	segs := make([]segment, len(f.Segments))
	for pos, src := range f.Segments {
		wc := wordcount.CountWords(src)
		words += wc
		if err := pool.QueryRow(ctx,
			`INSERT INTO segments (file_id, source_text, segment_key, position, word_count) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			file.ID, src, fmt.Sprintf("%d", pos+1), pos, wc).Scan(&segs[pos].id); err != nil {
			return fmt.Errorf("segment %d: %w", pos, err)
		}
		segs[pos].source = src
	}
	for li, lang := range spec.TargetLangs {
		sc := f.Scenarios[li]
		if err := seedJob(ctx, q, pool, file.ID, lang, int32(words), sc, stepIDs, people, now); err != nil {
			return fmt.Errorf("%s (%s): %w", lang, sc.Name, err)
		}
		if spec.Translations {
			if err := seedTranslations(ctx, pool, segs, lang, sc.Text, owner); err != nil {
				return fmt.Errorf("%s (%s) translations: %w", lang, sc.Name, err)
			}
		}
	}
	return nil
}

func seedJob(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, fileID int64, lang string, words int32,
	sc Scenario, stepIDs []int64, people []person, now time.Time) error {
	if len(sc.Steps) != len(stepIDs) {
		return fmt.Errorf("%d step plans for %d steps", len(sc.Steps), len(stepIDs))
	}
	job, err := q.CreateJob(ctx, db.CreateJobParams{FileID: fileID, TargetLanguage: lang, WordCount: words})
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	var current *int64
	for i, p := range sc.Steps {
		var who *int64
		if p.Who >= 0 {
			who = &people[p.Who%len(people)].id
		}
		a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: job.ID, WorkflowStepID: stepIDs[i], AssignedTo: who, Status: p.Status, Deadline: at(now, p.Due),
		})
		if err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
		var started, finished pgtype.Timestamptz
		switch p.Status {
		case "completed":
			finished = pgtype.Timestamptz{Time: now.Add(-p.DoneAgo), Valid: true}
			started = pgtype.Timestamptz{Time: now.Add(-p.DoneAgo - Day), Valid: true}
		case "in_progress", "rejected":
			started = pgtype.Timestamptz{Time: now.Add(-Day), Valid: true}
		}
		if _, err := pool.Exec(ctx,
			`UPDATE job_step_assignments SET started_at = $2, completed_at = $3, rejection_reason = $4, handoff_note = NULLIF($5, '') WHERE id = $1`,
			a.ID, started, finished, p.Reason, p.Note); err != nil {
			return fmt.Errorf("step %d times: %w", i, err)
		}
		if p.Status == "in_progress" && current == nil {
			current = &a.ID
		}
	}
	var progressDone, progressTotal *int32
	if sc.Total > 0 {
		progressDone, progressTotal = &sc.Done, &sc.Total
	}
	var errMsg *string
	if sc.ErrMsg != "" {
		errMsg = &sc.ErrMsg
	}
	if _, err := pool.Exec(ctx,
		`UPDATE jobs SET status = $2, current_step = $3, error_message = $4, current_step_id = $5,
		        translated_count = $6, total_count = $7 WHERE id = $1`,
		job.ID, sc.Status, sc.Label, errMsg, current, progressDone, progressTotal); err != nil {
		return fmt.Errorf("job state: %w", err)
	}
	return nil
}

// seedTranslations writes one row per segment from the plan. Machine text is
// the source tagged with the language, like the dev mock engine's output; a
// person's confirm stamps edited_by, as UpdateTranslationHuman does.
func seedTranslations(ctx context.Context, pool *pgxpool.Pool, segs []segment, lang string, plan TextPlan, owner int64) error {
	for i, s := range segs {
		status := db.TranslationStatusNeedsReview
		var text *string
		var origin *db.TranslationOrigin
		var editedBy *int64
		if i < plan.Filled || i < plan.Approved+plan.AutoApproved {
			t := fmt.Sprintf("[%s] %s", strings.ToUpper(lang), s.source)
			mt := db.TranslationOriginMt
			text, origin = &t, &mt
		}
		switch {
		case i < plan.Approved:
			status, editedBy = db.TranslationStatusApproved, auth.UserIDPtr(owner)
		case i < plan.Approved+plan.AutoApproved:
			status = db.TranslationStatusAutoApproved
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO translations (segment_id, target_language, status, active_text, origin, edited_by)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			s.id, lang, status, text, origin, editedBy); err != nil {
			return fmt.Errorf("segment %d: %w", s.id, err)
		}
	}
	return nil
}

func at(now time.Time, d *time.Duration) pgtype.Timestamptz {
	if d == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: now.Add(*d), Valid: true}
}

// tenantOwner is who the project is attributed to; 0 (no owner) leaves
// created_by NULL.
func tenantOwner(ctx context.Context, pool *pgxpool.Pool, tenantID int64) int64 {
	var id int64
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM memberships WHERE tenant_id = $1 AND role = 'owner' ORDER BY user_id LIMIT 1`,
		tenantID).Scan(&id); err != nil {
		logging.Warn(ctx, err, "no owner found for the tenant; project will have no creator", "devseed-no-owner")
		return 0
	}
	return id
}

// SeedOrg is the organization `make seed` creates, found by its slug: a dev
// database can hold older tenants with lower ids.
func SeedOrg(ctx context.Context, pool *pgxpool.Pool) (int64, string, error) {
	var id int64
	var name string
	err := pool.QueryRow(ctx, "SELECT id, name FROM tenants WHERE slug = $1", seedid.OrgSlug).Scan(&id, &name)
	return id, name, err
}

// ensureProject keeps an existing project of this name (and its id), clearing
// its files; jobs, tasks, segments and translations go with them via ON
// DELETE CASCADE.
func ensureProject(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID, userID int64, spec Spec) (db.Project, error) {
	var p db.Project
	err := pool.QueryRow(ctx,
		`SELECT id, settings_profile_id FROM projects WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL ORDER BY id LIMIT 1`,
		tenantID, spec.ProjectName).Scan(&p.ID, &p.SettingsProfileID)
	if err == nil {
		_, delErr := pool.Exec(ctx, "DELETE FROM files WHERE project_id = $1", p.ID)
		if delErr != nil {
			return db.Project{}, fmt.Errorf("clear files: %w", delErr)
		}
		if _, err := pool.Exec(ctx, "UPDATE projects SET created_by = $2 WHERE id = $1", p.ID, auth.UserIDPtr(userID)); err != nil {
			return db.Project{}, fmt.Errorf("set creator: %w", err)
		}
		return p, nil
	}
	sp, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{TenantID: tenantID, FuzzyThreshold: 0.75})
	if err != nil {
		return db.Project{}, fmt.Errorf("settings profile: %w", err)
	}
	return q.CreateProject(ctx, db.CreateProjectParams{
		Name: spec.ProjectName, SourceLanguage: "en", TargetLanguages: spec.TargetLangs, TenantID: tenantID,
		SettingsProfileID: sp.ID, Status: db.ProjectStatusInProgress, CreatedBy: auth.UserIDPtr(userID),
	})
}
