package ai

import "strings"

// Schema-enforced output. The batched contract is described to the model in
// prose (batchFormats) AND, where the model supports it, handed to the
// provider as a JSON schema it must satisfy — OpenAI's `response_format`
// json_schema and Anthropic's `output_config.format`.
//
// Where the schema goes, the prose that describes the same shape does not:
// batchFormats drops its output half, because repeating a constraint the
// provider already enforces is a few hundred tokens on every single request.
//
// The schema is shape only — names, types, nullability. What each field MEANS
// is instruction text, and instruction text belongs to the tenant: it ships in
// the stock templates and can be reworded there. Carrying meanings here too
// would give the model two voices, and no way to tell which one won.
//
// Capability is a pure function of the model id, like familyFromID and for the
// same reason: it is read on the worker's request path, and getting it wrong is
// a 400 on every segment rather than a soft degradation. Where a family would
// be too coarse (familyOpenAIChat spans gpt-4o, which takes a schema, and
// gpt-4-turbo, which does not) the id is matched directly. When in doubt the
// answer is false — that path still works.

// schemaName labels the schema for OpenAI, which requires a name.
const schemaName = "segment_results"

// claudeFirstSchemaVersion is the Anthropic generation from which structured
// outputs are available. Haiku 4.5 supports them too and sits below the line,
// so it is named separately rather than by lowering the threshold — dropping to
// 4.5 would sweep in Sonnet 4.5, which does not.
const claudeFirstSchemaVersion = 4.8

var claudeSchemaExceptions = []string{"claude-haiku-4-5"}

// SupportsStructuredOutput reports whether provider/model accepts a JSON schema
// on the request. Exported so the prompt preview can tell the author whether
// the engine they configured will have its output shape enforced.
func SupportsStructuredOutput(provider, model string) bool {
	base := strings.ToLower(baseModelID(strings.TrimSpace(model)))
	switch provider {
	case ProviderOpenAI:
		return openAITakesSchema(base)
	case ProviderClaude:
		if !strings.HasPrefix(base, "claude-") {
			return false
		}
		for _, id := range claudeSchemaExceptions {
			if strings.HasPrefix(base, id) {
				return true
			}
		}
		return claudeVersion(base) >= claudeFirstSchemaVersion
	}
	return false
}

// openAITakesSchema decides by id shape, because generation alone can't:
// gpt-4o takes a schema and gpt-4 does not, and both read as generation 4.
//
//   - the reasoning line (o3, o4, gpt-5+) takes one, except o1-preview and
//     o1-mini, which predate the feature;
//   - above generation 4 (gpt-4.1 and up) takes one;
//   - at generation 4, only the "4o" line does;
//   - below that, nothing does.
func openAITakesSchema(base string) bool {
	if !hasAnyPrefix(base, openAIAllowPrefixes) {
		return false
	}
	if strings.HasPrefix(base, "o1-preview") || strings.HasPrefix(base, "o1-mini") {
		return false
	}
	if oSeries.MatchString(base) {
		return true
	}
	gen := gptGeneration(base)
	if gen > 4 {
		return true
	}
	if gen == 4 {
		rest, _ := strings.CutPrefix(base, "gpt-")
		seg, _, _ := strings.Cut(rest, "-")
		return strings.HasPrefix(seg, "4o")
	}
	return false
}

// setClaudeOutputFormat nests the schema under `output_config.format`. It
// merges rather than assigns because applyClaude may already have written
// `output_config.effort` — overwriting the map there would silently drop a
// tenant's thinking-depth setting, which nothing downstream would report.
func setClaudeOutputFormat(body map[string]any, schema map[string]any) {
	cfg, ok := body["output_config"].(map[string]any)
	if !ok {
		cfg = map[string]any{}
		body["output_config"] = cfg
	}
	cfg["format"] = claudeSchemaFormat(schema)
}

// The two vendors carry the same schema in different envelopes. Both live here
// so neither request path can drift from ResultsSchema, which is what the
// preview shows — the envelope is the vendor's, the schema inside it is shared.
func openAISchemaFormat(schema map[string]any) map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   schemaName,
			"strict": true,
			"schema": schema,
		},
	}
}

func claudeSchemaFormat(schema map[string]any) map[string]any {
	return map[string]any{"type": "json_schema", "schema": schema}
}

// ResultsSchema is the JSON schema for one batched reply, matching the fields
// batchFormats asks for in prose. Written to satisfy BOTH vendors' subsets:
// every object carries `additionalProperties: false` and lists every property
// in `required` (OpenAI strict mode rejects anything else), and a nullable
// field is an `anyOf` rather than a `["string","null"]` type array — the union
// form is the one both vendors document.
func ResultsSchema(mode string) map[string]any {
	props := map[string]any{
		"id": strProp(),
	}
	order := []string{"id"}

	add := func(key string, schema map[string]any) {
		props[key] = schema
		order = append(order, key)
	}

	switch mode {
	case ModeMT:
		// Translation only: an MT prompt declares no comment output.
		add("target", strProp())
	case ModeQE:
		add("approved", boolProp())
		add("score", scoreProp())
		add("comment", nullable(strProp()))
	case ModeAPE:
		// No verdict and no score: an APE prompt stores out_status = false, so
		// it never decides whether a segment ships. A score is a judgement, and
		// a human makes that one on every segment this mode touches.
		add("postedit", nullable(strProp()))
		add("comment", nullable(strProp()))
	default: // QE+APE judges and post-edits.
		add("postedit", nullable(strProp()))
		add("approved", boolProp())
		add("score", scoreProp())
		add("postedit_approved", nullable(boolProp()))
		add("postedit_score", nullable(scoreProp()))
		add("comment", nullable(strProp()))
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"results"},
		"properties": map[string]any{
			"results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             order,
					"properties":           props,
				},
			},
		},
	}
}

func strProp() map[string]any  { return map[string]any{"type": "string"} }
func boolProp() map[string]any { return map[string]any{"type": "boolean"} }

// scoreProp is the one property that carries a description. A score's RANGE is
// the platform's, not the prompt's: clampScore bounds every score to 0-100
// whatever a prompt asks for, so a model told otherwise would have its answer
// silently rewritten.
//
// A description rather than minimum/maximum: OpenAI's strict mode rejects
// numeric bounds, and a schema it rejects is a 400 on every segment.
func scoreProp() map[string]any {
	return map[string]any{"type": "number", "description": "0-100, where 100 is perfect."}
}

// nullable wraps a property so it may also be null. The union form is the one
// both vendors document; a ["string","null"] type array is OpenAI-only. A
// description stays on the OUTSIDE, where both vendors read it — inside the
// union it describes one branch rather than the field.
func nullable(prop map[string]any) map[string]any {
	inner := map[string]any{}
	for k, v := range prop {
		if k != "description" {
			inner[k] = v
		}
	}
	out := map[string]any{
		"anyOf": []any{inner, map[string]any{"type": "null"}},
	}
	if desc, ok := prop["description"]; ok {
		out["description"] = desc
	}
	return out
}
