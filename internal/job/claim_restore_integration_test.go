package job_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// claimRaceFixture is a job the handler loaded while the upload worker's run
// was mid-flight, and whose run then finished before the handler's claim.
type claimRaceFixture struct {
	detachFixture
	ctx    context.Context
	loaded db.Job
}

func newClaimRaceFixture(t *testing.T, fake *fakePipeline) claimRaceFixture {
	t.Helper()
	fx := newDetachFixture(t, fake)
	ctx := auth.WithClaims(context.Background(), adminClaims(fx.tenant, fx.userID))
	if _, err := fx.pool.Exec(ctx, `UPDATE jobs SET status = 'in_progress', current_step = 'Translating' WHERE id = $1`, fx.jobID); err != nil {
		t.Fatal(err)
	}
	loaded, err := fx.q.GetJobTenant(ctx, db.GetJobTenantParams{ID: fx.jobID, TenantID: fx.tenant})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.pool.Exec(ctx, `UPDATE jobs SET current_step = 'Pre-translation complete' WHERE id = $1`, fx.jobID); err != nil {
		t.Fatal(err)
	}
	return claimRaceFixture{detachFixture: fx, ctx: ctx, loaded: loaded}
}

// expectRejected fails unless err is the pre-flight rejection that follows the
// claim, and the job reads as the finished run left it, not as it was loaded.
func (fx claimRaceFixture) expectRejected(t *testing.T, err error, message string) {
	t.Helper()
	var ce *httputil.ClientError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, message) {
		t.Fatalf("got %v, want the pre-flight rejection %q", err, message)
	}
	post, err := fx.q.GetJob(fx.ctx, fx.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if post.Status != "in_progress" || post.CurrentStep != "Pre-translation complete" {
		t.Fatalf("job left as %s / %q, want the finished run's in_progress / \"Pre-translation complete\"", post.Status, post.CurrentStep)
	}
}

// The handler loads the job before it claims it. When the upload worker's
// run finishes in between, the claim succeeds; a phase with nothing
// configured then puts the job back as the claim found it, not as the earlier
// load saw it, so the job is not left looking mid-run.
func TestIntegrationApplyPhases_ARejectionRestoresWhatTheClaimReplaced(t *testing.T) {
	fx := newClaimRaceFixture(t, &fakePipeline{outcome: pipeline.TranslateOutcome{
		TMSkipReason: pipeline.PhaseSkipNoAssignment, MTSkipReason: pipeline.PhaseSkipNoAssignment,
	}})
	_, err := fx.h.ApplyPhases(fx.ctx, fx.tenant, fx.loaded, "en", pipeline.TranslatePhases{UseTM: true}, "tm-apply")
	fx.expectRejected(t, err, "No TM configured")
}

// The same for a Re-run AI step on a step with no AI router.
func TestIntegrationRunAIStep_ARejectionRestoresWhatTheClaimReplaced(t *testing.T) {
	fx := newClaimRaceFixture(t, &fakePipeline{aiResult: pipeline.AIStepResult{SkipReason: pipeline.PhaseSkipNoAIStep}})
	_, err := fx.h.RunAIStep(fx.ctx, fx.tenant, fx.loaded)
	fx.expectRejected(t, err, "no AI router")
}

// A run whose pod died leaves the job in_progress; after 30 minutes untouched
// the claim may take it over. A rejection then puts the job back with its old
// updated_at too, so it stays reclaimable instead of starting the 30 minutes
// over.
func TestIntegrationApplyPhases_ARejectedTakeoverLeavesTheJobReclaimable(t *testing.T) {
	fx := newDetachFixture(t, &fakePipeline{outcome: pipeline.TranslateOutcome{
		TMSkipReason: pipeline.PhaseSkipNoAssignment, MTSkipReason: pipeline.PhaseSkipNoAssignment,
	}})
	ctx := auth.WithClaims(context.Background(), adminClaims(fx.tenant, fx.userID))
	if _, err := fx.pool.Exec(ctx, `UPDATE jobs SET status = 'in_progress', current_step = 'Translating', updated_at = now() - interval '31 minutes' WHERE id = $1`, fx.jobID); err != nil {
		t.Fatal(err)
	}
	loaded, err := fx.q.GetJobTenant(ctx, db.GetJobTenantParams{ID: fx.jobID, TenantID: fx.tenant})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fx.h.ApplyPhases(ctx, fx.tenant, loaded, "en", pipeline.TranslatePhases{UseTM: true}, "tm-apply")
	var ce *httputil.ClientError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "No TM configured") {
		t.Fatalf("got %v, want the pre-flight rejection after the takeover", err)
	}
	post, err := fx.q.GetJob(ctx, fx.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if !post.UpdatedAt.Time.Equal(loaded.UpdatedAt.Time) {
		t.Fatalf("updated_at moved from %s to %s: the dead run's job is no longer reclaimable", loaded.UpdatedAt.Time, post.UpdatedAt.Time)
	}
	if _, err := fx.q.ClaimJobPipeline(ctx, db.ClaimJobPipelineParams{ID: fx.jobID, TenantID: fx.tenant, CurrentStep: "Running MT"}); err != nil {
		t.Fatalf("the next claim on the dead run's job: %v, want it taken over", err)
	}
}

// The claim is tenant-scoped: another tenant's id claims nothing and leaves
// the job as it was.
func TestIntegrationClaimJobPipeline_IsTenantScoped(t *testing.T) {
	fx := newDetachFixture(t, &fakePipeline{})
	ctx := context.Background()
	other := testutil.SeedTenant(t, fx.q, "OtherClaimCo")
	_, err := fx.q.ClaimJobPipeline(ctx, db.ClaimJobPipelineParams{ID: fx.jobID, TenantID: other.ID, CurrentStep: "Running MT"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a claim with another tenant's id: %v, want no row", err)
	}
	post, err := fx.q.GetJob(ctx, fx.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if post.CurrentStep == "Running MT" {
		t.Fatal("another tenant's claim took the job")
	}
}
