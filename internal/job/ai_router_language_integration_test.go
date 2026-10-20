package job_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The step's AI router picks the engine by the job's TARGET
// language. A router that covers some other language and names no default
// engine leaves the job unreviewed, so "Re-run AI step" must reject with a 400
// naming the fix rather than reporting a misleading success — the same
// contract as the no-extension-runtime case.
//
// The rule is on the project's SOURCE language, so the test also pins the
// wiring: resolving on the source language would find an engine here and the
// request would come back 200.
//
// Anti-tautology RED proof: drop the PhaseSkipNoEngineForLanguage case from
// aiStepSkipMessage (internal/job/handler_pipeline.go) — the skip stops
// mapping to a message, the handler answers 200, and the 400 assertion flips.
func TestIntegrationAIStep_NoEngineForTargetLanguage_Returns400(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIStepNoLangCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@aisteplang.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "No Lang Route Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// The engine has a key (nothing may call it), so the rejection can only be
	// about the missing language route.
	extToken := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")
	// A router with no default engine, whose only rule routes "en" — the
	// source language, never the target this job needs.
	engineID := seedQEAPEEngine(t, q, tenant.ID, job.ID, &extToken)
	aiRouter := testutil.SeedAIRouter(t, q, tenant.ID, "en-only router", nil)
	testutil.SeedAIRouterRule(t, q, aiRouter.ID, "en", engineID)
	seedActiveStepWithRouter(t, q, project.SettingsProfileID, job.ID, &owner.ID, aiRouter.ID)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when the router has no engine for the job's target language; got %d (body: %s)",
			rec.Code, rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	if !strings.Contains(body, "target language") {
		t.Errorf("message must name the missing target-language route so the user knows what to fix; got: %s", rec.Body.String())
	}
	if !strings.Contains(body, "rule") || !strings.Contains(body, "default engine") {
		t.Errorf("message must name both fixes (add a rule / set a default engine); got: %s", rec.Body.String())
	}

	// The claim moved the job to in_progress; a rejected re-run must put it
	// back rather than hold the QE+APE editor lock.
	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if string(post.Status) == "completed" || string(post.Status) == "in_progress" {
		t.Errorf("job status must not move past the rejection; got %q", post.Status)
	}
}
