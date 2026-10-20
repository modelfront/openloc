package mcp

import (
	"context"
	"strings"

	"openloc/internal/aiengine"
	"openloc/internal/aiprompt"
	"openloc/internal/airouter"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/mtrouter"
	"openloc/internal/ptr"
	"openloc/internal/settings"
	"openloc/internal/sliceutil"
	"openloc/internal/stepdef"
	"openloc/internal/workflowlib"
)

// AI configuration tools: the prompts, engines, steps and workflows a QE+APE or
// AI-as-MT setup is assembled from. Every write delegates to the same service
// the REST handler calls, so the two surfaces validate identically.
//
// Provider connections are read-only here by design — creating one takes an API
// key, which does not belong in a tool call.

// previewLen caps the prompt text returned by the list tools. Enough to
// recognise a prompt, not enough to make a listing of twenty of them unreadable.
const previewLen = 200

func preview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= previewLen {
		return s
	}
	return s[:previewLen] + "…"
}

func (b *directBackend) ListAIPrompts(ctx context.Context, in ListAIPromptsIn) (AIPromptList, error) {
	tenantID, _ := scope(ctx)
	// Same lazy provisioning the REST list does, so a tenant that has
	// never opened the AI page still sees the built-in templates.
	if err := aiprompt.EnsureDefaultPrompts(ctx, b.q, tenantID); err != nil {
		return AIPromptList{}, err
	}
	rows, err := b.q.ListAIPrompts(ctx, tenantID)
	if err != nil {
		return AIPromptList{}, err
	}
	// One query for the page, not one per prompt. The error is propagated
	// rather than swallowed: this list is the read half of the update tool's
	// read-modify-write, and an empty `language_additions` is indistinguishable
	// from "this prompt has none", so a silent failure here is how an agent
	// writes back nothing and deletes the tenant's rules.
	adds, err := b.q.ListAIPromptLanguageAdditions(ctx, tenantID)
	if err != nil {
		return AIPromptList{}, err
	}
	additions := map[int64][]MCPLanguageAddition{}
	for _, a := range adds {
		additions[a.PromptID] = append(additions[a.PromptID], MCPLanguageAddition{
			TargetLanguages: a.TargetLanguages,
			Text:            a.AdditionText,
		})
	}
	want := strings.TrimSpace(strings.ToLower(in.Purpose))
	out := AIPromptList{Prompts: make([]AIPrompt, 0, len(rows))}
	for _, p := range rows {
		mode := aiprompt.Mode(p)
		// "mt" selects MT prompts; anything else selects the review family, so
		// a caller filtering for qe_ape gets qe, ape and qe_ape alike.
		if want == "mt" && mode != "mt" {
			continue
		}
		if want == "qe_ape" && mode == "mt" {
			continue
		}
		ctxKeys := sliceutil.NonNil(p.SegmentContext)
		adds := sliceutil.NonNil(additions[p.ID])
		out.Prompts = append(out.Prompts, AIPrompt{
			PromptID: p.ID, Name: p.Name, Mode: mode,
			Preview: preview(p.PromptText), IsDefault: p.IsDefault,
			SegmentContext: ctxKeys, LanguageAdditions: adds,
		})
	}
	return out, nil
}

// ListAIConnections surfaces the stored provider credentials WITHOUT the
// credential — ListVendorTokens excludes the ciphertext, and nothing here adds
// it back. The token id is all an AI engine needs.
func (b *directBackend) ListAIConnections(ctx context.Context, _ ListAIConnectionsIn) (AIConnectionList, error) {
	tenantID, _ := scope(ctx)
	rows, err := b.q.ListVendorTokens(ctx, tenantID)
	if err != nil {
		return AIConnectionList{}, err
	}
	out := AIConnectionList{Connections: make([]AIConnection, 0, len(rows))}
	for _, t := range rows {
		c := AIConnection{TokenID: t.ID, Provider: t.Provider, Name: t.Name}
		// The catalog is per tenant and fetched from the vendor,
		// so the list a caller picks from is the same one
		// the AI page offers. Nil in a bare test backend: no models rather than
		// a panic, and create_ai_engine then accepts any id.
		if b.catalog != nil {
			for _, m := range b.catalog.Models(ctx, tenantID, t.Provider) {
				c.Models = append(c.Models, m.ID)
			}
		}
		out.Connections = append(out.Connections, c)
	}
	return out, nil
}

func (b *directBackend) ListAIEngines(ctx context.Context, _ ListAIEnginesIn) (AIEngineList, error) {
	tenantID, _ := scope(ctx)
	systems, err := b.q.ListAIEngines(ctx, tenantID)
	if err != nil {
		return AIEngineList{}, err
	}
	rows, err := b.q.ListAIEngineRowsForTenant(ctx, tenantID)
	if err != nil {
		return AIEngineList{}, err
	}
	// An engine has exactly one row; first-seen wins if that ever
	// changes, which is the same row the runtime would pick for a wildcard pair.
	modelBySystem := make(map[int64]string, len(rows))
	for _, r := range rows {
		if _, seen := modelBySystem[r.EngineID]; !seen {
			modelBySystem[r.EngineID] = r.Model
		}
	}
	out := AIEngineList{Systems: make([]AIEngine, 0, len(systems))}
	for _, s := range systems {
		out.Systems = append(out.Systems, AIEngine{
			AIEngineID: s.ID, Name: s.Name, Purpose: s.Purpose,
			Model: modelBySystem[s.ID],
		})
	}
	return out, nil
}

// nameIndex maps engine ids to names so a router listing can show the engine a
// route reaches instead of the id it stores.
func nameIndex[T any](rows []T, key func(T) (int64, string)) map[int64]string {
	m := make(map[int64]string, len(rows))
	for _, r := range rows {
		id, name := key(r)
		m[id] = name
	}
	return m
}

func (b *directBackend) ListAIRouters(ctx context.Context, _ ListAIRoutersIn) (AIRouterList, error) {
	tenantID, _ := scope(ctx)
	routers, err := b.q.ListAIRouters(ctx, tenantID)
	if err != nil {
		return AIRouterList{}, err
	}
	rules, err := b.q.ListAIRouterRulesForTenant(ctx, tenantID)
	if err != nil {
		return AIRouterList{}, err
	}
	engines, err := b.q.ListAIEngines(ctx, tenantID)
	if err != nil {
		return AIRouterList{}, err
	}
	names := nameIndex(engines, func(e db.AiEngine) (int64, string) { return e.ID, e.Name })
	byRouter := make(map[int64][]db.AiRouterRule, len(routers))
	for _, rr := range rules {
		byRouter[rr.RouterID] = append(byRouter[rr.RouterID], rr)
	}
	out := AIRouterList{Routers: make([]AIRouter, 0, len(routers))}
	for _, rt := range routers {
		r := AIRouter{AIRouterID: rt.ID, Name: rt.Name}
		if rt.DefaultEngineID != nil {
			r.DefaultEngineName = names[*rt.DefaultEngineID]
		}
		for _, rr := range byRouter[rt.ID] {
			r.Routes = append(r.Routes, RouterRoute{
				TargetLanguage: rr.TargetLang, EngineName: names[rr.EngineID],
			})
		}
		out.Routers = append(out.Routers, r)
	}
	return out, nil
}

func (b *directBackend) ListMTRouters(ctx context.Context, _ ListMTRoutersIn) (MTRouterList, error) {
	tenantID, _ := scope(ctx)
	routers, err := b.q.ListMTRouters(ctx, tenantID)
	if err != nil {
		return MTRouterList{}, err
	}
	rules, err := b.q.ListMTRouterRulesForTenant(ctx, tenantID)
	if err != nil {
		return MTRouterList{}, err
	}
	mtEngines, err := b.q.ListMTEngines(ctx, tenantID)
	if err != nil {
		return MTRouterList{}, err
	}
	aiEngines, err := b.q.ListAIEngines(ctx, tenantID)
	if err != nil {
		return MTRouterList{}, err
	}
	mtNames := nameIndex(mtEngines, func(e db.MtEngine) (int64, string) { return e.ID, e.Name })
	aiNames := nameIndex(aiEngines, func(e db.AiEngine) (int64, string) { return e.ID, e.Name })
	// A route names exactly one of the two engine families (mtrouter.checkEngine).
	engineName := func(mtID, aiID *int64) string {
		switch {
		case mtID != nil:
			return mtNames[*mtID]
		case aiID != nil:
			return aiNames[*aiID]
		}
		return ""
	}
	byRouter := make(map[int64][]db.MtRouterRule, len(routers))
	for _, rr := range rules {
		byRouter[rr.RouterID] = append(byRouter[rr.RouterID], rr)
	}
	out := MTRouterList{Routers: make([]MTRouter, 0, len(routers))}
	for _, rt := range routers {
		r := MTRouter{
			MTRouterID: rt.ID, Name: rt.Name,
			DefaultEngineName: engineName(rt.DefaultMtEngineID, rt.DefaultAiEngineID),
		}
		for _, rr := range byRouter[rt.ID] {
			r.Routes = append(r.Routes, RouterRoute{
				TargetLanguage: rr.TargetLang, EngineName: engineName(rr.MtEngineID, rr.AiEngineID),
			})
		}
		out.Routers = append(out.Routers, r)
	}
	return out, nil
}

func (b *directBackend) ListStepDefinitions(ctx context.Context, _ ListStepDefinitionsIn) (StepDefinitionList, error) {
	tenantID, _ := scope(ctx)
	// Every tenant is meant to have Translation and Review; ensure them here so
	// a first-time caller can compose a workflow without creating them by hand.
	if err := stepdef.EnsureDefaults(ctx, b.q, tenantID); err != nil {
		return StepDefinitionList{}, err
	}
	rows, err := b.q.ListStepDefinitions(ctx, tenantID)
	if err != nil {
		return StepDefinitionList{}, err
	}
	out := StepDefinitionList{Steps: make([]StepDefinition, 0, len(rows))}
	for _, r := range rows {
		sd := StepDefinition{StepDefinitionID: r.ID, Name: r.Name, StepType: r.StepType,
			AIRouterID: ptr.Deref(r.AiRouterID), AIRouterName: ptr.Deref(r.AiRouterName)}
		out.Steps = append(out.Steps, sd)
	}
	return out, nil
}

func (b *directBackend) ListWorkflows(ctx context.Context, _ ListWorkflowsIn) (WorkflowList, error) {
	tenantID, _ := scope(ctx)
	wfs, err := b.q.ListWorkflows(ctx, tenantID)
	if err != nil {
		return WorkflowList{}, err
	}
	steps, err := b.q.ListWorkflowDefinitionStepsForTenant(ctx, tenantID)
	if err != nil {
		return WorkflowList{}, err
	}
	byWorkflow := make(map[int64][]string, len(wfs))
	for _, s := range steps {
		byWorkflow[s.WorkflowID] = append(byWorkflow[s.WorkflowID], s.Name)
	}
	out := WorkflowList{Workflows: make([]WorkflowSummary, 0, len(wfs))}
	for _, wf := range wfs {
		out.Workflows = append(out.Workflows, WorkflowSummary{
			WorkflowID: wf.ID, Name: wf.Name, Steps: byWorkflow[wf.ID],
		})
	}
	return out, nil
}

func (b *directBackend) CreateAIPrompt(ctx context.Context, in CreateAIPromptIn) (CreateAIPromptResult, error) {
	tenantID, _ := scope(ctx)
	flags, ok := aiprompt.FlagsForMode(in.Mode)
	if !ok {
		return CreateAIPromptResult{}, invalidf("mode must be one of: mt, qe, ape, qe_ape")
	}
	flags.Name = in.Name
	flags.PromptText = in.Text
	flags.SegmentContext = in.SegmentContext
	flags.LanguageAdditions = toServiceAdditions(in.LanguageAdditions)

	prompt, _, err := aiprompt.CreateTx(ctx, b.q, b.pool, tenantID, flags)
	if err != nil {
		if msg, isValidation := aiprompt.AsValidation(err); isValidation {
			return CreateAIPromptResult{}, invalidf("%s", msg)
		}
		return CreateAIPromptResult{}, err
	}
	return CreateAIPromptResult{
		PromptID: prompt.ID, Name: prompt.Name, Mode: aiprompt.Mode(prompt),
		Note: "The reply's STRUCTURE is appended automatically at send time; what each field " +
			"should contain belongs in the instruction text. " +
			"Next: openloc_create_ai_engine to bind this prompt to a model.",
	}, nil
}

// toServiceAdditions converts the wire shape to the service's. Nil stays nil:
// the service reads it as "no rules", which on update is a replace-all with
// nothing — exactly what omitting the field should mean.
func toServiceAdditions(in []MCPLanguageAddition) []aiprompt.LanguageAddition {
	if in == nil {
		return nil
	}
	out := make([]aiprompt.LanguageAddition, 0, len(in))
	for _, a := range in {
		out = append(out, aiprompt.LanguageAddition{
			TargetLanguages: a.TargetLanguages,
			Text:            a.Text,
		})
	}
	return out
}

// UpdateAIPrompt replaces a prompt's editable fields.
//
// There is no mode on the input: a prompt's type is fixed after creation
// (engines are bound to it and validate against it), so the stored flags are
// carried over rather than re-derived from the caller. That makes a type change
// unrepresentable here instead of merely refused.
func (b *directBackend) UpdateAIPrompt(ctx context.Context, in UpdateAIPromptIn) (UpdateAIPromptResult, error) {
	tenantID, _ := scope(ctx)
	// The prompt row and its language rules must land together.
	if err := b.needPool("updating an AI prompt"); err != nil {
		return UpdateAIPromptResult{}, err
	}
	existing, err := b.q.GetAIPrompt(ctx, db.GetAIPromptParams{ID: in.PromptID, TenantID: tenantID})
	if err != nil {
		return UpdateAIPromptResult{}, invalidf("no AI prompt with id %d", in.PromptID)
	}
	if existing.IsDefault {
		return UpdateAIPromptResult{}, invalidf("built-in prompts are read-only — create a new prompt to customize")
	}

	// aiprompt.Update leaves validation to its caller, the way the REST handler
	// does — without this an empty name or a rule with no target language
	// reaches the DB CHECK as an opaque failure instead of an actionable message.
	svcIn := aiprompt.Input{
		Name:       in.Name,
		PromptText: in.Text,
		// The type is the stored one, not the caller's.
		OutStatus:         existing.OutStatus,
		OutText:           existing.OutText,
		OutNote:           existing.OutNote,
		OutComments:       existing.OutComments,
		SegmentContext:    in.SegmentContext,
		LanguageAdditions: toServiceAdditions(in.LanguageAdditions),
	}
	if err := aiprompt.Validate(&svcIn); err != nil {
		if msg, isValidation := aiprompt.AsValidation(err); isValidation {
			return UpdateAIPromptResult{}, invalidf("%s", msg)
		}
		return UpdateAIPromptResult{}, err
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return UpdateAIPromptResult{}, err
	}
	defer tx.Rollback(ctx)
	prompt, err := aiprompt.Update(ctx, db.New(tx), tenantID, in.PromptID, svcIn)
	if err != nil {
		if msg, isValidation := aiprompt.AsValidation(err); isValidation {
			return UpdateAIPromptResult{}, invalidf("%s", msg)
		}
		return UpdateAIPromptResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UpdateAIPromptResult{}, err
	}
	return UpdateAIPromptResult{
		PromptID: prompt.ID, Name: prompt.Name, Mode: aiprompt.Mode(prompt),
		Note: "Language rules and segment context are replaced, not merged — send the full set.",
	}, nil
}

func (b *directBackend) CreateAIEngine(ctx context.Context, in CreateAIEngineIn) (CreateAIEngineResult, error) {
	tenantID, _ := scope(ctx)
	// The engine and its provider row are two inserts that must land
	// together; without a pool there is no transaction to put them in.
	if err := b.needPool("creating an AI engine"); err != nil {
		return CreateAIEngineResult{}, err
	}
	row := aiengine.RowInput{TokenID: in.TokenID, Model: in.Model, SystemPromptID: ptr.NilIfZero(in.SystemPromptID)}
	action := strings.TrimSpace(in.BadOutputAction)
	if action == "" {
		action = "reject"
	}
	row.BadOutputAction = &action

	engine, _, err := aiengine.Create(ctx, b.q, b.pool, b.catalog, tenantID, aiengine.Input{
		Name: in.Name, Purpose: in.Purpose, Row: row,
	})
	if err != nil {
		if msg, isValidation := aiengine.AsValidation(err); isValidation {
			return CreateAIEngineResult{}, invalidf("%s", msg)
		}
		return CreateAIEngineResult{}, err
	}
	note := "Next: openloc_create_mt_router with this ai_engine_id, then point a project at that router with openloc_set_project_mt_router."
	if engine.Purpose == aiengine.PurposeQEAPE {
		note = "Next: openloc_create_ai_router with this ai_engine_id, then attach that router to a step with openloc_create_step_definition and order the steps with openloc_create_workflow."
	}
	return CreateAIEngineResult{
		AIEngineID: engine.ID, Name: engine.Name, Purpose: engine.Purpose, Note: note,
	}, nil
}

func (b *directBackend) CreateAIRouter(ctx context.Context, in CreateAIRouterIn) (CreateAIRouterResult, error) {
	tenantID, _ := scope(ctx)
	// The router and its rules are several inserts that must land together.
	if err := b.needPool("creating an AI router"); err != nil {
		return CreateAIRouterResult{}, err
	}
	rtIn := airouter.Input{Name: in.Name, DefaultEngineID: ptr.NilIfZero(in.DefaultAIEngineID),
		Rules: make([]airouter.RuleInput, 0, len(in.Rules))}
	for _, r := range in.Rules {
		rtIn.Rules = append(rtIn.Rules, airouter.RuleInput{TargetLang: r.TargetLanguage, EngineID: r.AIEngineID})
	}
	if msg := airouter.Validate(ctx, b.q, tenantID, &rtIn); msg != "" {
		return CreateAIRouterResult{}, invalidf("%s", msg)
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return CreateAIRouterResult{}, err
	}
	defer tx.Rollback(ctx)
	rt, rules, err := airouter.Create(ctx, db.New(tx), tenantID, rtIn)
	if err != nil {
		return CreateAIRouterResult{}, conflictOnDuplicate(err, "an AI router with this name already exists")
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateAIRouterResult{}, err
	}
	routes := len(rules)
	if rt.DefaultEngineID != nil {
		routes++
	}
	return CreateAIRouterResult{
		AIRouterID: rt.ID, Name: rt.Name, Routes: routes,
		Note: "Next: openloc_create_step_definition with this ai_router_id for a hybrid or ai step, " +
			"then openloc_create_workflow to put that step in order.",
	}, nil
}

func (b *directBackend) CreateMTRouter(ctx context.Context, in CreateMTRouterIn) (CreateMTRouterResult, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("creating an MT router"); err != nil {
		return CreateMTRouterResult{}, err
	}
	rtIn := mtrouter.Input{
		Name:    in.Name,
		Default: engineRef(in.DefaultMTEngineID, in.DefaultAIEngineID),
		Rules:   make([]mtrouter.RuleInput, 0, len(in.Rules)),
	}
	for _, r := range in.Rules {
		rtIn.Rules = append(rtIn.Rules, mtrouter.RuleInput{
			TargetLang: r.TargetLanguage, EngineRef: engineRef(r.MTEngineID, r.AIEngineID),
		})
	}
	if msg := mtrouter.Validate(ctx, b.q, b.dec, tenantID, &rtIn); msg != "" {
		return CreateMTRouterResult{}, invalidf("%s", msg)
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return CreateMTRouterResult{}, err
	}
	defer tx.Rollback(ctx)
	rt, rules, err := mtrouter.Create(ctx, db.New(tx), tenantID, rtIn)
	if err != nil {
		return CreateMTRouterResult{}, conflictOnDuplicate(err, "an MT router with this name already exists")
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateMTRouterResult{}, err
	}
	routes := len(rules)
	if rtIn.Default.IsSet() {
		routes++
	}
	return CreateMTRouterResult{
		MTRouterID: rt.ID, Name: rt.Name, Routes: routes,
		Note: "Created org-wide. Machine translation only runs for a project once you point it here " +
			"with openloc_set_project_mt_router.",
	}, nil
}

// conflictOnDuplicate turns a unique-name violation into a message the model
// can act on (pick another name); anything else is an internal error.
func conflictOnDuplicate(err error, msg string) error {
	if httputil.IsUniqueViolation(err) {
		return invalidf("%s", msg)
	}
	return err
}

// engineRef maps the tool's two optional ids onto the one-of-two ref the
// router library takes; 0 means "not given".
func engineRef(mtEngineID, aiEngineID int64) mtrouter.EngineRef {
	return mtrouter.EngineRef{MTEngineID: ptr.NilIfZero(mtEngineID), AIEngineID: ptr.NilIfZero(aiEngineID)}
}

func (b *directBackend) CreateStepDefinition(ctx context.Context, in CreateStepDefinitionIn) (CreateStepDefinitionResult, error) {
	tenantID, _ := scope(ctx)
	sdIn := stepdef.Input{Name: in.Name, StepType: strings.TrimSpace(strings.ToLower(in.StepType)), AIRouterID: ptr.NilIfZero(in.AIRouterID)}
	if msg := stepdef.Validate(ctx, b.q, tenantID, &sdIn); msg != "" {
		return CreateStepDefinitionResult{}, invalidf("%s", msg)
	}
	sd, err := b.q.CreateStepDefinition(ctx, db.CreateStepDefinitionParams{
		TenantID: tenantID, Name: sdIn.Name, StepType: sdIn.StepType,
		RoleRequired: stepdef.DefaultRole, AiRouterID: sdIn.AIRouterID,
	})
	if err != nil {
		return CreateStepDefinitionResult{}, err
	}
	return CreateStepDefinitionResult{
		StepDefinitionID: sd.ID, Name: sd.Name, StepType: sd.StepType,
		Note: "Next: openloc_create_workflow, listing this step's id in the order you want.",
	}, nil
}

func (b *directBackend) CreateWorkflow(ctx context.Context, in CreateWorkflowIn) (CreateWorkflowResult, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("creating a workflow"); err != nil {
		return CreateWorkflowResult{}, err
	}
	steps := make([]workflowlib.StepInput, 0, len(in.StepDefinitionIDs))
	for _, id := range in.StepDefinitionIDs {
		steps = append(steps, workflowlib.StepInput{StepDefinitionID: id})
	}
	d, err := workflowlib.CreateValidated(ctx, b.q, b.pool, tenantID, workflowlib.Input{Name: in.Name, Steps: steps})
	if err != nil {
		return CreateWorkflowResult{}, configErr(err)
	}
	return CreateWorkflowResult{
		WorkflowID: d.ID, Name: d.Name, Steps: len(d.Steps),
		Note: "Pass this workflow_id to openloc_create_project to run a project on it.",
	}, nil
}

// SetProjectMTRouter points a project's settings profile at an MT router.
//
// A project has one router, so this is a single pointer write rather than a
// merge: the router itself holds the per-language routing, and editing that is
// the router's own job, not this tool's.
func (b *directBackend) SetProjectMTRouter(ctx context.Context, in SetProjectMTRouterIn) (SetProjectMTRouterResult, error) {
	tenantID, _ := scope(ctx)
	if in.MTRouterID <= 0 {
		return SetProjectMTRouterResult{}, invalidf("mt_router_id is required — pick one from openloc_list_mt_routers")
	}

	// Tenant-scoped read first, so a project id from another tenant is a
	// not-found rather than a path into its settings.
	project, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return SetProjectMTRouterResult{}, err
	}

	// Same gate the REST settings patch uses: the router must be this tenant's.
	// SetSettingsProfileMTRouter takes the profile id on trust, so both sides of
	// the pointer are verified before it runs.
	routerID := in.MTRouterID
	if msg := mtrouter.ValidateProfileRouter(ctx, b.q, tenantID, &routerID); msg != "" {
		return SetProjectMTRouterResult{}, invalidf("%s", msg)
	}
	if err := b.q.SetSettingsProfileMTRouter(ctx, db.SetSettingsProfileMTRouterParams{
		ID: project.SettingsProfileID, MtRouterID: &routerID,
	}); err != nil {
		return SetProjectMTRouterResult{}, err
	}

	return SetProjectMTRouterResult{
		ProjectID:    project.ID,
		MTRouterID:   routerID,
		MTRouterName: ptr.Deref(settings.MTRouterName(ctx, b.q, tenantID, &routerID)),
		Note: "Machine translation for this project now runs through this router. " +
			"Re-run Apply MT on a job to translate with it.",
	}, nil
}
