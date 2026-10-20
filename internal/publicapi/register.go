package publicapi

import (
	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
)

// Register mounts the v1 routes on r, which the caller has already placed
// behind authentication at /api/v1.
func Register(r chi.Router, h Handlers) {
	r.Get("/openapi.json", h.Spec.ServeHTTP)

	r.Route("/projects", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listProjects)
		r.Post("/", h.createProject)
		r.Route("/{projectID}", func(r chi.Router) {
			r.Get("/", h.getProject)
			r.Patch("/", h.updateProject)
			// Owner/admin, or a manager on a draft — the handler checks.
			r.Delete("/", h.Projects.HandleDelete)
			r.Get("/jobs", h.listProjectJobs)
			r.Get("/labels", h.listProjectLabels)
			r.Put("/labels", h.setProjectLabels)
			r.Get("/files", h.listFiles)
			r.Post("/files", h.uploadFile)
			r.Route("/files/{fileID}", func(r chi.Router) {
				r.Use(h.requireFileInProject)
				r.Get("/", h.getFile)
				// Owner/admin, or a manager on a draft — the handler checks.
				r.Delete("/", h.Files.HandleDelete)
			})
		})
	})

	// Not under /projects: a linguist reaches the source of a file they have
	// a job in, and never the project routes.
	r.Route("/files/{fileID}", func(r chi.Router) {
		r.Use(auth.RequireFileOwnership(h.Queries))
		r.Use(auth.RequireLinguistFileAccess(h.Queries))
		r.Get("/source", h.Files.HandleDownloadSource)
	})

	r.Route("/tms", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listTMs)
		r.Post("/", h.createTM)
		r.Route("/{tmID}", func(r chi.Router) {
			r.Use(auth.RequireTMOwnership(h.Queries))
			r.Get("/", h.getTM)
			r.Patch("/", h.renameTM)
			r.Get("/entries", h.listTMEntries)
			r.Post("/imports", h.importTMX)
			r.Get("/imports", h.listTMImports)
			r.Get("/export", h.TMs.HandleExportTMX)
			r.With(auth.RequireRole("owner", "admin")).Delete("/", h.TMs.HandleDeleteCollection)
		})
	})

	r.Route("/glossaries", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listGlossaries)
		r.Post("/", h.createGlossary)
		r.Route("/{glossaryID}", func(r chi.Router) {
			r.Get("/", h.getGlossary)
			r.Patch("/", h.updateGlossary)
			r.With(auth.RequireRole("owner", "admin")).Delete("/", h.deleteGlossary)
			r.Get("/concepts", h.listConcepts)
			r.Post("/concepts", h.createConcept)
			r.Delete("/concepts/{conceptID}", h.deleteConcept)
			r.Post("/imports", h.importGlossary)
			r.Get("/export", h.exportGlossary)
		})
	})

	r.Route("/templates", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listTemplates)
		r.Get("/{templateID}", h.getTemplate)
		r.Post("/{templateID}/duplicate", h.duplicateTemplate)
		r.Delete("/{templateID}", h.Templates.HandleDelete)
	})

	r.Route("/workflows", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listWorkflows)
		r.Post("/", h.createWorkflow)
		r.Get("/{workflowID}", h.getWorkflow)
		r.Put("/{workflowID}", h.replaceWorkflow)
		r.Delete("/{workflowID}", h.deleteWorkflow)
	})

	r.Route("/workflow-steps", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listStepDefinitions)
		r.Post("/", h.createStepDefinition)
		r.Get("/{stepDefinitionID}", h.getStepDefinition)
		r.Put("/{stepDefinitionID}", h.replaceStepDefinition)
		r.Delete("/{stepDefinitionID}", h.deleteStepDefinition)
	})

	r.Route("/labels", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listLabels)
		r.Post("/", h.createLabel)
		r.Post("/{labelID}/values", h.addLabelValue)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Delete("/{labelID}", h.deleteLabel)
			r.Delete("/{labelID}/values/{valueID}", h.deleteLabelValue)
		})
	})

	r.Route("/members", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listMembers)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Patch("/{userID}", h.changeMemberRole)
			r.Delete("/{userID}", h.removeMember)
		})
	})

	r.Route("/invites", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/", h.listInvites)
		r.Post("/", h.createInvite)
		r.Delete("/{inviteID}", h.revokeInvite)
	})

	r.Route("/ai", func(r chi.Router) {
		r.Use(auth.RequireManager())
		r.Get("/prompts", h.listAIPrompts)
		r.Get("/engines", h.listAIEngines)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Post("/prompts", h.createAIPrompt)
			r.Put("/prompts/{promptID}", h.replaceAIPrompt)
			r.Delete("/prompts/{promptID}", h.AIPrompts.HandleDelete)
			r.Post("/engines", h.createAIEngine)
			r.Put("/engines/{engineID}", h.replaceAIEngine)
			r.Delete("/engines/{engineID}", h.deleteAIEngine)
		})
	})

	r.Route("/jobs/{jobID}", func(r chi.Router) {
		r.Use(auth.RequireJobOwnership(h.Queries))
		r.Use(auth.RequireLinguistJobAccess(h.Queries))
		r.Get("/", h.getJob)
		r.Get("/export", h.Jobs.HandleExport)
		r.Get("/segments", h.listSegments)
		r.Route("/segments/{translationID}", func(r chi.Router) {
			r.Use(h.requireSegmentInJob)
			r.Put("/draft", h.saveSegmentDraft)
			r.Post("/confirm", h.confirmSegment)
			r.Get("/comments", h.listComments)
			r.Post("/comments", h.addComment)
			r.Delete("/comments/{commentID}", h.deleteComment)
		})
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Delete("/", h.Jobs.HandleDeleteJob)
			r.Post("/assign", h.assignJob)
		})
	})
}
