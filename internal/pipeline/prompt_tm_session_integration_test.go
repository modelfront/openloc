package pipeline_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/segmentprompt"
	"openloc/internal/testutil"
)

// A QE+APE prompt that carries <tm_match> looks up a batch's TM matches in
// one transaction: the trigram threshold is set once per batch, not once per
// segment, and every segment's prompt still carries its scored match.
func TestIntegrationRunAIStep_TMMatchLookupsShareATransaction(t *testing.T) {
	inner := usageStub(t, func(string) string { return `"approved":true,"score":95` })
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		resp, err := http.Post(inner.URL+r.URL.Path, "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	e := newAIUsageEnv(t, srv.URL)
	ctx := context.Background()

	for i := 1; i <= 4; i++ {
		seg := testutil.SeedSegment(t, e.q, e.file.ID, "Hello world.", int32(i))
		testutil.SeedTranslation(t, e.q, seg.ID, "fr", db.TranslationStatusNeedsReview)
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE translations t SET active_text = 'MT text', origin = 'mt'
		FROM segments s WHERE s.id = t.segment_id AND s.file_id = $1`, e.file.ID); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	tm := testutil.SeedTMWithLangs(t, e.q, e.tenant.ID, "Prompt TM", "en", []string{"fr"})
	testutil.SeedTMAssignment(t, e.q, e.project.SettingsProfileID, tm.ID, "read", 0)
	testutil.SeedTMEntry(t, e.q, tm.ID, "en", "fr", "Hello world", "Bonjour le monde")

	prompt, err := e.q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
		TenantID: e.tenant.ID, Name: "TM-aware reviewer", PromptText: "Review.", OutStatus: true,
		SegmentContext: []string{segmentprompt.KeyTMMatch},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := e.q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: e.tenant.ID, Name: "TM-aware", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: "en", TargetLangs: []string{"fr"},
		SystemPromptID: &prompt.ID, TokenID: &e.tokenID, Model: "gpt-4o-mini", Settings: []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}
	job := testutil.SeedJob(t, e.q, e.file.ID, "fr")
	routerID := testutil.SeedRouterForEngine(t, e.q, e.tenant.ID, engine.ID)
	testutil.SeedAIJobStepWithRouter(t, e.q, e.project.SettingsProfileID, job.ID, routerID)

	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	if _, err := svc.ConfigureAI(testutil.TestAISecretsKey, nil); err != nil {
		t.Fatalf("ConfigureAI: %v", err)
	}
	log.Reset()
	res, err := svc.RunAIStep(ctx, e.tenant.ID, job.ID, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.Total != 5 {
		t.Fatalf("reviewed %d segments, want 5", res.Total)
	}
	if n := log.Count("FuzzyLookupTM"); n < 5 {
		t.Fatalf("%d TM lookups for 5 segments; the prompt did not look up its matches", n)
	}
	if n := log.Count("SetTrgmSimilarityThreshold"); n != 1 {
		t.Errorf("threshold set %d times for one batch of 5 segments, want once", n)
	}
	sent := strings.Join(bodies, "\n")
	if n := strings.Count(sent, "Bonjour le monde"); n != 5 {
		t.Errorf("the TM match reached %d of 5 segment prompts", n)
	}
	if n := strings.Count(sent, `Bonjour le monde\",\"score\":`); n != 5 {
		t.Errorf("%d of 5 TM matches carry a score: %s", n, sent)
	}
}
