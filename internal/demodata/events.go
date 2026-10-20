package demodata

import (
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
)

// projectAge is how many days ago a project was created and last updated. The
// booking project is the most recently updated live project, so it is the one
// the saved tours open.
type projectAge struct{ created, updated int }

// projectHistory writes each project's History tab — created, files uploaded,
// steps assigned and, for the finished project, jobs completed — and backdates
// the project and its events to its age.
func (b *builder) projectHistory() error {
	bt := b.built
	ages := map[int64]projectAge{
		bt.booking.ID: {20, 0},
		bt.ifu.ID:     {12, 1},
		bt.patch.ID:   {40, 10},
		bt.faq.ID:     {6, 4},
	}
	for _, p := range []db.Project{bt.booking, bt.ifu, bt.patch, bt.faq} {
		if err := b.projectEvents(p); err != nil {
			return err
		}
		age := ages[p.ID]
		if err := b.exec(`UPDATE projects SET created_at = $2, updated_at = $3 WHERE id = $1 AND tenant_id = $4`,
			p.ID, b.daysAgo(age.created), b.daysAgo(age.updated), b.tenantID); err != nil {
			return err
		}
		// Events keep their insertion order, one hour apart from the day the
		// project was created.
		if err := b.exec(`
			UPDATE project_events e SET created_at = $2::timestamptz + make_interval(hours => s.rn::int)
			FROM (SELECT id, row_number() OVER (ORDER BY id) AS rn FROM project_events WHERE project_id = $1 AND tenant_id = $3) s
			WHERE e.id = s.id`, p.ID, b.daysAgo(age.created), b.tenantID); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) projectEvents(p db.Project) error {
	actor := b.ownerID
	if p.CreatedBy != nil {
		actor = *p.CreatedBy
	}
	emit := func(ev projectevents.Event) error {
		ev.ProjectID, ev.TenantID, ev.ActorID = p.ID, b.tenantID, &actor
		if err := projectevents.EmitInTx(b.ctx, b.q, ev); err != nil {
			return fmt.Errorf("project event %s: %w", ev.Type, err)
		}
		return nil
	}
	if err := emit(projectevents.Event{Type: projectevents.TypeProjectCreated, Details: map[string]any{
		"name": p.Name, "source_language": p.SourceLanguage, "target_languages": p.TargetLanguages,
	}}); err != nil {
		return err
	}
	rows, err := b.tx.Query(b.ctx, `
		SELECT f.id, f.name, f.format::text, f.segment_count, j.id, j.target_language,
		       COALESCE(ws.name, ''), COALESCE(u.email, ''),
		       COALESCE(NULLIF(btrim(u.name), ''), u.email, ''), j.status
		FROM files f
		JOIN jobs j ON j.file_id = f.id
		LEFT JOIN job_step_assignments a ON a.id = j.current_step_id
		LEFT JOIN workflow_steps ws ON ws.id = a.workflow_step_id
		LEFT JOIN users u ON u.id = a.assigned_to
		WHERE f.project_id = $1
		ORDER BY f.id, j.id`, p.ID)
	if err != nil {
		return fmt.Errorf("list project jobs: %w", err)
	}
	type jobRow struct {
		fileID, jobID                         int64
		file, format, lang, step, email, name string
		status                                string
		segments                              int32
	}
	var jobs []jobRow
	for rows.Next() {
		var r jobRow
		if err := rows.Scan(&r.fileID, &r.file, &r.format, &r.segments, &r.jobID, &r.lang, &r.step, &r.email, &r.name, &r.status); err != nil {
			rows.Close()
			return fmt.Errorf("scan project job: %w", err)
		}
		jobs = append(jobs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("project jobs: %w", err)
	}

	// One upload event per file, then one assignment per job.
	for i := 0; i < len(jobs); {
		f := jobs[i]
		var fileJobs []map[string]any
		for ; i < len(jobs) && jobs[i].fileID == f.fileID; i++ {
			fileJobs = append(fileJobs, map[string]any{"job_id": jobs[i].jobID, "target_language": jobs[i].lang})
		}
		fileID := f.fileID
		if err := emit(projectevents.Event{Type: projectevents.TypeFileUploaded, FileID: &fileID, Details: map[string]any{
			"file_name": f.file, "format": f.format, "segment_count": f.segments, "jobs": fileJobs,
		}}); err != nil {
			return err
		}
	}
	for _, j := range jobs {
		jobID, fileID := j.jobID, j.fileID
		if j.step != "" {
			if err := emit(projectevents.Event{Type: projectevents.TypeStepAssigned, FileID: &fileID, JobID: &jobID, Details: map[string]any{
				"file_name": j.file, "target_language": j.lang, "step": j.step, "assignee_email": j.email,
				"assignee_name": j.name,
			}}); err != nil {
				return err
			}
		}
		if j.status == "completed" {
			if err := emit(projectevents.Event{Type: projectevents.TypeJobCompleted, FileID: &fileID, JobID: &jobID, Details: map[string]any{
				"file_name": j.file, "target_language": j.lang,
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}
