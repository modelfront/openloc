//go:generate go run gen_mock_querier.go

package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"openloc/internal/aiprompt"
	db "openloc/internal/database/sqlcgen"
)

// mockQuerier implements db.Querier for pipeline unit tests.
// Only methods used by pipeline are implemented here; the rest are
// auto-generated panic stubs in mock_querier_stubs_test.go.
type mockQuerier struct {
	files    map[int64]db.File
	projects map[int64]db.Project
	segments map[int64]db.Segment

	// untranslated rows keyed by fileID
	untranslated map[int64][]db.ListUntranslatedByFileRow
	// pre-translated rows keyed by fileID
	preTranslated map[int64][]db.ListPreTranslatedByFileRow
	// approved for TM write-back keyed by "fileID:lang"
	approvedForTM map[string][]db.ListApprovedForTMRow
	// approved for export keyed by "fileID:lang"
	approvedForExport map[string][]db.ListApprovedForExportRow

	// Settings TM assignments: settingsProfileID:src:tgt → []LookupSettingsTMsRow
	settingsTMs map[string][]db.LookupSettingsTMsRow
	// Settings write TMs: settingsProfileID:src:tgt → []int64
	settingsWriteTMs map[string][]int64
	// MT routers: engines by id, routers by id, rules by router id. A profile
	// reaches an engine only through its MtRouterID (see addMTRouter).
	mtEngines     map[int64]db.MtEngine
	mtRouters     map[int64]db.MtRouter
	mtRouterRules map[int64][]db.MtRouterRule
	// AI-as-MT: AI engines keyed by id; a row carries its own token +
	// model.
	aiEngines     map[int64]db.AiEngine
	aiRouters     map[int64]db.AiRouter       // keyed by router id
	aiRouterRules map[int64][]db.AiRouterRule // keyed by router id
	aiEngineRows  map[int64][]db.AiEngineRow  // keyed by engineID
	aiPrompts     map[int64]db.AiPrompt       // keyed by prompt id
	// Per-language prompt additions, keyed by prompt id. Empty for most tests:
	// a prompt without additions must run exactly as it did before they existed.
	aiPromptLangAdditions map[int64][]db.AiPromptLanguageAddition
	vendorTokens          map[int64]db.GetVendorTokenRow       // tokens by id (for GetVendorToken)
	firstTokens           map[string]db.GetFirstVendorTokenRow // by "tenant:provider" (GetFirstVendorToken)
	// Settings profiles: settingsProfileID → SettingsProfile
	settingsProfiles map[int64]db.SettingsProfile

	// TM entries: tmID:src:tgt:text → TranslationMemory
	tmEntries map[string]db.TranslationMemory
	// Fuzzy TM entries: tmID:src:tgt:queryText → []FuzzyLookupTMRow
	fuzzyTMEntries map[string][]db.FuzzyLookupTMRow

	// QE+APE workflow steps: job/active-step/workflow-step/customer
	// fixtures that RunAIStep resolves. Keyed by their id (jobID for jobs +
	// activeSteps, workflowStepID for workflowSteps, customerID for customers).
	jobs          map[int64]db.Job
	activeSteps   map[int64]db.GetActiveStepForJobRow
	workflowSteps map[int64]db.WorkflowStep
	customers     map[int64]db.Customer

	// Built-in label picks, keyed "projectID:systemKey" — where Customer and
	// Domain live.
	projectSystemLabels map[string]string

	// History events the handlers emit, in order.
	projectEvents []db.InsertProjectEventParams

	// The per-segment context a generated body resolves from.
	segComments map[int64][]string // translationID → comments, oldest first
	// Glossary terms: settingsProfileID:src:tgt → every term of that pair. The
	// mock returns them all and lets the caller do the token matching, which is
	// what the real query's `lower(text) = ANY(tokens)` does.
	settingsTerms map[string][]db.LookupSettingsTermsByTokensRow

	// Track calls for assertions
	updateMTCalls      []db.UpdateTranslationMTParams
	updateHumanCalls   []db.UpdateTranslationHumanParams
	updatePredictCalls []db.UpdateTranslationPredictParams
	updateTMFuzzyCalls []db.UpdateTranslationTMFuzzyParams
	insertTMCalls      []db.InsertTMParams
	// upsertTMCalls records calls to UpsertTMPipelineEntry (the pipeline
	// write-back path). Tests that exercise TM write-back / auto-save assert on
	// this slice.
	upsertTMCalls []db.UpsertTMPipelineEntryParams
	// AI-authored segment comments from the QE+APE step.
	createAICommentCalls []db.CreateAICommentParams
	// Translation ids whose stale QA findings a phase dropped after rewriting
	// their target, flattened across calls.
	droppedQAResultIDs []int64
	lookupTMCalls      int // per-segment exact lookups (LookupTM)
	lookupTMBatchCalls int // batch exact lookups (LookupTMBatch)

	// History emission — recorded by the mock impls
	// below so unit tests can assert on per-event provenance without
	// reaching for a real DB. EmitAutomationHistory writes both, in
	// order: UpdateTranslationActiveSource then InsertTranslationHistory.
	updateActiveSourceCalls []db.UpdateTranslationActiveSourceParams
	insertHistoryCalls      []db.InsertTranslationHistoryParams

	// Lazy-resolution assertions: a prompt that doesn't use a
	// token must not trigger its lookup, so tests count the calls.
	commentLookups int
	termLookups    int
}

func newMockQuerier() *mockQuerier {
	return &mockQuerier{
		files:                 make(map[int64]db.File),
		projects:              make(map[int64]db.Project),
		segments:              make(map[int64]db.Segment),
		untranslated:          make(map[int64][]db.ListUntranslatedByFileRow),
		preTranslated:         make(map[int64][]db.ListPreTranslatedByFileRow),
		approvedForTM:         make(map[string][]db.ListApprovedForTMRow),
		approvedForExport:     make(map[string][]db.ListApprovedForExportRow),
		settingsTMs:           make(map[string][]db.LookupSettingsTMsRow),
		settingsWriteTMs:      make(map[string][]int64),
		mtEngines:             make(map[int64]db.MtEngine),
		mtRouters:             make(map[int64]db.MtRouter),
		mtRouterRules:         make(map[int64][]db.MtRouterRule),
		aiEngines:             make(map[int64]db.AiEngine),
		aiRouters:             make(map[int64]db.AiRouter),
		aiRouterRules:         make(map[int64][]db.AiRouterRule),
		aiEngineRows:          make(map[int64][]db.AiEngineRow),
		aiPrompts:             make(map[int64]db.AiPrompt),
		aiPromptLangAdditions: make(map[int64][]db.AiPromptLanguageAddition),
		vendorTokens:          make(map[int64]db.GetVendorTokenRow),
		firstTokens:           make(map[string]db.GetFirstVendorTokenRow),
		settingsProfiles:      make(map[int64]db.SettingsProfile),
		tmEntries:             make(map[string]db.TranslationMemory),
		fuzzyTMEntries:        make(map[string][]db.FuzzyLookupTMRow),
		jobs:                  make(map[int64]db.Job),
		activeSteps:           make(map[int64]db.GetActiveStepForJobRow),
		workflowSteps:         make(map[int64]db.WorkflowStep),
		customers:             make(map[int64]db.Customer),
		settingsTerms:         make(map[string][]db.LookupSettingsTermsByTokensRow),
		segComments:           make(map[int64][]string),
		projectSystemLabels:   make(map[string]string),
	}
}

func tmKey(tmID int64, src, tgt, text string) string {
	return fmt.Sprintf("%d:%s:%s:%s", tmID, src, tgt, text)
}

func langKey(id int64, src, tgt string) string {
	return fmt.Sprintf("%d:%s:%s", id, src, tgt)
}

func fileLangKey(fileID int64, lang string) string {
	return fmt.Sprintf("%d:%s", fileID, lang)
}

// --- Implemented methods ---

func (m *mockQuerier) GetFile(ctx context.Context, id int64) (db.File, error) {
	f, ok := m.files[id]
	if !ok {
		return db.File{}, fmt.Errorf("file %d not found", id)
	}
	return f, nil
}

func (m *mockQuerier) GetFileTenant(ctx context.Context, arg db.GetFileTenantParams) (db.File, error) {
	f, ok := m.files[arg.ID]
	if !ok {
		return db.File{}, fmt.Errorf("file %d not found", arg.ID)
	}
	// Verify tenant via project
	p, ok := m.projects[f.ProjectID]
	if !ok || p.TenantID != arg.TenantID {
		return db.File{}, fmt.Errorf("file %d not found for tenant %d", arg.ID, arg.TenantID)
	}
	return f, nil
}

func (m *mockQuerier) GetProjectByID(ctx context.Context, id int64) (db.Project, error) {
	p, ok := m.projects[id]
	if !ok {
		return db.Project{}, fmt.Errorf("project %d not found", id)
	}
	return p, nil
}

func (m *mockQuerier) GetProject(ctx context.Context, arg db.GetProjectParams) (db.Project, error) {
	p, ok := m.projects[arg.ID]
	if !ok || p.TenantID != arg.TenantID {
		return db.Project{}, fmt.Errorf("project %d not found for tenant %d", arg.ID, arg.TenantID)
	}
	return p, nil
}

func (m *mockQuerier) ListUntranslatedByFile(ctx context.Context, arg db.ListUntranslatedByFileParams) ([]db.ListUntranslatedByFileRow, error) {
	fileID := arg.FileID
	// Mirror real-DB semantics: rows promoted out of "untranslated" by
	// either an exact TM match (UpdateTranslationHuman → status=approved)
	// or a fuzzy TM match (UpdateTranslationTMFuzzy → status=needs_review)
	// must be excluded from subsequent ListUntranslated calls. MT phase
	// re-queries this AFTER the TM phase, so without this filter unit tests
	// would see double translation in the override path.
	promoted := make(map[int64]bool, len(m.updateHumanCalls)+len(m.updateTMFuzzyCalls))
	for _, c := range m.updateHumanCalls {
		promoted[c.ID] = true
	}
	for _, c := range m.updateTMFuzzyCalls {
		promoted[c.ID] = true
	}
	src := m.untranslated[fileID]
	out := make([]db.ListUntranslatedByFileRow, 0, len(src))
	for _, r := range src {
		if !promoted[r.ID] && r.TargetLanguage == arg.TargetLanguage {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListTMTargetableByFile mirrors ListUntranslatedByFile in unit-test fixtures:
// most existing mocks seed only untranslated rows, so the same set is also the
// TM-targetable set. Tests that need to exercise the TM-applied-over-MT
// override path (TM applied on top of MT-filled segments) seed their own data via the
// real DB through testutil — they don't use this mock.
func (m *mockQuerier) ListTMTargetableByFile(ctx context.Context, arg db.ListTMTargetableByFileParams) ([]db.ListTMTargetableByFileRow, error) {
	fileID := arg.FileID
	src := m.untranslated[fileID]
	out := make([]db.ListTMTargetableByFileRow, 0, len(src))
	for _, r := range src {
		if r.TargetLanguage != arg.TargetLanguage {
			continue
		}
		out = append(out, db.ListTMTargetableByFileRow{
			ID:             r.ID,
			SegmentID:      r.SegmentID,
			TargetLanguage: r.TargetLanguage,
			SourceText:     r.SourceText,
			SourceRichText: r.SourceRichText,
		})
	}
	return out, nil
}

func (m *mockQuerier) ListPreTranslatedByFile(ctx context.Context, arg db.ListPreTranslatedByFileParams) ([]db.ListPreTranslatedByFileRow, error) {
	var out []db.ListPreTranslatedByFileRow
	for _, r := range m.preTranslated[arg.FileID] {
		if r.TargetLanguage == arg.TargetLanguage && r.ActiveText != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockQuerier) ListApprovedForTM(ctx context.Context, arg db.ListApprovedForTMParams) ([]db.ListApprovedForTMRow, error) {
	return m.approvedForTM[fileLangKey(arg.FileID, arg.TargetLanguage)], nil
}

func (m *mockQuerier) ListApprovedForExport(ctx context.Context, arg db.ListApprovedForExportParams) ([]db.ListApprovedForExportRow, error) {
	return m.approvedForExport[fileLangKey(arg.FileID, arg.TargetLanguage)], nil
}

// ListAllForBilingualExport — mock returns empty slice by default. The
// bilingual XLIFF export reads this. Handler-level tests for
// ?format=xliff dispatch exercise the success path against an empty
// segment list (the body content correctness is covered by the
// integration tests in bilingual_export_integration_test.go which use
// a real Postgres testcontainer).
func (m *mockQuerier) ListAllForBilingualExport(ctx context.Context, arg db.ListAllForBilingualExportParams) ([]db.ListAllForBilingualExportRow, error) {
	return nil, nil
}

func baseLang(code string) string {
	if i := strings.IndexByte(code, '-'); i > 0 {
		return code[:i]
	}
	return code
}

func (m *mockQuerier) GetSettingsProfile(ctx context.Context, id int64) (db.SettingsProfile, error) {
	sp, ok := m.settingsProfiles[id]
	if !ok {
		return db.SettingsProfile{}, fmt.Errorf("settings profile %d not found", id)
	}
	return sp, nil
}

// The mock has no tm_profiles, so the TM scope of a row is always the row
// itself (what tm_scope_profile_id returns for an unlinked row).
func (m *mockQuerier) GetTMScopeSettingsProfile(ctx context.Context, id int64) (db.SettingsProfile, error) {
	return m.GetSettingsProfile(ctx, id)
}

func (m *mockQuerier) LookupSettingsTMs(ctx context.Context, arg db.LookupSettingsTMsParams) ([]db.LookupSettingsTMsRow, error) {
	// Exact match first
	key := langKey(arg.SettingsProfileID, arg.SourceLanguage, arg.TargetLanguage)
	if rows, ok := m.settingsTMs[key]; ok {
		return rows, nil
	}
	// Base-language fallback
	srcBase := baseLang(arg.SourceLanguage)
	tgtBase := baseLang(arg.TargetLanguage)
	for k, rows := range m.settingsTMs {
		parts := strings.SplitN(k, ":", 3)
		if len(parts) == 3 && parts[0] == fmt.Sprintf("%d", arg.SettingsProfileID) &&
			baseLang(parts[1]) == srcBase && baseLang(parts[2]) == tgtBase {
			return rows, nil
		}
	}
	return nil, nil
}

func (m *mockQuerier) LookupSettingsWriteTMs(ctx context.Context, arg db.LookupSettingsWriteTMsParams) ([]int64, error) {
	// Exact match first
	key := langKey(arg.SettingsProfileID, arg.SourceLanguage, arg.TargetLanguage)
	if ids := m.settingsWriteTMs[key]; len(ids) > 0 {
		return ids, nil
	}
	// Base-language fallback
	srcBase := baseLang(arg.SourceLanguage)
	tgtBase := baseLang(arg.TargetLanguage)
	for k, ids := range m.settingsWriteTMs {
		parts := strings.SplitN(k, ":", 3)
		if len(parts) == 3 && parts[0] == fmt.Sprintf("%d", arg.SettingsProfileID) &&
			baseLang(parts[1]) == srcBase && baseLang(parts[2]) == tgtBase {
			return ids, nil
		}
	}
	return nil, nil
}

func (m *mockQuerier) GetMTRouterAny(_ context.Context, arg db.GetMTRouterAnyParams) (db.MtRouter, error) {
	r, ok := m.mtRouters[arg.ID]
	if !ok || r.TenantID != arg.TenantID {
		return db.MtRouter{}, fmt.Errorf("no MT router id=%d", arg.ID)
	}
	return r, nil
}

func (m *mockQuerier) ListMTRouterRules(_ context.Context, routerID int64) ([]db.MtRouterRule, error) {
	return m.mtRouterRules[routerID], nil
}

func (m *mockQuerier) GetMTEngine(ctx context.Context, arg db.GetMTEngineParams) (db.MtEngine, error) {
	eng, ok := m.mtEngines[arg.ID]
	if !ok || eng.TenantID != arg.TenantID {
		return db.MtEngine{}, fmt.Errorf("no MT engine id=%d", arg.ID)
	}
	return eng, nil
}

// GetMTEngineCredential joins the engine to the key it spends. The mock keeps
// engines and tokens in separate maps, so the join is done here — including the
// LEFT side, since an engine with no key (a deployment one) is a valid row and
// falls back to its own columns.
func (m *mockQuerier) GetMTEngineCredential(ctx context.Context, arg db.GetMTEngineCredentialParams) (db.GetMTEngineCredentialRow, error) {
	eng, err := m.GetMTEngine(ctx, db.GetMTEngineParams{ID: arg.ID, TenantID: arg.TenantID})
	if err != nil {
		return db.GetMTEngineCredentialRow{}, err
	}
	row := db.GetMTEngineCredentialRow{
		ID: eng.ID, Name: eng.Name, EngineType: eng.EngineType, ApiKey: eng.ApiKey,
		ApiUrl: eng.ApiUrl, Enabled: eng.Enabled, CreatedAt: eng.CreatedAt, UpdatedAt: eng.UpdatedAt,
		TenantID: eng.TenantID, DeploymentManaged: eng.DeploymentManaged,
		TokenID: eng.TokenID, Settings: eng.Settings,
	}
	if eng.TokenID != nil {
		if tok, ok := m.vendorTokens[*eng.TokenID]; ok {
			row.TokenKeyEncrypted = &tok.ApiKeyEncrypted
			row.TokenBaseUrl = &tok.BaseUrl
		}
	}
	return row, nil
}

func (m *mockQuerier) GetAIEngine(ctx context.Context, arg db.GetAIEngineParams) (db.AiEngine, error) {
	e, ok := m.aiEngines[arg.ID]
	if !ok || e.TenantID != arg.TenantID {
		return db.AiEngine{}, fmt.Errorf("no AI engine id=%d", arg.ID)
	}
	return e, nil
}

// aiTokenID is a deterministic mock token id for a (tenant, provider) pair so
// addLLMAIStepJob and setAIProviderConn agree on which token a config points at.
func aiTokenID(tenantID int64, provider string) int64 {
	codes := map[string]int64{"ext": 1, "openai": 2, "claude": 3}
	return tenantID*100 + codes[provider]
}

func (m *mockQuerier) GetVendorToken(ctx context.Context, arg db.GetVendorTokenParams) (db.GetVendorTokenRow, error) {
	t, ok := m.vendorTokens[arg.ID]
	if !ok || t.TenantID != arg.TenantID {
		return db.GetVendorTokenRow{}, fmt.Errorf("no vendor token id=%d", arg.ID)
	}
	return t, nil
}

func (m *mockQuerier) GetFirstVendorToken(ctx context.Context, arg db.GetFirstVendorTokenParams) (db.GetFirstVendorTokenRow, error) {
	t, ok := m.firstTokens[fmt.Sprintf("%d:%s", arg.TenantID, arg.Provider)]
	if !ok {
		return db.GetFirstVendorTokenRow{}, fmt.Errorf("no token for provider %q", arg.Provider)
	}
	return t, nil
}

func (m *mockQuerier) LookupTM(ctx context.Context, arg db.LookupTMParams) (db.TranslationMemory, error) {
	m.lookupTMCalls++
	key := tmKey(arg.TmID, arg.SourceLanguage, arg.TargetLanguage, arg.SourceText)
	entry, ok := m.tmEntries[key]
	if !ok {
		return db.TranslationMemory{}, fmt.Errorf("no TM match")
	}
	return entry, nil
}

func (m *mockQuerier) LookupTMBatch(ctx context.Context, arg db.LookupTMBatchParams) ([]db.LookupTMBatchRow, error) {
	m.lookupTMBatchCalls++
	var results []db.LookupTMBatchRow
	seen := make(map[string]bool)
	for _, srcText := range arg.SourceTexts {
		if seen[srcText] {
			continue
		}
		key := tmKey(arg.TmID, arg.SourceLanguage, arg.TargetLanguage, srcText)
		entry, ok := m.tmEntries[key]
		if ok {
			// The languages live on the row in the real schema, and the pipeline
			// compares them against the requested pair to decide whether a hit is
			// a true exact match (auto-confirm) or a penalized sublanguage
			// variant. A fixture that leaves them blank would silently exercise
			// the variant path — fail loudly instead.
			if entry.SourceLanguage == "" || entry.TargetLanguage == "" {
				panic(fmt.Sprintf("mock tmEntries[%q] is missing SourceLanguage/TargetLanguage — "+
					"set them on the fixture, they're what tells an exact match from a sublanguage variant", key))
			}
			results = append(results, db.LookupTMBatchRow{
				ID:             entry.ID,
				TmID:           entry.TmID,
				SourceLanguage: entry.SourceLanguage,
				TargetLanguage: entry.TargetLanguage,
				SourceText:     entry.SourceText,
				TargetText:     entry.TargetText,
				CreatedAt:      entry.CreatedAt,
			})
			seen[srcText] = true
		}
	}
	return results, nil
}

// SetTrgmSimilarityThreshold is a no-op: the mock matches fuzzy entries by key.
func (m *mockQuerier) SetTrgmSimilarityThreshold(context.Context, float32) error {
	return nil
}

func (m *mockQuerier) FuzzyLookupTM(ctx context.Context, arg db.FuzzyLookupTMParams) ([]db.FuzzyLookupTMRow, error) {
	// Keyed by tmID:srcLang:tgtLang:queryText for precise control in tests
	key := fmt.Sprintf("%d:%s:%s:%s", arg.TmID, arg.SourceLanguage, arg.TargetLanguage, arg.Query)
	entries := m.fuzzyTMEntries[key]
	var results []db.FuzzyLookupTMRow
	for _, e := range entries {
		if e.Score >= arg.Threshold {
			results = append(results, e)
		}
	}
	return results, nil
}

func (m *mockQuerier) UpdateTranslationMT(ctx context.Context, arg db.UpdateTranslationMTParams) (db.Translation, error) {
	m.updateMTCalls = append(m.updateMTCalls, arg)
	return db.Translation{ID: arg.ID}, nil
}

func (m *mockQuerier) DeleteQAResultsForTranslations(ctx context.Context, translationIDs []int64) error {
	m.droppedQAResultIDs = append(m.droppedQAResultIDs, translationIDs...)
	return nil
}

func (m *mockQuerier) UpdateTranslationHuman(ctx context.Context, arg db.UpdateTranslationHumanParams) (db.Translation, error) {
	m.updateHumanCalls = append(m.updateHumanCalls, arg)
	return db.Translation{ID: arg.ID}, nil
}

func (m *mockQuerier) UpdateTranslationPredict(ctx context.Context, arg db.UpdateTranslationPredictParams) (db.Translation, error) {
	m.updatePredictCalls = append(m.updatePredictCalls, arg)
	return db.Translation{ID: arg.ID}, nil
}

func (m *mockQuerier) UpdateTranslationTMFuzzy(ctx context.Context, arg db.UpdateTranslationTMFuzzyParams) (db.Translation, error) {
	m.updateTMFuzzyCalls = append(m.updateTMFuzzyCalls, arg)
	return db.Translation{ID: arg.ID}, nil
}

func (m *mockQuerier) InsertTM(ctx context.Context, arg db.InsertTMParams) (db.TranslationMemory, error) {
	m.insertTMCalls = append(m.insertTMCalls, arg)
	return db.TranslationMemory{
		TmID:           arg.TmID,
		SourceLanguage: arg.SourceLanguage,
		TargetLanguage: arg.TargetLanguage,
		SourceText:     arg.SourceText,
		TargetText:     arg.TargetText,
	}, nil
}

func (m *mockQuerier) TMExactMatchExists(ctx context.Context, arg db.TMExactMatchExistsParams) (int64, error) {
	return 0, nil // always "no match" in tests → insert new entry
}

func (m *mockQuerier) TMUpdateExactMatch(ctx context.Context, arg db.TMUpdateExactMatchParams) error {
	return nil
}

// UpsertTMPipelineEntry records the call + returns a plausible row. Used by
// the pipeline TM write-back path in tm_write.go.
// Returns prev_* as nil since the mock doesn't track prior state — callers
// that need to exercise the history-write branch use the integration test
// suite against a real DB instead.
func (m *mockQuerier) UpsertTMPipelineEntry(ctx context.Context, arg db.UpsertTMPipelineEntryParams) (db.UpsertTMPipelineEntryRow, error) {
	m.upsertTMCalls = append(m.upsertTMCalls, arg)
	return db.UpsertTMPipelineEntryRow{
		TargetText: arg.TargetText,
	}, nil
}

// UpsertTMPipelineEntries records each entry as an UpsertTMPipelineEntry
// call, so tests read one list whichever write path ran.
func (m *mockQuerier) UpsertTMPipelineEntries(ctx context.Context, arg db.UpsertTMPipelineEntriesParams) (int32, error) {
	for i := range arg.SourceTexts {
		m.upsertTMCalls = append(m.upsertTMCalls, db.UpsertTMPipelineEntryParams{
			TmID: arg.TmID, SourceLanguage: arg.SourceLanguage, TargetLanguage: arg.TargetLanguage,
			SourceText: arg.SourceTexts[i], TargetText: arg.TargetTexts[i], UpdatedBy: arg.UpdatedBy,
			SourceRichText: arg.SourceRichTexts[i], TargetRichText: arg.TargetRichTexts[i],
			PrevSourceHash: arg.PrevSourceHashes[i], NextSourceHash: arg.NextSourceHashes[i],
		})
	}
	return int32(len(arg.SourceTexts)), nil
}

// ListSegmentNeighborSourcesByIDs returns no neighbours: unit tests write
// entries without document context.
func (m *mockQuerier) ListSegmentNeighborSourcesByIDs(ctx context.Context, ids []int64) ([]db.ListSegmentNeighborSourcesByIDsRow, error) {
	return nil, nil
}

// AcquireTMSourceLocks is a no-op, like AcquireTMSourceLock.
func (m *mockQuerier) AcquireTMSourceLocks(ctx context.Context, arg db.AcquireTMSourceLocksParams) error {
	return nil
}

// AcquireTMSourceLock is a no-op in the mock: pg_advisory_xact_lock is a
// Postgres concept, and mock-querier tests run without a real DB. Concurrent
// behaviour is asserted by the integration tests in integration_test.go.
func (m *mockQuerier) AcquireTMSourceLock(ctx context.Context, arg db.AcquireTMSourceLockParams) error {
	return nil
}

// InsertTMHistory is a no-op in the mock — the production path's history
// branch is gated on PrevEntryID != nil, and our UpsertTMPipelineEntry mock
// always returns PrevEntryID=nil, so this method is unreachable from the
// TM write-back / handler / predict / translate unit tests. Override per-test if
// you ever need to exercise it.
func (m *mockQuerier) InsertTMHistory(ctx context.Context, arg db.InsertTMHistoryParams) error {
	return nil
}

// UpdateTranslationActiveSource + InsertTranslationHistory are the
// translation_history pair. The mock records the calls so unit
// tests can assert on emission shape without a real DB. EmitAutomationHistory
// fires UpdateTranslationActiveSource first, then InsertTranslationHistory,
// inside the passthrough TxRunner — the recordings preserve that order.
func (m *mockQuerier) UpdateTranslationActiveSource(ctx context.Context, arg db.UpdateTranslationActiveSourceParams) error {
	m.updateActiveSourceCalls = append(m.updateActiveSourceCalls, arg)
	return nil
}

func (m *mockQuerier) InsertTranslationHistory(ctx context.Context, arg db.InsertTranslationHistoryParams) (db.TranslationHistory, error) {
	m.insertHistoryCalls = append(m.insertHistoryCalls, arg)
	return db.TranslationHistory{}, nil
}

// passthroughTxRunner is the test TxRunner: instead of opening a real tx,
// it just calls fn with the test's mock querier. Mock-based pipeline tests
// (tmwrite_test.go, handler_test.go, predict_test.go, translate_test.go)
// construct it as `&passthroughTxRunner{q: mq}` and pass it to NewService.
//
// Production code uses pipeline.NewPgxTxRunner(pool) which opens a real tx
// and binds db.New(tx) to the callback.
type passthroughTxRunner struct {
	q db.Querier
}

func (r *passthroughTxRunner) InTx(ctx context.Context, fn func(db.Querier) error) error {
	return fn(r.q)
}

func (m *mockQuerier) GetTranslation(ctx context.Context, id int64) (db.Translation, error) {
	return db.Translation{ID: id}, nil
}

// --- QE+APE workflow-step resolution (RunAIStep) ---

func (m *mockQuerier) GetJob(ctx context.Context, id int64) (db.Job, error) {
	j, ok := m.jobs[id]
	if !ok {
		return db.Job{}, fmt.Errorf("job %d not found", id)
	}
	return j, nil
}

func (m *mockQuerier) GetActiveStepForJob(ctx context.Context, jobID int64) (db.GetActiveStepForJobRow, error) {
	s, ok := m.activeSteps[jobID]
	if !ok {
		return db.GetActiveStepForJobRow{}, fmt.Errorf("no active step for job %d", jobID)
	}
	return s, nil
}

func (m *mockQuerier) GetWorkflowStep(ctx context.Context, id int64) (db.WorkflowStep, error) {
	ws, ok := m.workflowSteps[id]
	if !ok {
		return db.WorkflowStep{}, fmt.Errorf("workflow step %d not found", id)
	}
	return ws, nil
}

func (m *mockQuerier) GetCustomer(ctx context.Context, arg db.GetCustomerParams) (db.Customer, error) {
	c, ok := m.customers[arg.ID]
	if !ok || c.TenantID != arg.TenantID {
		return db.Customer{}, fmt.Errorf("customer %d not found for tenant %d", arg.ID, arg.TenantID)
	}
	return c, nil
}

// --- The lazy per-segment context sources ---

func (m *mockQuerier) ListCommentsByTranslationIDs(_ context.Context, ids []int64) ([]db.ListCommentsByTranslationIDsRow, error) {
	m.commentLookups++
	var rows []db.ListCommentsByTranslationIDsRow
	for _, id := range ids {
		for _, c := range m.segComments[id] {
			rows = append(rows, db.ListCommentsByTranslationIDsRow{TranslationID: id, Content: c})
		}
	}
	return rows, nil
}

func (m *mockQuerier) GetSegment(ctx context.Context, id int64) (db.Segment, error) {
	seg, ok := m.segments[id]
	if !ok {
		return db.Segment{}, fmt.Errorf("segment %d not found", id)
	}
	return seg, nil
}

// ListSegmentSourcesByFile mirrors the SQL (source-only, ordered by position).
// buildNeighborHashes relies on the position ordering.
func (m *mockQuerier) ListSegmentSourcesByFile(ctx context.Context, fileID int64) ([]db.ListSegmentSourcesByFileRow, error) {
	var rows []db.ListSegmentSourcesByFileRow
	for _, seg := range m.segments {
		if seg.FileID == fileID {
			rows = append(rows, db.ListSegmentSourcesByFileRow{ID: seg.ID, Position: seg.Position, SourceText: seg.SourceText})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Position < rows[j].Position })
	return rows, nil
}

// GetSegmentNeighborSources mirrors the SQL: nearest lower/higher position
// within the same file ("" at a boundary). Write-side context.
func (m *mockQuerier) GetSegmentNeighborSources(ctx context.Context, segmentID int64) (db.GetSegmentNeighborSourcesRow, error) {
	seg, ok := m.segments[segmentID]
	if !ok {
		return db.GetSegmentNeighborSourcesRow{}, fmt.Errorf("segment %d not found", segmentID)
	}
	var prev, next string
	var prevPos, nextPos int32
	havePrev, haveNext := false, false
	for _, other := range m.segments {
		if other.FileID != seg.FileID || other.ID == seg.ID {
			continue
		}
		if other.Position < seg.Position && (!havePrev || other.Position > prevPos) {
			prev, prevPos, havePrev = other.SourceText, other.Position, true
		}
		if other.Position > seg.Position && (!haveNext || other.Position < nextPos) {
			next, nextPos, haveNext = other.SourceText, other.Position, true
		}
	}
	return db.GetSegmentNeighborSourcesRow{PrevSource: prev, NextSource: next}, nil
}

// compile-time check
var _ db.Querier = (*mockQuerier)(nil)

// helper to add a file+project pair with project-level settings.
func (m *mockQuerier) addFileProject(fileID, projectID int64, templateID *int64, sourceLang string, targetLangs []string) {
	m.addFileProjectTenant(fileID, projectID, 1, templateID, sourceLang, targetLangs)
}

func (m *mockQuerier) addFileProjectTenant(fileID, projectID, tenantID int64, templateID *int64, sourceLang string, targetLangs []string) {
	m.files[fileID] = db.File{
		ID:         fileID,
		ProjectID:  projectID,
		Name:       fmt.Sprintf("file_%d.json", fileID),
		Format:     db.FileFormatJsonI18n,
		InlineHtml: true,
	}
	m.projects[projectID] = db.Project{
		ID:                projectID,
		Name:              fmt.Sprintf("project_%d", projectID),
		SourceLanguage:    sourceLang,
		TargetLanguages:   targetLangs,
		TemplateID:        templateID,
		TenantID:          tenantID,
		SettingsProfileID: projectID, // use projectID as settingsProfileID for simplicity in tests
		CreatedAt:         pgtype.Timestamptz{Valid: true},
		UpdatedAt:         pgtype.Timestamptz{Valid: true},
	}
	// Create a default settings profile for this project
	m.settingsProfiles[projectID] = db.SettingsProfile{
		ID:             projectID,
		FuzzyThreshold: 0.75,
		InlineHtml:     true,
	}
	// Point the profile at a Mock MT router. The pipeline skips MT entirely
	// when the profile has no router, so unit tests asserting MT calls need
	// one. Tests that exercise the "no MT engine" path call
	// `m.clearMTRouter(projectID)`.
	m.addMTRouter(projectID, tenantID, "mock")
}

// addMTRouter creates an MT engine of engineType plus a router defaulting to
// it, and points the settings profile at the router. engineType is passed
// straight to mt.NewFromDB — "mock" produces the in-memory Mock engine, which
// is what most unit tests want. Engine id = profile id; router id = profile
// id + 2000.
func (m *mockQuerier) addMTRouter(settingsProfileID, tenantID int64, engineType string) {
	engineID := settingsProfileID
	routerID := settingsProfileID + 2000
	m.mtEngines[engineID] = db.MtEngine{
		ID:         engineID,
		TenantID:   tenantID,
		Name:       engineType,
		EngineType: engineType,
		Enabled:    true,
	}
	m.mtRouters[routerID] = db.MtRouter{ID: routerID, TenantID: tenantID, Name: engineType + " router", DefaultMtEngineID: &engineID}
	sp := m.settingsProfiles[settingsProfileID]
	sp.MtRouterID = &routerID
	m.settingsProfiles[settingsProfileID] = sp
}

// clearMTRouter detaches the profile from its MT router so resolveMTEngine
// finds nothing and the MT phase is skipped.
func (m *mockQuerier) clearMTRouter(settingsProfileID int64) {
	sp := m.settingsProfiles[settingsProfileID]
	sp.MtRouterID = nil
	m.settingsProfiles[settingsProfileID] = sp
}

// helper to set fuzzy threshold on a project's settings profile
func (m *mockQuerier) setProjectFuzzyThreshold(projectID int64, threshold float32) {
	sp := m.settingsProfiles[projectID]
	sp.FuzzyThreshold = threshold
	m.settingsProfiles[projectID] = sp
}

// setProjectLabels gives a project its Customer and Domain picks — the two
// built-in labels prompts can read. An empty
// string leaves that label unset.
func (m *mockQuerier) setProjectLabels(projectID int64, customer, domain string) {
	for key, value := range map[string]string{"customer": customer, "domain": domain} {
		if value != "" {
			m.projectSystemLabels[systemLabelKey(projectID, key)] = value
		}
	}
}

func systemLabelKey(projectID int64, key string) string {
	return fmt.Sprintf("%d:%s", projectID, key)
}

func (m *mockQuerier) GetProjectSystemLabelValue(_ context.Context, arg db.GetProjectSystemLabelValueParams) (db.GetProjectSystemLabelValueRow, error) {
	p, ok := m.projects[arg.ProjectID]
	if !ok || p.TenantID != arg.TenantID {
		return db.GetProjectSystemLabelValueRow{}, pgx.ErrNoRows
	}
	if arg.SystemKey == nil {
		return db.GetProjectSystemLabelValueRow{}, pgx.ErrNoRows
	}
	v, ok := m.projectSystemLabels[systemLabelKey(arg.ProjectID, *arg.SystemKey)]
	if !ok {
		return db.GetProjectSystemLabelValueRow{}, pgx.ErrNoRows
	}
	return db.GetProjectSystemLabelValueRow{Value: v}, nil
}

// addAIStepJob wires a job onto a QE+APE workflow step so RunAIStep
// resolves: the job (fileID + targetLang), its active step, the workflow step
// carrying an AI router whose default is aiEngineID, the engine, and an
// extension language-routing row (no system prompt ⇒ the extension behind the
// row's key runs). Pair it with a fakeExtensions holding that key (extTokenID).
// Source language is taken
// from the already-seeded file's project (call addFileProject first). tenantID
// scopes the lookups.
func (m *mockQuerier) addAIStepJob(jobID, fileID, tenantID int64, targetLang, stepName string) {
	const workflowStepID, aiEngineID, engineRowID int64 = 900, 901, 902
	stepAssignID := int64(910)
	m.jobs[jobID] = db.Job{ID: jobID, FileID: fileID, TargetLanguage: targetLang, CurrentStepID: &stepAssignID}
	m.activeSteps[jobID] = db.GetActiveStepForJobRow{
		ID:             stepAssignID,
		JobID:          jobID,
		WorkflowStepID: workflowStepID,
		Status:         "in_progress",
		StepName:       stepName,
	}
	engineID := aiEngineID
	m.addAIRouterStep(workflowStepID, stepName, tenantID, engineID)
	m.aiEngines[aiEngineID] = db.AiEngine{ID: aiEngineID, TenantID: tenantID, Name: stepName, Purpose: "qe_ape", Enabled: true}

	tokenID := extTokenID(tenantID)
	m.aiEngineRows[aiEngineID] = []db.AiEngineRow{{
		ID:          engineRowID,
		EngineID:    aiEngineID,
		SourceLang:  m.projectSourceLang(fileID),
		TargetLangs: []string{targetLang},
		TokenID:     &tokenID,
	}}
}

// addLLMAIStepJob is the LLM variant of addAIStepJob: the engine row
// carries a system prompt whose flags spell the given mode — "qe" is status
// only, "ape" is text only, "qe_ape" is both — and a
// model preset on the given provider. Pair it with setAIProviderConn +
// SetAIProvider(nil, fakePredictor, identityDecryptor{}). badOutputAction ""
// leaves the row's action nil (runner defaults to reject).
func (m *mockQuerier) addLLMAIStepJob(jobID, fileID, tenantID int64, targetLang, mode, stepName, provider, badOutputAction string) {
	const workflowStepID, aiEngineID, engineRowID, presetID int64 = 900, 901, 902, 904
	stepAssignID := int64(910)
	m.jobs[jobID] = db.Job{ID: jobID, FileID: fileID, TargetLanguage: targetLang, CurrentStepID: &stepAssignID}
	m.activeSteps[jobID] = db.GetActiveStepForJobRow{
		ID:             stepAssignID,
		JobID:          jobID,
		WorkflowStepID: workflowStepID,
		Status:         "in_progress",
		StepName:       stepName,
	}
	engineID := aiEngineID
	m.addAIRouterStep(workflowStepID, stepName, tenantID, engineID)
	m.aiEngines[aiEngineID] = db.AiEngine{ID: aiEngineID, TenantID: tenantID, Name: stepName, Purpose: "qe_ape", Enabled: true}

	sysPromptID := fixtureSystemPromptID
	rowTokID := aiTokenID(tenantID, provider)
	// Token + model live on the row itself.
	row := db.AiEngineRow{
		ID:             engineRowID,
		EngineID:       aiEngineID,
		SourceLang:     m.projectSourceLang(fileID),
		TargetLangs:    []string{targetLang},
		SystemPromptID: &sysPromptID,
		TokenID:        &rowTokID,
		Model:          "test-model",
		Settings:       []byte("{}"),
	}
	if badOutputAction != "" {
		row.BadOutputAction = &badOutputAction
	}
	m.aiEngineRows[aiEngineID] = []db.AiEngineRow{row}
	// Flags from the real derivation rather than by hand: APE is the mode that
	// emits text and comments but NO status, and spelling that out here is how
	// a fixture ends up describing a mode the runtime would never produce.
	flags, ok := aiprompt.FlagsForMode(mode)
	if !ok {
		panic("addLLMAIStepJob: unknown mode " + mode)
	}
	m.aiPrompts[fixtureSystemPromptID] = db.AiPrompt{
		ID:          fixtureSystemPromptID,
		TenantID:    tenantID,
		Name:        stepName + " prompt",
		PromptText:  "Evaluate carefully.",
		OutStatus:   flags.OutStatus,
		OutText:     flags.OutText,
		OutNote:     flags.OutNote,
		OutComments: flags.OutComments,
	}
	// The token itself is created by setAIProviderConn — its absence models a
	// "not connected" vendor (GetVendorToken fails ⇒ benign no-provider skip).
}

func (m *mockQuerier) LookupSettingsTermsByTokens(_ context.Context, arg db.LookupSettingsTermsByTokensParams) ([]db.LookupSettingsTermsByTokensRow, error) {
	m.termLookups++
	all := m.settingsTerms[langKey(arg.SettingsProfileID, arg.SourceLanguage, arg.TargetLanguage)]
	want := make(map[string]bool, len(arg.Tokens))
	for _, t := range arg.Tokens {
		want[t] = true
	}
	var out []db.LookupSettingsTermsByTokensRow
	for _, row := range all {
		if want[strings.ToLower(row.SourceTerm)] {
			out = append(out, row)
		}
	}
	return out, nil
}

// setSegmentContext switches optional per-segment context on for the system
// prompt seeded by addLLMAIStepJob. Without this a prompt carries only the
// source text (and the translation, on a reviewing mode).
func (m *mockQuerier) setSegmentContext(keys ...string) {
	p := m.aiPrompts[fixtureSystemPromptID]
	p.SegmentContext = keys
	m.aiPrompts[fixtureSystemPromptID] = p
}

// projectSourceLang returns the seeded project's source language for a file so
// engine-row pair matching hits (call addFileProject first).
func (m *mockQuerier) projectSourceLang(fileID int64) string {
	if f, ok := m.files[fileID]; ok {
		if p, ok := m.projects[f.ProjectID]; ok {
			return p.SourceLanguage
		}
	}
	return ""
}

// setAIProviderConn seeds a connected vendor token for an LLM provider
// (openai / claude) for LLM rows.
func (m *mockQuerier) setAIProviderConn(tenantID int64, provider, apiKey string) {
	tokID := aiTokenID(tenantID, provider)
	m.vendorTokens[tokID] = db.GetVendorTokenRow{ID: tokID, Provider: provider, TenantID: tenantID, ApiKeyEncrypted: apiKey, Name: "Default"}
	m.firstTokens[fmt.Sprintf("%d:%s", tenantID, provider)] = db.GetFirstVendorTokenRow{ID: tokID, Name: "Default", ApiKeyEncrypted: apiKey}
}

func (m *mockQuerier) CreateAIComment(_ context.Context, arg db.CreateAICommentParams) (db.SegmentComment, error) {
	m.createAICommentCalls = append(m.createAICommentCalls, arg)
	return db.SegmentComment{ID: int64(len(m.createAICommentCalls)), TranslationID: arg.TranslationID, Content: arg.Content}, nil
}

// addAIRouterStep puts a workflow step on a fresh AI router whose default is
// engineID (no per-language rules). Router id = step id + 1000.
func (m *mockQuerier) addAIRouterStep(workflowStepID int64, stepName string, tenantID, engineID int64) {
	routerID := workflowStepID + 1000
	m.workflowSteps[workflowStepID] = db.WorkflowStep{ID: workflowStepID, Name: stepName, AiRouterID: &routerID}
	m.aiRouters[routerID] = db.AiRouter{ID: routerID, TenantID: tenantID, Name: stepName + " router", DefaultEngineID: &engineID}
}

// routerRule is one target language → engine mapping for setAIRouterRules.
type routerRule struct {
	targetLang string
	engineID   int64
}

// setAIRouterRules re-points the router addAIRouterStep put on workflowStepID:
// defaultEngineID becomes its default (nil = none, so a language no rule covers
// resolves to no engine) and rules replace its per-language mappings, in order.
// Rules win over the default, and airouter.ResolveEngineID matches them on the
// job's TARGET language.
func (m *mockQuerier) setAIRouterRules(workflowStepID int64, defaultEngineID *int64, rules ...routerRule) {
	routerID := workflowStepID + 1000
	router := m.aiRouters[routerID]
	router.DefaultEngineID = defaultEngineID
	m.aiRouters[routerID] = router
	out := make([]db.AiRouterRule, len(rules))
	for i, r := range rules {
		out[i] = db.AiRouterRule{
			ID: routerID*10 + int64(i), RouterID: routerID,
			TargetLang: r.targetLang, EngineID: r.engineID, SortOrder: int32(i),
		}
	}
	m.aiRouterRules[routerID] = out
}

// addLLMAIEngine seeds a standalone LLM QE+APE engine covering
// sourceLang→targetLang, with its own prompt text and model on the given
// provider. Pair it with setAIProviderConn. Ids derive from engineID, so keep
// it clear of the 900-904 block addLLMAIStepJob uses. A router test points its
// default at one of these and a rule at another: the predict config the run
// produces then names which engine the router actually picked.
func (m *mockQuerier) addLLMAIEngine(engineID, tenantID int64, sourceLang, targetLang, provider, promptText, model string) {
	promptID, rowID := engineID+1, engineID+2
	tokenID := aiTokenID(tenantID, provider)
	m.aiEngines[engineID] = db.AiEngine{ID: engineID, TenantID: tenantID, Name: model + " engine", Purpose: "qe_ape", Enabled: true}
	m.aiEngineRows[engineID] = []db.AiEngineRow{{
		ID: rowID, EngineID: engineID, SourceLang: sourceLang, TargetLangs: []string{targetLang},
		SystemPromptID: &promptID, TokenID: &tokenID, Model: model, Settings: []byte("{}"),
	}}
	m.aiPrompts[promptID] = db.AiPrompt{
		ID: promptID, TenantID: tenantID, Name: model + " prompt",
		PromptText: promptText, OutStatus: true, OutText: true,
	}
}

// setAIEngineEnabled flips an engine's switch. The builders create enabled
// engines because the column defaults to true, so a test only calls this to
// switch one OFF.
func (m *mockQuerier) setAIEngineEnabled(engineID int64, enabled bool) {
	e := m.aiEngines[engineID]
	e.Enabled = enabled
	m.aiEngines[engineID] = e
}

// setAIEngineRowToken replaces the token on an engine's rows. nil is what the
// FK's ON DELETE SET NULL leaves behind when the key is deleted from AI
// settings.
func (m *mockQuerier) setAIEngineRowToken(engineID int64, tokenID *int64) {
	rows := m.aiEngineRows[engineID]
	for i := range rows {
		rows[i].TokenID = tokenID
	}
	m.aiEngineRows[engineID] = rows
}

func (m *mockQuerier) GetAIRouterAny(_ context.Context, arg db.GetAIRouterAnyParams) (db.AiRouter, error) {
	r, ok := m.aiRouters[arg.ID]
	if !ok || r.TenantID != arg.TenantID {
		return db.AiRouter{}, fmt.Errorf("no AI router id=%d", arg.ID)
	}
	return r, nil
}

func (m *mockQuerier) ListAIRouterRules(_ context.Context, routerID int64) ([]db.AiRouterRule, error) {
	return m.aiRouterRules[routerID], nil
}

func (m *mockQuerier) ListAIEngineRows(_ context.Context, engineID int64) ([]db.AiEngineRow, error) {
	return m.aiEngineRows[engineID], nil
}

func (m *mockQuerier) GetAIPrompt(_ context.Context, arg db.GetAIPromptParams) (db.AiPrompt, error) {
	p, ok := m.aiPrompts[arg.ID]
	if !ok || p.TenantID != arg.TenantID {
		return db.AiPrompt{}, fmt.Errorf("no AI prompt id=%d", arg.ID)
	}
	return p, nil
}

func (m *mockQuerier) ListAIPromptLanguageAdditionsForPrompt(_ context.Context, promptID int64) ([]db.AiPromptLanguageAddition, error) {
	return m.aiPromptLangAdditions[promptID], nil
}

// identityDecryptor returns the encoded string unchanged, so LLM runners get
// the raw key.
type identityDecryptor struct{}

func (identityDecryptor) Decrypt(encoded string) (string, error) { return encoded, nil }

// GetJobByFileAndLang finds a seeded job by its file and language.
func (m *mockQuerier) GetJobByFileAndLang(_ context.Context, arg db.GetJobByFileAndLangParams) (db.Job, error) {
	for _, j := range m.jobs {
		if j.FileID == arg.FileID && j.TargetLanguage == arg.TargetLanguage {
			return j, nil
		}
	}
	return db.Job{}, pgx.ErrNoRows
}

// InsertProjectEvent records a history event instead of storing it.
func (m *mockQuerier) InsertProjectEvent(_ context.Context, arg db.InsertProjectEventParams) (int64, error) {
	m.projectEvents = append(m.projectEvents, arg)
	return int64(len(m.projectEvents)), nil
}
