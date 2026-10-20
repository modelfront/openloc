package publicapi

import (
	"encoding/json"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/job"
)

// The v1 response shapes. Each field here is part of the public contract and
// is described in openapi/*.yaml; the spec tests check every response against
// it. Fields may be added; none may be renamed or removed within v1.

// Project is the v1 project.
type Project struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Status          string   `json:"status"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	TemplateID      *int64   `json:"template_id"`
	Deadline        *string  `json:"deadline"`
	CreatedBy       *int64   `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func projectDTO(p db.Project) Project {
	targets := p.TargetLanguages
	if targets == nil {
		targets = []string{}
	}
	return Project{
		ID:              p.ID,
		Name:            p.Name,
		Description:     p.Description,
		Status:          string(p.Status),
		SourceLanguage:  p.SourceLanguage,
		TargetLanguages: targets,
		TemplateID:      p.TemplateID,
		Deadline:        optionalTimestamp(p.Deadline),
		CreatedBy:       p.CreatedBy,
		CreatedAt:       timestamp(p.CreatedAt),
		UpdatedAt:       timestamp(p.UpdatedAt),
	}
}

// projectListDTO maps a list row, which carries the project's columns plus
// display names the v1 shape does not include.
func projectListDTO(r db.ListProjectsFilteredRow) Project {
	return projectDTO(db.Project{
		ID: r.ID, Name: r.Name, Description: r.Description, Status: r.Status,
		SourceLanguage: r.SourceLanguage, TargetLanguages: r.TargetLanguages,
		TemplateID: r.TemplateID, Deadline: r.Deadline, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	})
}

// File is the v1 file.
type File struct {
	ID           int64   `json:"id"`
	ProjectID    int64   `json:"project_id"`
	Name         string  `json:"name"`
	OriginalName *string `json:"original_name"`
	Format       string  `json:"format"`
	SegmentCount int32   `json:"segment_count"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	// XLSXLayout is an Excel workbook's column layout; null for every other file.
	XLSXLayout json.RawMessage `json:"xlsx_layout"`
}

func fileDTO(f db.File) File {
	return File{
		ID:           f.ID,
		ProjectID:    f.ProjectID,
		Name:         f.Name,
		OriginalName: f.OriginalName,
		Format:       string(f.Format),
		SegmentCount: f.SegmentCount,
		CreatedAt:    timestamp(f.CreatedAt),
		UpdatedAt:    timestamp(f.UpdatedAt),
		XLSXLayout:   f.XlsxLayout,
	}
}

// Job is the v1 job.
type Job struct {
	ID               int64         `json:"id"`
	ProjectID        int64         `json:"project_id"`
	FileID           int64         `json:"file_id"`
	FileName         string        `json:"file_name"`
	SourceLanguage   string        `json:"source_language"`
	TargetLanguage   string        `json:"target_language"`
	WordCount        int32         `json:"word_count"`
	SegmentCount     int32         `json:"segment_count"`
	Status           string        `json:"status"`
	ProcessingStatus string        `json:"processing_status"`
	ErrorMessage     *string       `json:"error_message"`
	MTSkipReason     *string       `json:"mt_skip_reason"`
	Counts           SegmentCounts `json:"counts"`
	WorkflowStep     *WorkflowStep `json:"workflow_step"`
	CreatedAt        string        `json:"created_at"`
	UpdatedAt        string        `json:"updated_at"`
}

// SegmentCounts is a job's segments by state.
type SegmentCounts struct {
	Total         int32 `json:"total"`
	Untranslated  int32 `json:"untranslated"`
	PreTranslated int32 `json:"pre_translated"`
	AutoApproved  int32 `json:"auto_approved"`
	NeedsReview   int32 `json:"needs_review"`
	Approved      int32 `json:"approved"`
}

// WorkflowStep is a job's current workflow step.
type WorkflowStep struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Order      int32   `json:"order"`
	Type       string  `json:"type"`
	Status     string  `json:"status"`
	IsLastStep bool    `json:"is_last_step"`
	AssigneeID *int64  `json:"assignee_id"`
	Deadline   *string `json:"deadline"`
}

func jobDTO(v job.JobView) Job {
	out := Job{
		ID:               v.ID,
		ProjectID:        v.ProjectID,
		FileID:           v.FileID,
		FileName:         v.FileName,
		SourceLanguage:   v.SourceLanguage,
		TargetLanguage:   v.TargetLanguage,
		WordCount:        v.WordCount,
		SegmentCount:     v.SegmentCount,
		Status:           v.Status,
		ProcessingStatus: v.PipelineStatus,
		ErrorMessage:     v.ErrorMessage,
		MTSkipReason:     v.MTSkipReason,
		Counts: SegmentCounts{
			Total:         v.Counts.Total,
			Untranslated:  v.Counts.Untranslated,
			PreTranslated: v.Counts.PreTranslated,
			AutoApproved:  v.Counts.AutoApproved,
			NeedsReview:   v.Counts.NeedsReview,
			Approved:      v.Counts.Approved,
		},
		CreatedAt: requiredRFC3339(v.CreatedAt),
		UpdatedAt: requiredRFC3339(v.UpdatedAt),
	}
	if s := v.WorkflowStep; s != nil {
		out.WorkflowStep = &WorkflowStep{
			ID:         s.ID,
			Name:       s.StepName,
			Order:      s.StepOrder,
			Type:       s.StepType,
			Status:     s.Status,
			IsLastStep: s.IsLastStep,
			AssigneeID: s.AssignedTo,
			Deadline:   optionalRFC3339(s.Deadline),
		}
	}
	return out
}
