package demodata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
)

// builder writes the dataset through one transaction. Every method returns
// its error; the first one aborts Create and rolls everything back.
type builder struct {
	ctx        context.Context
	q          *db.Queries
	tx         pgx.Tx
	now        time.Time
	tenantID   int64
	ownerID    int64
	linguistID int64
	author     string
	skipAI     bool
	omitLabel  bool
	org        *Organization
	res        Result
	demo       *labelPick
	// Set as each project is wired, and read while its files are seeded, so a
	// history row names the memory and engine that project actually uses.
	tmID       *int64
	mtEngineID *int64
	// histSeq numbers the translations that get history, so organization mode
	// can spread them over the charts' 90-day window.
	histSeq int
	ai      struct{ nordwind, helix, translation *int64 }
	lib     Library
	built   built
}

// built is what the organization pass reads back from the project pass.
type built struct {
	booking, ifu, patch, faq db.Project
	bookingJobs, emailJobs   map[string]int64
	ifuJobs, patchJobs       map[string]int64
	faqJobs                  map[string]int64
	tmNordwind, tmHelix      int64
	termBase                 int64
	qaProfile                int64
	mtRouter                 *int64
	customers                map[string]customerRef
}

// customerRef is a customer both ways it exists: the legacy customers row that
// translation memories point at, and the Customer label value projects pick.
type customerRef struct {
	id    int64
	label labelPick
}

// frLinguist is who translates French: the organization's French linguist,
// else base.
func (b *builder) frLinguist(base int64) int64 {
	if b.org != nil && b.org.FrenchLinguistUserID != 0 {
		return b.org.FrenchLinguistUserID
	}
	return base
}

// creator is who created a project: the organization's project manager for
// managed projects, else the owner.
func (b *builder) creator(managed bool) int64 {
	if managed && b.org != nil && b.org.ManagerUserID != 0 {
		return b.org.ManagerUserID
	}
	return b.ownerID
}

func (b *builder) build() error {
	var err error
	if !b.omitLabel {
		pick, err := b.demoLabel()
		if err != nil {
			return err
		}
		b.demo = &pick
	}
	var nordwind, helix, kaido customerRef
	for _, c := range []struct {
		dst  *customerRef
		name string
	}{{&nordwind, customerNordwind}, {&helix, customerHelix}, {&kaido, customerKaido}} {
		if *c.dst, err = b.customer(c.name); err != nil {
			return err
		}
	}
	b.built.customers = map[string]customerRef{customerNordwind: nordwind, customerHelix: helix, customerKaido: kaido}

	tmN, err := b.tm(tmNordwind, []string{"de", "fr"}, nordwind.id, nordwindTM)
	if err != nil {
		return err
	}
	tmH, err := b.tm(tmHelix, []string{"de"}, helix.id, helixTM)
	if err != nil {
		return err
	}
	tb, err := b.termBase()
	if err != nil {
		return err
	}
	qa, err := b.qaProfile()
	if err != nil {
		return err
	}
	b.built.tmNordwind, b.built.tmHelix, b.built.termBase, b.built.qaProfile = tmN, tmH, tb.ID, qa
	var deepl *db.MtEngine
	if b.org != nil {
		e, err := b.keylessMTEngine()
		if err != nil {
			return err
		}
		deepl = &e
	}
	if !b.skipAI {
		if err := b.aiEngines(); err != nil {
			return err
		}
	}
	if b.org != nil {
		if b.lib, err = EnsureWorkflowLibrary(b.ctx, b.q, b.tenantID, b.ai.nordwind); err != nil {
			return err
		}
	}
	engines, err := b.mtEngines(deepl)
	if err != nil {
		return err
	}
	if len(engines) == 0 {
		b.res.Skipped = append(b.res.Skipped, "The organization has no MT engine, so the demo projects have no MT router.")
	}

	// Project 1: in progress, two files, two languages, TM+terms+QA wired, live tasks.
	p1, err := b.project(projectBooking, "Web and email strings for the summer campaign.", []string{"de", "fr"}, nordwind, db.ProjectStatusInProgress, 6, "Travel", false)
	if err != nil {
		return err
	}
	if err := b.wire(p1, tmN, &tb, &qa, engines, []string{"de", "fr"}); err != nil {
		return err
	}
	if b.org != nil && b.built.mtRouter != nil {
		if err := b.routers(*b.built.mtRouter); err != nil {
			return err
		}
	}
	steps1, err := b.workflow(p1, b.linguistID, b.ownerID)
	if err != nil {
		return err
	}
	j1, err := b.file(p1, fileBooking, db.FileFormatJsonI18n, bookingRows, []string{"de", "fr"})
	if err != nil {
		return err
	}
	j2, err := b.file(p1, fileEmails, db.FileFormatHtml, emailRows, []string{"de", "fr"})
	if err != nil {
		return err
	}
	if err := b.assignAll(
		assignment{j1["de"], steps1, 0, b.linguistID, 3, "in_progress"},
		assignment{j1["fr"], steps1, 0, b.frLinguist(b.ownerID), 5, "in_progress"},
		assignment{j2["de"], steps1, 1, b.ownerID, 4, "in_progress"},
		assignment{j2["fr"], steps1, 0, b.frLinguist(b.linguistID), 5, "in_progress"},
	); err != nil {
		return err
	}
	// The booking file is the one the editor screenshots open, so it carries a
	// finished QA run — the sidebar and the grid's issue dots have content.
	for _, lang := range []string{"de", "fr"} {
		if err := b.runQA(j1[lang]); err != nil {
			return err
		}
	}
	if err := b.comment(j1["de"], "Only {count} rooms left at this price!", "de", b.ownerID, "Marketing wants the urgency kept — please don't soften this one."); err != nil {
		return err
	}
	b.addProject(p1, j1, j2)
	b.built.booking, b.built.bookingJobs, b.built.emailJobs = p1, j1, j2

	// Project 2: regulated content, DE only, mostly confirmed, Review step active.
	p2, err := b.project(projectIFU, "Instructions for use, chapter 3 (preparing the pump).", []string{"de"}, helix, db.ProjectStatusInProgress, 14, "Medical devices", true)
	if err != nil {
		return err
	}
	if err := b.wire(p2, tmH, nil, &qa, engines, []string{"de"}); err != nil {
		return err
	}
	steps2, err := b.reviewWorkflow(p2)
	if err != nil {
		return err
	}
	j3, err := b.file(p2, fileIFU, db.FileFormatDocx, ifuRows, []string{"de"})
	if err != nil {
		return err
	}
	if err := b.assignAll(assignment{j3["de"], steps2, 1, b.ownerID, 9, "in_progress"}); err != nil {
		return err
	}
	b.addProject(p2, j3)
	b.built.ifu, b.built.ifuJobs = p2, j3

	// Project 3: completed.
	p3, err := b.project(projectPatch, "", []string{"de", "fr"}, kaido, db.ProjectStatusCompleted, -10, "Games", false)
	if err != nil {
		return err
	}
	if err := b.wire(p3, tmN, nil, nil, engines, []string{"de", "fr"}); err != nil {
		return err
	}
	steps3, err := b.workflow(p3, b.ownerID, b.ownerID)
	if err != nil {
		return err
	}
	j4, err := b.file(p3, filePatch, db.FileFormatMarkdown, patchRows, []string{"de", "fr"})
	if err != nil {
		return err
	}
	if err := b.assignAll(
		assignment{j4["de"], steps3, 1, b.ownerID, -12, "completed"},
		assignment{j4["fr"], steps3, 1, b.ownerID, -12, "completed"},
	); err != nil {
		return err
	}
	if err := b.exec(`UPDATE jobs SET status = 'completed' WHERE id = ANY($1)`, []int64{j4["de"], j4["fr"]}); err != nil {
		return err
	}
	b.addProject(p3, j4)
	b.built.patch, b.built.patchJobs = p3, j4

	// Project 4: parked. The sample workspace has no files yet; a whole
	// organization has the first draft of the FAQ in it.
	p4, err := b.project(projectFAQ, "Waiting for the final FAQ copy from Nordwind.", []string{"de"}, nordwind, db.ProjectStatusOnHold, 30, "Travel", true)
	if err != nil {
		return err
	}
	if err := b.wire(p4, tmN, &tb, &qa, engines, []string{"de"}); err != nil {
		return err
	}
	b.built.faq = p4
	if b.org != nil {
		steps4, err := b.workflow(p4, b.linguistID, b.ownerID)
		if err != nil {
			return err
		}
		if b.built.faqJobs, err = b.file(p4, fileFAQ, db.FileFormatDocx, faqRows, []string{"de"}); err != nil {
			return err
		}
		if err := b.assignAll(assignment{b.built.faqJobs["de"], steps4, 0, b.linguistID, 30, "in_progress"}); err != nil {
			return err
		}
		b.addProject(p4, b.built.faqJobs)
	} else {
		b.addProject(p4)
	}

	b.res.TranslationMemories = []Entity{{tmN, tmNordwind}, {tmH, tmHelix}}
	b.res.TermBases = []Entity{{tb.ID, termBase}}
	b.res.QAProfiles = []Entity{{qa, qaProfile}}
	if b.org != nil {
		return b.organization()
	}
	return nil
}

// mtEngines lists the tenant's MT engines; the keyless DeepL engine, when
// given, comes first so every router defaults to it.
func (b *builder) mtEngines(first *db.MtEngine) ([]db.MtEngine, error) {
	engines, err := b.q.ListMTEngines(b.ctx, b.tenantID)
	if err != nil {
		return nil, fmt.Errorf("list mt engines: %w", err)
	}
	if first == nil {
		return engines, nil
	}
	out := []db.MtEngine{*first}
	for _, e := range engines {
		if e.ID != first.ID {
			out = append(out, e)
		}
	}
	return out, nil
}

// addProject records a project and its jobs in the result, in a stable order.
func (b *builder) addProject(p db.Project, jobSets ...map[string]int64) {
	out := Project{ID: p.ID, Name: p.Name, Status: string(p.Status), JobIDs: []int64{}}
	for _, jobs := range jobSets {
		for _, lang := range p.TargetLanguages {
			if id, ok := jobs[lang]; ok {
				out.JobIDs = append(out.JobIDs, id)
			}
		}
	}
	b.res.Projects = append(b.res.Projects, out)
}

func (b *builder) exec(sql string, args ...any) error {
	if _, err := b.tx.Exec(b.ctx, sql, args...); err != nil {
		return fmt.Errorf("exec %q: %w", sql, err)
	}
	return nil
}

// record adds a created entity to the tenant's demo manifest.
func (b *builder) record(entityType string, id int64) error {
	if err := b.q.RecordDemoWorkspaceItem(b.ctx, db.RecordDemoWorkspaceItemParams{TenantID: b.tenantID, EntityType: entityType, EntityID: id}); err != nil {
		return fmt.Errorf("record %s %d: %w", entityType, id, err)
	}
	return nil
}

// deadline is d days from b.now. In organization mode a future deadline moves
// out by the dataset's lifetime, so it is still ahead on the day before the
// rebuild.
func (b *builder) deadline(d int) pgtype.Timestamptz {
	if b.org != nil && d > 0 {
		d += b.org.LifetimeDays
	}
	t := b.now.AddDate(0, 0, d).Truncate(time.Hour)
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// daysAgo is b.now minus d days, for backdating.
func (b *builder) daysAgo(d int) time.Time {
	return b.now.AddDate(0, 0, -d)
}

// customer finds or creates the legacy customers row and the Customer label
// value of the same name.
func (b *builder) customer(name string) (customerRef, error) {
	var ref customerRef
	existing, err := b.q.ListCustomers(b.ctx, b.tenantID)
	if err != nil {
		return ref, fmt.Errorf("list customers: %w", err)
	}
	for _, c := range existing {
		if strings.EqualFold(c.Name, name) {
			ref.id = c.ID
		}
	}
	if ref.id == 0 {
		c, err := b.q.CreateCustomer(b.ctx, db.CreateCustomerParams{TenantID: b.tenantID, Name: name})
		if err != nil {
			return ref, fmt.Errorf("customer %q: %w", name, err)
		}
		if err := b.record(ItemCustomer, c.ID); err != nil {
			return ref, err
		}
		ref.id = c.ID
	}
	ref.label, err = b.systemLabel("customer", name)
	return ref, err
}

func (b *builder) project(name, description string, targets []string, customer customerRef, status db.ProjectStatus, deadlineDays int, domain string, managed bool) (db.Project, error) {
	sp, err := b.q.CreateSettingsProfile(b.ctx, db.CreateSettingsProfileParams{TenantID: b.tenantID, FuzzyThreshold: 0.75, MtOnUpload: true, TmOnUpload: true})
	if err != nil {
		return db.Project{}, fmt.Errorf("settings profile: %w", err)
	}
	p, err := b.q.CreateProject(b.ctx, db.CreateProjectParams{
		Name: name, Description: description, SourceLanguage: "en", TargetLanguages: targets,
		TenantID: b.tenantID, SettingsProfileID: sp.ID, CustomerID: &customer.id, Status: status,
		CreatedBy: auth.UserIDPtr(b.creator(managed)), Deadline: b.deadline(deadlineDays), Domain: &domain,
	})
	if err != nil {
		return db.Project{}, fmt.Errorf("project %q: %w", name, err)
	}
	if err := b.record(ItemProject, p.ID); err != nil {
		return db.Project{}, err
	}
	picks, err := b.projectLabels(customer, domain)
	if err != nil {
		return db.Project{}, err
	}
	for _, pick := range picks {
		if err := b.q.SetProjectLabel(b.ctx, db.SetProjectLabelParams{ProjectID: p.ID, LabelDefinitionID: pick.defID, LabelValueID: pick.valueID}); err != nil {
			return db.Project{}, fmt.Errorf("project %q label: %w", name, err)
		}
	}
	return p, nil
}

// projectLabels is the Customer and Domain picks, plus Demo unless omitted.
func (b *builder) projectLabels(customer customerRef, domain string) ([]labelPick, error) {
	domainPick, err := b.systemLabel("domain", domain)
	if err != nil {
		return nil, err
	}
	picks := []labelPick{customer.label, domainPick}
	if b.demo != nil {
		picks = append(picks, *b.demo)
	}
	return picks, nil
}

// mtRouter returns the id of the tenant's "Default MT router" (default → the
// given engine), creating it on first use so every project shares it.
func (b *builder) mtRouter(mtEngineID int64) (*int64, error) {
	r, created, err := EnsureMTRouter(b.ctx, b.q, b.tenantID, MTRouterName, mtEngineID)
	if err != nil {
		return nil, err
	}
	if created {
		if err := b.record(ItemMTRouter, r.ID); err != nil {
			return nil, err
		}
	}
	b.built.mtRouter = &r.ID
	return &r.ID, nil
}

// wire attaches the TM (master, read+write), the term base, the QA profile and
// the MT router (default → the first MT engine) to the project's settings profile.
func (b *builder) wire(p db.Project, tmID int64, tb *db.TermBasis, qaID *int64, engines []db.MtEngine, targets []string) error {
	return b.wireProfile(p.SettingsProfileID, tmID, tb, qaID, engines, targets)
}

func (b *builder) wireProfile(profileID, tmID int64, tb *db.TermBasis, qaID *int64, engines []db.MtEngine, targets []string) error {
	b.tmID = &tmID
	b.mtEngineID = nil
	if len(engines) > 0 {
		b.mtEngineID = &engines[0].ID
	}
	if _, err := b.q.CreateSettingsTMAssignment(b.ctx, db.CreateSettingsTMAssignmentParams{SettingsProfileID: profileID, TmID: tmID, Mode: "read_write", Priority: 0}); err != nil {
		return fmt.Errorf("tm assignment: %w", err)
	}
	if tb != nil {
		for _, lang := range targets {
			if _, err := b.q.CreateSettingsTermAssignment(b.ctx, db.CreateSettingsTermAssignmentParams{SettingsProfileID: profileID, TermBaseID: tb.ID, SourceLanguage: "en", TargetLanguage: lang, Mode: "read", Priority: 0}); err != nil {
				return fmt.Errorf("term assignment: %w", err)
			}
		}
	}
	if len(engines) > 0 {
		routerID, err := b.mtRouter(engines[0].ID)
		if err != nil {
			return err
		}
		if err := b.q.SetSettingsProfileMTRouter(b.ctx, db.SetSettingsProfileMTRouterParams{ID: profileID, MtRouterID: routerID}); err != nil {
			return fmt.Errorf("mt router link: %w", err)
		}
	}
	if qaID != nil {
		if _, err := b.q.UpdateSettingsProfile(b.ctx, db.UpdateSettingsProfileParams{ID: profileID, FuzzyThreshold: 0.75, MtOnUpload: true, TmOnUpload: true, QaProfileID: qaID}); err != nil {
			return fmt.Errorf("qa link: %w", err)
		}
	}
	return nil
}
