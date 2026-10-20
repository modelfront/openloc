package main

// Step-definition + workflow library demo data. Idempotent
// like every heal*: each item is looked up by name before it is created.

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/settings"
)

// healWorkflowLibrary seeds the step definition + workflow library
// (demodata.EnsureWorkflowLibrary: the two default steps, and with the demo
// QE+APE engine a hybrid and an ai step on an AI router plus the workflows
// built from them), links every pre-library profile step to its definition by
// name, and stamps the template + demo project as "based on Standard".
func healWorkflowLibrary(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	// The demo QE+APE engine backs the AI steps; without it only the human
	// defaults are seeded.
	var qeape *int64
	systems, err := q.ListAIEngines(ctx, tenantID)
	if err != nil {
		log.Printf("⚠ Could not list AI engines: %v", err)
		return
	}
	for i := range systems {
		if systems[i].Purpose == "qe_ape" {
			qeape = &systems[i].ID
			break
		}
	}
	lib, err := demodata.EnsureWorkflowLibrary(ctx, q, tenantID, qeape)
	if err != nil {
		log.Printf("⚠ Could not ensure the workflow library: %v", err)
		return
	}
	log.Printf("  ✓ Workflow library (AI steps: %v)", lib.HasAI)

	// Link pre-library profile steps to their definition by name, then stamp
	// the template + demo project as based on Standard when their steps are
	// exactly its two.
	if tag, err := pool.Exec(ctx, `
		UPDATE workflow_steps ws
		SET step_definition_id = sd.id
		FROM settings_profiles sp
		JOIN step_definitions sd ON sd.tenant_id = sp.tenant_id AND sd.deleted_at IS NULL
		WHERE ws.settings_profile_id = sp.id AND sp.tenant_id = $1
		  AND ws.step_definition_id IS NULL AND lower(ws.name) = lower(sd.name)`, tenantID); err != nil {
		log.Printf("⚠ Could not link profile steps to the library: %v", err)
	} else if n := tag.RowsAffected(); n > 0 {
		log.Printf("  ✓ Linked %d profile step(s) to library definitions", n)
	}
	if tag, err := pool.Exec(ctx, `
		UPDATE settings_profiles sp SET workflow_id = $2
		WHERE sp.tenant_id = $1 AND sp.workflow_id IS NULL
		  AND (SELECT string_agg(lower(ws.name), '>' ORDER BY ws.step_order) FROM workflow_steps ws WHERE ws.settings_profile_id = sp.id) = 'translation>review'`,
		tenantID, lib.Standard.ID); err != nil {
		log.Printf("⚠ Could not stamp profiles with the Standard workflow: %v", err)
	} else if n := tag.RowsAffected(); n > 0 {
		log.Printf("  ✓ %d profile(s) now based on the %q workflow", n, demodata.WorkflowStandard)
	}
}

// createDefaultSteps gives a profile the default Translation → Review pair
// linked to the library; used by the project / template heals.
func createDefaultSteps(ctx context.Context, q *db.Queries, tenantID, profileID int64) {
	if err := settings.CreateDefaultWorkflowSteps(ctx, q, tenantID, profileID); err != nil {
		log.Printf("⚠ Could not create default workflow steps: %v", err)
		return
	}
	log.Printf("  ✓ Workflow steps: Translation → Review")
}
