// Command seed-spellcheck creates a dedicated project for manually testing the
// spell check feature.
//
//	make seed-spellcheck
//
// Idempotent: re-running clears the project's file and rebuilds it, so edits
// made while testing are thrown away rather than accumulating. The PROJECT id is
// stable across runs (only its files are replaced), so /projects/<id> keeps
// working; the job id does change, which is why the exact editor URL is printed
// at the end of every run.
//
// Deliberately a separate project rather than more segments on demo.txt. The
// demo file is shared by dozens of other tests and journeys, and its targets are
// mostly `[DE] <english>` MT-fallback placeholders — LanguageTool flags the
// English words in those, which buries real findings in noise.
//
// Every German typo here is verified to be flagged by LanguageTool's de-DE
// speller with the intended top suggestion. Test data that produces no findings
// is worse than none: it looks like a broken feature.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/auth"
	"openloc/internal/config"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/devseed"
	"openloc/internal/ptr"
	"openloc/internal/settings"
	"openloc/internal/stepdef"
	"openloc/internal/wordcount"
)

const (
	projectName = "Spellcheck QA"
	fileName    = "spellcheck-qa.txt"
	targetLang  = "de"
)

// qaSegment is one row of test content.
//
// `status` and `locked` exist so the scope options and the skip behaviour are
// actually exercisable: "unconfirmed only" is indistinguishable from "all"
// unless some segments are approved, and the locked-skip count stays at zero
// unless something is locked.
type qaSegment struct {
	src     string
	srcRich string // optional; set when the source carries inline tags
	tgt     string
	tgtRich string // optional; set when the target carries inline tags
	status  string // defaults to needs_review
	locked  bool
	note    string // why this row exists — printed in the summary
}

// The word `adresse` recurs in five segments on purpose: "apply to all" is
// meaningless against a single occurrence.
var qaSegments = []qaSegment{
	// --- Clean rows, so not every segment is an error ---
	{src: "Thank you for your order.", tgt: "Vielen Dank für Ihre Bestellung.", note: "clean"},
	{src: "Your package is on its way.", tgt: "Ihr Paket ist unterwegs.", note: "clean"},

	// --- Plain single-word typos ---
	{src: "Please confirm the delivery address.", tgt: "Bitte bestätigen Sie die adresse für die Lieferung.", note: "adresse (1 of 5)"},
	{src: "Your order has been shipped.", tgt: "Ihre bestellung wurde versandt.", note: "lowercase noun"},
	{src: "The invoice is attached.", tgt: "Die rechnung liegt bei.", note: "lowercase noun"},
	{src: "We have received your payment.", tgt: "Wir haben Ihre Zahlnug erhalten.", note: "transposed letters"},
	{src: "Please enter your password.", tgt: "Bitte geben Sie Ihr Passwrot ein.", note: "transposed letters"},
	{src: "Delivery takes place tomorrow.", tgt: "Die Liferung erfolgt morgen.", note: "missing letter"},
	{src: "We confirm the appointment.", tgt: "Wir bestatigen den Termin.", note: "missing umlaut"},
	{src: "Customer service is available.", tgt: "Der kundendienst ist erreichbar.", note: "lowercase noun"},
	{src: "The package was sent to the address.", tgt: "Das Paket wurde an die adresse gesendet.", note: "adresse (2 of 5)"},

	// --- Two occurrences of the same typo in ONE segment ---
	// Exercises the back-to-front offset ordering in buildSpellFixes: fixing the
	// first occurrence shifts the second one's offset.
	{
		src:  "The address is wrong, please check the address.",
		tgt:  "Die adresse ist falsch, bitte die adresse prüfen.",
		note: "adresse TWICE in one segment (3 of 5)",
	},

	// --- Umlauts BEFORE the typo ---
	// LanguageTool reports offsets in UTF-16 code units; anything doing byte
	// arithmetic on them breaks here and nowhere else.
	{
		src:  "Check the confirmation in the attachment.",
		tgt:  "Prüfen Sie die Bestätiugng im Anhang.",
		note: "multi-byte chars before the typo (UTF-16 offsets)",
	},

	// --- Inline tags with the typo AFTER the tag ---
	// The tag costs a ProseMirror position but no plain-text characters, so a
	// naive `offset + 1` mapping underlines and replaces the wrong characters.
	{
		src:     "Send the {orderId} confirmation to the address.",
		srcRich: `Send the <ph id="1">{orderId}</ph> confirmation to the address.`,
		tgt:     "Senden Sie die {orderId} Bestätigung an die adresse.",
		tgtRich: `Senden Sie die <ph id="1">{orderId}</ph> Bestätigung an die adresse.`,
		note:    "ph placeholder BEFORE the typo (4 of 5)",
	},
	{
		src:     "Click here to reset your password.",
		srcRich: `Click <bpt id="2">&lt;b&gt;</bpt>here<ept id="2">&lt;/b&gt;</ept> to reset your password.`,
		tgt:     "Klicken Sie hier, um Ihr Passwrot zurückzusetzen.",
		tgtRich: `Klicken Sie <bpt id="2">&lt;b&gt;</bpt>hier<ept id="2">&lt;/b&gt;</ept>, um Ihr Passwrot zurückzusetzen.`,
		note:    "bpt/ept pair BEFORE the typo",
	},
	{
		src:     "Your reference is {ref}.",
		srcRich: `Your reference is <ph id="3">{ref}</ph>.`,
		tgt:     "Ihre rechnung hat die Nummer {ref}.",
		tgtRich: `Ihre rechnung hat die Nummer <ph id="3">{ref}</ph>.`,
		note:    "typo BEFORE the tag (opposite order)",
	},

	// --- Locked: must be skipped and counted, never offered as fixable ---
	{
		src:    "The billing address cannot be changed.",
		tgt:    "Die adresse für die Rechnung kann nicht geändert werden.",
		locked: true,
		note:   "LOCKED — expect it in the skipped count (5 of 5)",
	},

	// --- Approved: excluded by the "unconfirmed only" scope ---
	{
		src:    "We have updated your details.",
		tgt:    "Wir haben Ihre Daten aktualisiert, die bestellung bleibt gleich.",
		status: "approved",
		note:   "APPROVED — excluded by 'unconfirmed only'",
	},
	{
		src:    "Your subscription renews monthly.",
		tgt:    "Ihr Abo verlängert sich monatlich, die rechnung folgt.",
		status: "approved",
		note:   "APPROVED — excluded by 'unconfirmed only'",
	},

	// --- Filler: realistic bulk so the list is a few dozen lines ---
	{src: "We are here to help.", tgt: "Wir sind für Sie da.", note: "clean"},
	{src: "Sign in to your account.", tgt: "Melden Sie sich bei Ihrem Konto an.", note: "clean"},
	{src: "Update your preferences at any time.", tgt: "Aktualisieren Sie Ihre Einstellungen jederzeit.", note: "clean"},
	{src: "This message was sent automatically.", tgt: "Diese Nachricht wurde automatisch versandt.", note: "clean"},
	{src: "Do not reply to this email.", tgt: "Antworten Sie nicht auf diese E-Mail.", note: "clean"},
	{src: "Our office is closed on Sunday.", tgt: "Unser Büro ist am Sonntag geschlossen.", note: "clean"},
	{src: "Track your shipment online.", tgt: "Verfolgen Sie Ihre Sendung online.", note: "clean"},
	{src: "Returns are free within 30 days.", tgt: "Rücksendungen sind innerhalb von 30 Tagen kostenlos.", note: "clean"},
	{src: "Please keep this receipt.", tgt: "Bitte bewahren Sie diesen beleg auf.", note: "lowercase noun"},
	{src: "The warranty lasts two years.", tgt: "Die garantie gilt zwei Jahre.", note: "lowercase noun"},
	{src: "Contact us with any questions.", tgt: "Kontaktieren Sie uns bei Fragen.", note: "clean"},
	{src: "We appreciate your patience.", tgt: "Wir danken Ihnen für Ihre gedult.", note: "misspelling"},
	{src: "Your feedback helps us improve.", tgt: "Ihr Feedback hilft uns, besser zu werden.", note: "clean"},
	{src: "Delivery is free for members.", tgt: "Für Mitglieder ist die Liferung kostenlos.", note: "missing letter"},
	{src: "Choose a payment method.", tgt: "Wählen Sie eine Zahlungsart.", note: "clean"},
	{src: "Review your basket before checkout.", tgt: "Prüfen Sie Ihren Warenkorb vor dem Bezahlen.", note: "clean"},
	{src: "A confirmation will follow shortly.", tgt: "Eine bestatigung folgt in Kürze.", note: "missing umlaut"},
	{src: "Your account is now active.", tgt: "Ihr Konto ist jetzt aktiv.", note: "clean"},
	{src: "Save your changes before leaving.", tgt: "Speichern Sie Ihre Änderungen vor dem Verlassen.", note: "clean"},
	{src: "The support team will respond within one day.", tgt: "Der kundendienst antwortet innerhalb eines Tages.", note: "lowercase noun"},
}

// summary is what a run built, for the printout.
type summary struct {
	tenantID                   int64
	tenantName                 string
	projectID, jobID           int64
	words                      int
	locked, approved, withTags int
}

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	s, err := seed(ctx, pool)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf(`
✓ Project %q ready — organization %q (tenant %d)
  File:     %s — %d segments, %d words
  Job:      en → %s
  Locked:   %d (expect these in the "skipped" count)
  Approved: %d (excluded by the "unconfirmed only" scope)
  Tagged:   %d (inline tags around the typo)

  Recurring typo: "adresse" in 5 segments — that's the one to try "apply to all" on.

  Sign in to the %q organization — the project lives only in that tenant, and
  every job endpoint is tenant-scoped, so another org gives "job not found".

  Open the editor directly:
    %s/projects/%d/jobs/%d/edit
`, projectName, s.tenantName, s.tenantID, fileName, len(qaSegments), s.words, targetLang,
		s.locked, s.approved, s.withTags, s.tenantName, baseURL(), s.projectID, s.jobID)
}

// seed builds the project in the first tenant. `make seed` must have run.
func seed(ctx context.Context, pool *pgxpool.Pool) (summary, error) {
	q := db.New(pool)
	var s summary

	tenantID, tenantName, err := devseed.SeedOrg(ctx, pool)
	if err != nil {
		return s, fmt.Errorf("find tenant: %w (run `make seed` first)", err)
	}
	s.tenantID, s.tenantName = tenantID, tenantName

	owner := tenantOwner(ctx, pool, tenantID)
	project, err := ensureProject(ctx, q, pool, tenantID, owner)
	if err != nil {
		return s, fmt.Errorf("ensure project: %w", err)
	}
	s.projectID = project.ID
	if err := applyWorkflow(ctx, q, pool, tenantID, project.SettingsProfileID); err != nil {
		return s, err
	}

	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID:    project.ID,
		Name:         fileName,
		Format:       "txt",
		StoragePath:  fmt.Sprintf("seed/spellcheck-qa-%d.txt", project.ID),
		SegmentCount: int32(len(qaSegments)),
	})
	if err != nil {
		return s, fmt.Errorf("create file: %w", err)
	}

	for pos, seg := range qaSegments {
		wc := wordcount.CountWords(seg.src)
		s.words += wc

		var segID int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO segments (file_id, source_text, source_rich_text, segment_key, position, word_count)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			file.ID, seg.src, ptr.NilIfZero(seg.srcRich), fmt.Sprintf("%d", pos+1), pos, wc,
		).Scan(&segID); err != nil {
			return s, fmt.Errorf("insert segment %d: %w", pos, err)
		}

		status := seg.status
		if status == "" {
			status = "needs_review"
		}
		// A locked segment is always approved: locking approves it.
		if seg.locked {
			status = "approved"
		}
		// edited_by is left NULL so rows derive as "pre_translated" in the UI
		// rather than looking like someone already worked on them.
		if _, err := pool.Exec(ctx,
			`INSERT INTO translations (segment_id, target_language, status, active_text, active_rich_text, origin, locked)
			 VALUES ($1, $2, $3, $4, $5, 'mt', $6)`,
			segID, targetLang, status, seg.tgt, ptr.NilIfZero(seg.tgtRich), seg.locked,
		); err != nil {
			return s, fmt.Errorf("insert translation %d: %w", pos, err)
		}

		if seg.locked {
			s.locked++
		}
		if status == "approved" {
			s.approved++
		}
		if seg.tgtRich != "" {
			s.withTags++
		}
	}

	s.jobID, err = createJob(ctx, pool, project.SettingsProfileID, file.ID, s.words, owner)
	return s, err
}

// applyWorkflow points the project at the library's Standard workflow, as
// picking it in the New project form does. The project's files are cleared
// first, so no job holds a step yet.
func applyWorkflow(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID, profileID int64) error {
	wf, err := q.GetWorkflowByName(ctx, db.GetWorkflowByNameParams{TenantID: tenantID, Name: demodata.WorkflowStandard})
	if err != nil {
		return fmt.Errorf("find the %q workflow: %w (run `make seed` first)", demodata.WorkflowStandard, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if _, err := settings.ApplyWorkflow(ctx, db.New(tx), tenantID, wf.ID, profileID); err != nil {
		return fmt.Errorf("apply the %q workflow: %w", demodata.WorkflowStandard, err)
	}
	return tx.Commit(ctx)
}

// createJob makes the file's job with a task per workflow step, as an upload
// does: the first task is current and held by its default assignee, else the
// owner; later tasks wait with their default assignee. Automated steps have
// nobody.
func createJob(ctx context.Context, pool *pgxpool.Pool, profileID, fileID int64, words int, owner int64) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	q := db.New(tx)

	job, err := q.CreateJob(ctx, db.CreateJobParams{FileID: fileID, TargetLanguage: targetLang, WordCount: int32(words)})
	if err != nil {
		return 0, fmt.Errorf("create job: %w", err)
	}
	steps, err := q.ListSettingsWorkflowSteps(ctx, profileID)
	if err != nil {
		return 0, fmt.Errorf("list workflow steps: %w", err)
	}
	var current *int64
	for i, step := range steps {
		status, assignee := "pending", step.DefaultAssignee
		if i == 0 {
			status = "in_progress"
			if assignee == nil {
				assignee = auth.UserIDPtr(owner)
			}
		}
		if stepdef.IsAutomated(step.StepType) {
			assignee = nil
		}
		task, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: job.ID, WorkflowStepID: step.ID, AssignedTo: assignee, Status: status,
		})
		if err != nil {
			return 0, fmt.Errorf("create task for step %q: %w", step.Name, err)
		}
		if i == 0 {
			current = &task.ID
		}
	}
	// needs_attention, not completed — a completed job opens read-only.
	if _, err := tx.Exec(ctx, "UPDATE jobs SET status = 'needs_attention', current_step_id = $2 WHERE id = $1",
		job.ID, current); err != nil {
		return 0, fmt.Errorf("set job state: %w", err)
	}
	return job.ID, tx.Commit(ctx)
}

// tenantOwner is the identity this seed attributes its project to, so the
// project list shows a creator instead of an em-dash.
// Best-effort: a tenant with no owner membership yields 0, which
// auth.UserIDPtr turns into a NULL created_by — the pre-attribution behaviour.
func tenantOwner(ctx context.Context, pool *pgxpool.Pool, tenantID int64) int64 {
	var id int64
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM memberships WHERE tenant_id = $1 AND role = 'owner' ORDER BY user_id LIMIT 1`,
		tenantID).Scan(&id); err != nil {
		log.Printf("⚠ No owner found for tenant %d — project will have no creator: %v", tenantID, err)
		return 0
	}
	return id
}

// ensureProject reuses an existing "Spellcheck QA" project, clearing only its
// files, and creates one only when absent.
//
// Deliberately NOT delete-project-then-recreate: that hands out a new project id
// on every run, so any URL you had open — or were given — silently points at a
// project that no longer exists. Clearing the files instead keeps the project id
// (and therefore the URL) stable across re-seeds, while still throwing away test
// edits. Segments, translations and jobs go with the files via ON DELETE CASCADE.
func ensureProject(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID, userID int64) (db.Project, error) {
	var existing db.Project
	err := pool.QueryRow(ctx,
		`SELECT id, name, source_language, target_languages, tenant_id, settings_profile_id, status
		 FROM projects WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL`, tenantID, projectName,
	).Scan(&existing.ID, &existing.Name, &existing.SourceLanguage, &existing.TargetLanguages,
		&existing.TenantID, &existing.SettingsProfileID, &existing.Status)
	if err == nil {
		tag, delErr := pool.Exec(ctx, "DELETE FROM files WHERE project_id = $1", existing.ID)
		if delErr != nil {
			return db.Project{}, fmt.Errorf("clear files: %w", delErr)
		}
		log.Printf("↻ Reusing project %d, cleared %d old file(s)", existing.ID, tag.RowsAffected())
		return existing, nil
	}

	return createProject(ctx, q, tenantID, userID)
}

func createProject(ctx context.Context, q *db.Queries, tenantID, userID int64) (db.Project, error) {
	sp, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: 0.75,
		MtOnUpload:     false,
		TmOnUpload:     false,
	})
	if err != nil {
		return db.Project{}, fmt.Errorf("settings profile: %w", err)
	}
	return q.CreateProject(ctx, db.CreateProjectParams{
		Name:              projectName,
		SourceLanguage:    "en",
		TargetLanguages:   []string{targetLang},
		TenantID:          tenantID,
		SettingsProfileID: sp.ID,
		Status:            db.ProjectStatusInProgress,
		CreatedBy:         auth.UserIDPtr(userID),
	})
}

func baseURL() string {
	if v := os.Getenv("BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:9000"
}
