package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"openloc/internal/aiengine"
	"openloc/internal/aiprompt"
	"openloc/internal/airouter"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtrouter"
)

// AI prompts, AI engines and both router families. Reads resolve engine names
// so a caller can check a route without a second lookup, and carry the ids an
// update tool takes back.

func (b *directBackend) aiEngineNames(ctx context.Context, tenantID int64) (map[int64]string, error) {
	engines, err := b.q.ListAIEngines(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list AI engines: %w", err)
	}
	return nameIndex(engines, func(e db.AiEngine) (int64, string) { return e.ID, e.Name }), nil
}

func (b *directBackend) GetAIRouter(ctx context.Context, in GetAIRouterIn) (AIRouterDetail, error) {
	tenantID, _ := scope(ctx)
	rt, err := b.q.GetAIRouter(ctx, db.GetAIRouterParams{ID: in.AIRouterID, TenantID: tenantID})
	if err != nil {
		return AIRouterDetail{}, configErr(err)
	}
	return b.aiRouterDetail(ctx, tenantID, rt)
}

func (b *directBackend) aiRouterDetail(ctx context.Context, tenantID int64, rt db.AiRouter) (AIRouterDetail, error) {
	rules, err := b.q.ListAIRouterRules(ctx, rt.ID)
	if err != nil {
		return AIRouterDetail{}, fmt.Errorf("list AI router rules: %w", err)
	}
	names, err := b.aiEngineNames(ctx, tenantID)
	if err != nil {
		return AIRouterDetail{}, err
	}
	steps, err := airouter.StepsUsing(ctx, b.q, tenantID, rt.ID)
	if err != nil {
		return AIRouterDetail{}, err
	}
	out := AIRouterDetail{AIRouterID: rt.ID, Name: rt.Name, Rules: []RouterRuleDetail{}, StepsUsing: []string{}}
	if rt.DefaultEngineID != nil {
		out.DefaultAIEngineID = *rt.DefaultEngineID
		out.DefaultEngineName = names[*rt.DefaultEngineID]
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, RouterRuleDetail{TargetLanguage: r.TargetLang, AIEngineID: r.EngineID, EngineName: names[r.EngineID]})
	}
	for _, s := range steps {
		out.StepsUsing = append(out.StepsUsing, s.Name)
	}
	return out, nil
}

func (b *directBackend) GetMTRouter(ctx context.Context, in GetMTRouterIn) (MTRouterDetail, error) {
	tenantID, _ := scope(ctx)
	rt, err := b.q.GetMTRouter(ctx, db.GetMTRouterParams{ID: in.MTRouterID, TenantID: tenantID})
	if err != nil {
		return MTRouterDetail{}, configErr(err)
	}
	return b.mtRouterDetail(ctx, tenantID, rt)
}

func (b *directBackend) mtRouterDetail(ctx context.Context, tenantID int64, rt db.MtRouter) (MTRouterDetail, error) {
	rules, err := b.q.ListMTRouterRules(ctx, rt.ID)
	if err != nil {
		return MTRouterDetail{}, fmt.Errorf("list MT router rules: %w", err)
	}
	mtEngines, err := b.q.ListMTEngines(ctx, tenantID)
	if err != nil {
		return MTRouterDetail{}, fmt.Errorf("list MT engines: %w", err)
	}
	mtNames := nameIndex(mtEngines, func(e db.MtEngine) (int64, string) { return e.ID, e.Name })
	aiNames, err := b.aiEngineNames(ctx, tenantID)
	if err != nil {
		return MTRouterDetail{}, err
	}
	refs, err := b.q.CountProfileRefsForMTRouter(ctx, db.CountProfileRefsForMTRouterParams{RouterID: &rt.ID, TenantID: tenantID})
	if err != nil {
		return MTRouterDetail{}, fmt.Errorf("count MT router references: %w", err)
	}
	route := func(lang string, mtID, aiID *int64) RouterRuleDetail {
		r := RouterRuleDetail{TargetLanguage: lang}
		if mtID != nil {
			r.MTEngineID, r.EngineName = *mtID, mtNames[*mtID]
		}
		if aiID != nil {
			r.AIEngineID, r.EngineName = *aiID, aiNames[*aiID]
		}
		return r
	}
	def := route("", rt.DefaultMtEngineID, rt.DefaultAiEngineID)
	out := MTRouterDetail{
		MTRouterID: rt.ID, Name: rt.Name, Rules: []RouterRuleDetail{}, UsedBy: int64(refs),
		DefaultMTEngineID: def.MTEngineID, DefaultAIEngineID: def.AIEngineID, DefaultEngineName: def.EngineName,
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, route(r.TargetLang, r.MtEngineID, r.AiEngineID))
	}
	return out, nil
}

// PreviewAIPrompt assembles a prompt with aiprompt.Preview, the function
// behind POST /api/ai/prompts/preview. A stored prompt is previewed with its
// own mode, text and context.
func (b *directBackend) PreviewAIPrompt(ctx context.Context, in PreviewAIPromptIn) (AIPromptPreview, error) {
	tenantID, _ := scope(ctx)
	draft := aiprompt.PreviewInput{
		PromptText: in.Text, SegmentContext: in.SegmentContext,
		SourceLang: in.SourceLanguage, TargetLang: in.TargetLanguage, Model: in.Model,
	}
	if in.PromptID != 0 {
		p, err := b.q.GetAIPrompt(ctx, db.GetAIPromptParams{ID: in.PromptID, TenantID: tenantID})
		if err != nil {
			return AIPromptPreview{}, configErr(err)
		}
		draft.PromptText, draft.SegmentContext = p.PromptText, p.SegmentContext
		draft.OutStatus, draft.OutText, draft.OutNote, draft.OutComments = p.OutStatus, p.OutText, p.OutNote, p.OutComments
	} else {
		flags, ok := aiprompt.FlagsForMode(in.Mode)
		if !ok {
			return AIPromptPreview{}, invalidf("give prompt_id, or mode (mt, qe, ape or qe_ape) and text")
		}
		draft.OutStatus, draft.OutText, draft.OutNote, draft.OutComments = flags.OutStatus, flags.OutText, flags.OutNote, flags.OutComments
	}
	resp, err := aiprompt.Preview(draft)
	if err != nil {
		return AIPromptPreview{}, fmt.Errorf("preview prompt: %w", err)
	}
	return AIPromptPreview{
		Mode: resp.Mode, Instruction: resp.Instruction, Appended: resp.Appended,
		SystemPrompt: resp.SystemPrompt, ContextPreview: string(resp.ContextPreview),
		SchemaEnforced: resp.SchemaEnforced, OutputSchema: resp.OutputSchema,
	}, nil
}

// aiEngineUpdate merges the caller's fields over the engine's stored row, so an
// omitted field keeps its value.
func (b *directBackend) aiEngineUpdate(ctx context.Context, tenantID int64, in UpdateAIEngineIn) (db.AiEngine, aiengine.UpdateInput, error) {
	e, err := b.q.GetAIEngine(ctx, db.GetAIEngineParams{ID: in.AIEngineID, TenantID: tenantID})
	if err != nil {
		return db.AiEngine{}, aiengine.UpdateInput{}, configErr(err)
	}
	rows, err := b.q.ListAIEngineRows(ctx, e.ID)
	if err != nil {
		return db.AiEngine{}, aiengine.UpdateInput{}, fmt.Errorf("list AI engine rows: %w", err)
	}
	up := aiengine.UpdateInput{Name: e.Name, Enabled: in.Enabled}
	if len(rows) > 0 {
		r := rows[0]
		up.Row = aiengine.RowInput{Model: r.Model, SystemPromptID: r.SystemPromptID, BadOutputAction: r.BadOutputAction}
		if len(r.Settings) > 0 {
			if err := json.Unmarshal(r.Settings, &up.Row.Settings); err != nil {
				return db.AiEngine{}, aiengine.UpdateInput{}, fmt.Errorf("decode AI engine row settings: %w", err)
			}
		}
		if r.TokenID != nil {
			up.Row.TokenID = *r.TokenID
		}
	}
	if in.Name != nil {
		up.Name = *in.Name
	}
	if in.TokenID != 0 {
		up.Row.TokenID = in.TokenID
	}
	if in.Model != "" {
		up.Row.Model = in.Model
	}
	if in.SystemPromptID != 0 {
		id := in.SystemPromptID
		up.Row.SystemPromptID = &id
	}
	if a := strings.TrimSpace(in.BadOutputAction); a != "" {
		up.Row.BadOutputAction = &a
	}
	return e, up, nil
}

func (b *directBackend) PreviewUpdateAIEngine(ctx context.Context, in UpdateAIEngineIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	e, up, err := b.aiEngineUpdate(ctx, tenantID, in)
	if err != nil {
		return Preview{}, err
	}
	if _, err := aiengine.ValidateUpdate(ctx, b.q, b.catalog, tenantID, e.ID, up); err != nil {
		return Preview{}, configErr(err)
	}
	refs, err := b.q.CountAIRouterRefsForAIEngine(ctx, db.CountAIRouterRefsForAIEngineParams{EngineID: &e.ID, TenantID: tenantID})
	if err != nil {
		return Preview{}, fmt.Errorf("count router references: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Update the AI engine %q.", e.Name)}
	if refs > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("Reached through %s; every job routed to it runs with the change from now on.", configPlural(int64(refs), "router")))
	}
	return p, nil
}

func (b *directBackend) UpdateAIEngine(ctx context.Context, in UpdateAIEngineIn) (AIEngine, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating an AI engine"); err != nil {
		return AIEngine{}, err
	}
	e, up, err := b.aiEngineUpdate(ctx, tenantID, in)
	if err != nil {
		return AIEngine{}, err
	}
	engine, rows, err := aiengine.Update(ctx, b.q, b.pool, b.catalog, tenantID, e.ID, up)
	if err != nil {
		return AIEngine{}, configErr(err)
	}
	out := AIEngine{AIEngineID: engine.ID, Name: engine.Name, Purpose: engine.Purpose}
	if len(rows) > 0 {
		out.Model = rows[0].Model
	}
	return out, nil
}

// routerInput converts the tool's router shape into the library's.
func configAIRouterInput(in CreateAIRouterIn) airouter.Input {
	out := airouter.Input{Name: in.Name, Rules: make([]airouter.RuleInput, 0, len(in.Rules))}
	if in.DefaultAIEngineID != 0 {
		id := in.DefaultAIEngineID
		out.DefaultEngineID = &id
	}
	for _, r := range in.Rules {
		out.Rules = append(out.Rules, airouter.RuleInput{TargetLang: r.TargetLanguage, EngineID: r.AIEngineID})
	}
	return out
}

func configMTRouterInput(in CreateMTRouterIn) mtrouter.Input {
	out := mtrouter.Input{
		Name:    in.Name,
		Default: engineRef(in.DefaultMTEngineID, in.DefaultAIEngineID),
		Rules:   make([]mtrouter.RuleInput, 0, len(in.Rules)),
	}
	for _, r := range in.Rules {
		out.Rules = append(out.Rules, mtrouter.RuleInput{TargetLang: r.TargetLanguage, EngineRef: engineRef(r.MTEngineID, r.AIEngineID)})
	}
	return out
}

func (b *directBackend) PreviewUpdateAIRouter(ctx context.Context, in UpdateAIRouterIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	rtIn := configAIRouterInput(in.CreateAIRouterIn)
	rt, err := airouter.ValidateUpdate(ctx, b.q, tenantID, in.AIRouterID, &rtIn)
	if err != nil {
		return Preview{}, configErr(err)
	}
	refs, err := b.q.CountStepRefsForAIRouter(ctx, db.CountStepRefsForAIRouterParams{RouterID: &rt.ID, TenantID: tenantID})
	if err != nil {
		return Preview{}, fmt.Errorf("count step references: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Replace the routes of the AI router %q.", rt.Name)}
	if refs > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("Used by %s; each job that starts one of them from now on is reviewed by the new routes.", configPlural(int64(refs), "workflow step")))
	}
	return p, nil
}

func (b *directBackend) UpdateAIRouter(ctx context.Context, in UpdateAIRouterIn) (AIRouterDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating an AI router"); err != nil {
		return AIRouterDetail{}, err
	}
	rt, _, err := airouter.Update(ctx, b.q, b.pool, tenantID, in.AIRouterID, configAIRouterInput(in.CreateAIRouterIn))
	if err != nil {
		return AIRouterDetail{}, configErr(err)
	}
	return b.aiRouterDetail(ctx, tenantID, rt)
}

func (b *directBackend) PreviewUpdateMTRouter(ctx context.Context, in UpdateMTRouterIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	rtIn := configMTRouterInput(in.CreateMTRouterIn)
	rt, err := mtrouter.ValidateUpdate(ctx, b.q, b.dec, tenantID, in.MTRouterID, &rtIn)
	if err != nil {
		return Preview{}, configErr(err)
	}
	refs, err := b.q.CountProfileRefsForMTRouter(ctx, db.CountProfileRefsForMTRouterParams{RouterID: &rt.ID, TenantID: tenantID})
	if err != nil {
		return Preview{}, fmt.Errorf("count MT router references: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Replace the routes of the MT router %q.", rt.Name)}
	if refs > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("Used by %s (projects and templates); machine translation there uses the new routes from now on.", configPlural(int64(refs), "settings profile")))
	}
	return p, nil
}

func (b *directBackend) UpdateMTRouter(ctx context.Context, in UpdateMTRouterIn) (MTRouterDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating an MT router"); err != nil {
		return MTRouterDetail{}, err
	}
	rt, _, err := mtrouter.Update(ctx, b.q, b.pool, b.dec, tenantID, in.MTRouterID, configMTRouterInput(in.CreateMTRouterIn))
	if err != nil {
		return MTRouterDetail{}, configErr(err)
	}
	return b.mtRouterDetail(ctx, tenantID, rt)
}

func (b *directBackend) PreviewDeleteAIEngine(ctx context.Context, in DeleteAIEngineIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	e, err := aiengine.CheckDelete(ctx, b.q, tenantID, in.AIEngineID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	return Preview{Summary: fmt.Sprintf("Delete the AI engine %q. No router uses it.", e.Name)}, nil
}

func (b *directBackend) DeleteAIEngine(ctx context.Context, in DeleteAIEngineIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("deleting an AI engine"); err != nil {
		return DeletedConfig{}, err
	}
	e, err := aiengine.CheckDelete(ctx, b.q, tenantID, in.AIEngineID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := aiengine.Delete(ctx, b.q, b.pool, tenantID, e.ID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: e.ID, Name: e.Name}, nil
}

func (b *directBackend) PreviewDeleteAIRouter(ctx context.Context, in DeleteAIRouterIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	rt, err := airouter.CheckDelete(ctx, b.q, tenantID, in.AIRouterID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	return Preview{Summary: fmt.Sprintf("Delete the AI router %q. No workflow step uses it.", rt.Name)}, nil
}

func (b *directBackend) DeleteAIRouter(ctx context.Context, in DeleteAIRouterIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	rt, err := airouter.CheckDelete(ctx, b.q, tenantID, in.AIRouterID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := airouter.Delete(ctx, b.q, tenantID, rt.ID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: rt.ID, Name: rt.Name}, nil
}

func (b *directBackend) PreviewDeleteMTRouter(ctx context.Context, in DeleteMTRouterIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	rt, err := mtrouter.CheckDelete(ctx, b.q, tenantID, in.MTRouterID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	return Preview{Summary: fmt.Sprintf("Delete the MT router %q. No project or template uses it.", rt.Name)}, nil
}

func (b *directBackend) DeleteMTRouter(ctx context.Context, in DeleteMTRouterIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	rt, err := mtrouter.CheckDelete(ctx, b.q, tenantID, in.MTRouterID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := mtrouter.Delete(ctx, b.q, tenantID, rt.ID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: rt.ID, Name: rt.Name}, nil
}
