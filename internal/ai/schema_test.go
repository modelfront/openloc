package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Capability is read on the worker's request path and a wrong "yes" is a 400 on
// every segment, so the cases that matter are the near-misses: ids that look
// like a supported model and aren't.
func TestSupportsStructuredOutput(t *testing.T) {
	tests := []struct {
		provider, model string
		want            bool
	}{
		// OpenAI — the 4o line takes a schema, plain gpt-4 does not, and both
		// read as generation 4.
		{ProviderOpenAI, "gpt-4o", true},
		{ProviderOpenAI, "gpt-4o-mini", true},
		{ProviderOpenAI, "gpt-4o-2024-08-06", true},
		{ProviderOpenAI, "gpt-4", false},
		{ProviderOpenAI, "gpt-4-turbo", false},
		{ProviderOpenAI, "gpt-4-32k", false},
		{ProviderOpenAI, "gpt-4.1", true},
		{ProviderOpenAI, "gpt-4.1-mini", true},
		{ProviderOpenAI, "gpt-5", true},
		{ProviderOpenAI, "gpt-3.5-turbo", false},
		{ProviderOpenAI, "o3", true},
		{ProviderOpenAI, "o4-mini", true},
		// The o1 previews predate the feature.
		{ProviderOpenAI, "o1-preview", false},
		{ProviderOpenAI, "o1-mini", false},
		// A fine-tune inherits its base model's answer.
		{ProviderOpenAI, "ft:gpt-4o:acme::abc123", true},
		{ProviderOpenAI, "ft:gpt-4:acme::abc123", false},
		// Not a text model at all.
		{ProviderOpenAI, "text-embedding-3-small", false},

		// Anthropic — the current generation takes one; Sonnet 4.5 does not,
		// and must not be swept in by a threshold set to catch Haiku 4.5.
		{ProviderClaude, "claude-opus-5", true},
		{ProviderClaude, "claude-sonnet-5", true},
		{ProviderClaude, "claude-opus-4-8", true},
		{ProviderClaude, "claude-fable-5-1", true},
		{ProviderClaude, "claude-haiku-4-5", true},
		{ProviderClaude, "claude-haiku-4-5-20251001", true},
		{ProviderClaude, "claude-sonnet-4-5", false},
		{ProviderClaude, "claude-3-5-sonnet-20241022", false},

		// An extension runs its own QE+APE — no LLM request, no schema.
		{testExtProvider, "anything", false},
		{ProviderOpenAI, "", false},
	}
	for _, tt := range tests {
		if got := SupportsStructuredOutput(tt.provider, tt.model); got != tt.want {
			t.Errorf("SupportsStructuredOutput(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
		}
	}
}

// OpenAI strict mode rejects a schema whose objects allow extra properties or
// leave a property out of `required`. Anthropic rejects `additionalProperties`
// set to anything but false. Walk the whole tree rather than spot-checking the
// root, because a nested object is exactly where this gets forgotten.
func TestResultsSchema_SatisfiesStrictMode(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		t.Run(mode, func(t *testing.T) {
			schema := ResultsSchema(mode)
			// It must survive the round trip that the request body makes.
			raw, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("schema does not marshal: %v", err)
			}
			var back map[string]any
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("schema does not round-trip: %v", err)
			}
			assertStrictObjects(t, back, "$")
		})
	}
}

func assertStrictObjects(t *testing.T, node map[string]any, path string) {
	t.Helper()
	props, hasProps := node["properties"].(map[string]any)
	if node["type"] == "object" {
		if node["additionalProperties"] != false {
			t.Errorf("%s: object must set additionalProperties:false", path)
		}
		req, _ := node["required"].([]any)
		if len(req) != len(props) {
			t.Errorf("%s: every property must be required (required=%d, properties=%d)", path, len(req), len(props))
		}
		named := map[string]bool{}
		for _, r := range req {
			named[r.(string)] = true
		}
		for key := range props {
			if !named[key] {
				t.Errorf("%s: property %q missing from required", path, key)
			}
		}
	}
	if hasProps {
		for key, v := range props {
			if child, ok := v.(map[string]any); ok {
				assertStrictObjects(t, child, path+"."+key)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		assertStrictObjects(t, items, path+"[]")
	}
	for _, alt := range anyOfOf(node) {
		assertStrictObjects(t, alt, path+"|anyOf")
	}
}

func anyOfOf(node map[string]any) []map[string]any {
	raw, ok := node["anyOf"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, a := range raw {
		if m, ok := a.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// The schema has to ask for the same fields the prose contract does, or the
// model is told two different things and the stricter one wins silently.
func TestResultsSchema_FieldsMatchMode(t *testing.T) {
	fields := func(mode string) map[string]any {
		results := ResultsSchema(mode)["properties"].(map[string]any)["results"].(map[string]any)
		return results["items"].(map[string]any)["properties"].(map[string]any)
	}
	has := func(mode, key string) bool { _, ok := fields(mode)[key]; return ok }

	// Every mode echoes the id — that is how a result is matched back to its
	// segment.
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		if !has(mode, "id") {
			t.Errorf("%s: missing id", mode)
		}
	}
	if !has(ModeMT, "target") || has(ModeMT, "approved") {
		t.Error("MT must ask for a target and not a verdict")
	}
	if has(ModeQE, "postedit") {
		t.Error("QE must not offer a postedit — a QE engine discards one")
	}
	// Both post-editing modes offer a postedit; only QE+APE judges. See
	// ape_contract_test.go for the split in full.
	for _, mode := range []string{ModeAPE, ModeQEAPE} {
		if !has(mode, "postedit") {
			t.Errorf("%s: missing postedit", mode)
		}
	}
	// A score is a judgement, so it belongs to the judging modes only.
	for _, key := range []string{"postedit_approved", "postedit_score", "approved", "score"} {
		if !has(ModeQEAPE, key) {
			t.Errorf("QE+APE: missing %s", key)
		}
		if has(ModeAPE, key) {
			t.Errorf("APE declares no status output, so it must not be asked for %s", key)
		}
	}
}

// A nullable field must actually admit null, or a model with nothing to flag
// has no legal way to say so and is forced to invent a comment.
// A score's range is the platform's, not the prompt's: clampScore bounds every
// score to 0-100 whatever a prompt asks for, so the schema has to say so or a
// model can be told one scale and have its answer rewritten to another. It is
// the ONLY field carrying a description — the rest is shape, and meaning lives
// in the prompt text.
func TestResultsSchema_ScoreCarriesItsRange(t *testing.T) {
	for _, mode := range []string{ModeQE, ModeQEAPE} {
		items := ResultsSchema(mode)["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)
		props := items["properties"].(map[string]any)
		for key, prop := range props {
			desc, _ := prop.(map[string]any)["description"].(string)
			isScore := strings.Contains(key, "score")
			if isScore && !strings.Contains(desc, "0-100") {
				t.Errorf("mode=%s: %s must state its 0-100 range, got %q", mode, key, desc)
			}
			if !isScore && desc != "" {
				t.Errorf("mode=%s: %s carries a description (%q) — meaning belongs in the prompt text", mode, key, desc)
			}
		}
	}
	// postedit_score is nullable, so its description must survive the union
	// wrapper rather than describing only the number branch.
	qeape := ResultsSchema(ModeQEAPE)["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)
	ps := qeape["properties"].(map[string]any)["postedit_score"].(map[string]any)
	if desc, _ := ps["description"].(string); !strings.Contains(desc, "0-100") {
		t.Errorf("postedit_score lost its range when it was made nullable: %+v", ps)
	}
}

func TestResultsSchema_NullableFieldsAdmitNull(t *testing.T) {
	items := ResultsSchema(ModeQEAPE)["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)
	props := items["properties"].(map[string]any)
	for _, key := range []string{"comment", "postedit", "postedit_approved", "postedit_score"} {
		prop := props[key].(map[string]any)
		var sawNull bool
		for _, alt := range anyOfOf(prop) {
			if alt["type"] == "null" {
				sawNull = true
			}
		}
		if !sawNull {
			t.Errorf("%s must admit null", key)
		}
	}
	// Non-nullable fields must NOT admit null — an "approved" the model can
	// leave null is a verdict we cannot route.
	if len(anyOfOf(props["approved"].(map[string]any))) != 0 {
		t.Error("approved must be a plain boolean")
	}
}

// The schema is added to the Anthropic body after the tenant's settings, and
// both live under output_config. Assigning instead of merging would drop the
// effort setting with nothing reporting it.
func TestSetClaudeOutputFormat_KeepsEffort(t *testing.T) {
	body := map[string]any{"model": "claude-opus-5"}
	Settings{ParamKeyEffort: "max"}.applyClaude(body, "claude-opus-5", 4096)

	setClaudeOutputFormat(body, ResultsSchema(ModeQE))

	cfg, ok := body["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config missing or wrong type: %#v", body["output_config"])
	}
	if cfg["effort"] != "max" {
		t.Errorf("effort = %v, want max — the schema must merge, not replace", cfg["effort"])
	}
	if cfg["format"] == nil {
		t.Error("format missing")
	}
}

// …and the other way round: a request with no effort setting still gets a
// well-formed output_config.
func TestSetClaudeOutputFormat_CreatesConfigWhenAbsent(t *testing.T) {
	body := map[string]any{"model": "claude-opus-5"}
	setClaudeOutputFormat(body, ResultsSchema(ModeQE))

	cfg, ok := body["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config missing or wrong type: %#v", body["output_config"])
	}
	format, ok := cfg["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" {
		t.Errorf("format = %#v, want a json_schema block", cfg["format"])
	}
}

// Unsupported models must get no schema at all — the prose contract carries
// them, and sending the parameter anyway is a hard 400.
func TestCallConfigOutputSchema_NilOnUnsupportedModel(t *testing.T) {
	cc := callConfig{provider: ProviderOpenAI, mode: ModeQE}
	if cc.outputSchema("gpt-4-turbo") != nil {
		t.Error("gpt-4-turbo must not be sent a schema")
	}
	if cc.outputSchema("gpt-4o") == nil {
		t.Error("gpt-4o must be sent a schema")
	}
}

// What the unit tests above establish about the schema only matters if it
// reaches the request body, under the parameter name each vendor expects.
func TestStructuredOutput_ReachesTheRequestBody(t *testing.T) {
	t.Run("openai sends a strict response_format", func(t *testing.T) {
		srv, got := captureBody(t, false)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
			Provider: ProviderOpenAI, Model: testOpenAIModel, APIKey: "sk", BaseURL: srv.URL,
		}, texts("hello")); err != nil {
			t.Fatal(err)
		}
		rf, ok := (*got)["response_format"].(map[string]any)
		if !ok {
			t.Fatalf("response_format missing: %#v", (*got)["response_format"])
		}
		if rf["type"] != "json_schema" {
			t.Errorf("type = %v, want json_schema", rf["type"])
		}
		js, ok := rf["json_schema"].(map[string]any)
		if !ok {
			t.Fatalf("json_schema block missing: %#v", rf["json_schema"])
		}
		// Without strict the schema is a hint, not a guarantee — which is the
		// entire reason for sending it.
		if js["strict"] != true {
			t.Errorf("strict = %v, want true", js["strict"])
		}
		if js["name"] == nil || js["schema"] == nil {
			t.Errorf("json_schema needs both a name and a schema: %#v", js)
		}
	})

	t.Run("claude sends output_config.format", func(t *testing.T) {
		srv, got := captureBody(t, true)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
			Provider: ProviderClaude, Model: testOpus5, APIKey: "sk-ant", BaseURL: srv.URL,
		}, texts("hello")); err != nil {
			t.Fatal(err)
		}
		cfg, ok := (*got)["output_config"].(map[string]any)
		if !ok {
			t.Fatalf("output_config missing: %#v", (*got)["output_config"])
		}
		format, ok := cfg["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Errorf("format = %#v, want a json_schema block", cfg["format"])
		}
		// Anthropic takes no `strict` flag and no schema name — sending
		// OpenAI's spelling here would be rejected.
		if _, present := (*got)["response_format"]; present {
			t.Error("response_format is OpenAI's parameter and must not be sent to Anthropic")
		}
	})

	t.Run("a model that cannot take a schema is sent none", func(t *testing.T) {
		srv, got := captureBody(t, false)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
			Provider: ProviderOpenAI, Model: "gpt-4-turbo", APIKey: "sk", BaseURL: srv.URL,
		}, texts("hello")); err != nil {
			t.Fatal(err)
		}
		if _, present := (*got)["response_format"]; present {
			t.Error("gpt-4-turbo rejects response_format — it must be left off")
		}
	})

	// The dangerous failure is a disagreement: the prompt omits the format
	// instructions because it believes a schema is coming, and the body carries
	// none — the model is then told nothing at all about the shape. Both read
	// the same predicate through the same model resolution, and this asserts
	// they land together on a request that actually goes out.
	t.Run("the prompt drops the shape exactly when the body carries a schema", func(t *testing.T) {
		for _, tc := range []struct {
			model      string
			wantSchema bool
		}{
			{testOpenAIModel, true}, // gpt-4o
			{"gpt-4-turbo", false},
			{"", true}, // blank falls back to gpt-4o-mini, which takes one
		} {
			srv, got := captureBody(t, false)
			tr := NewHTTPTranslator()
			if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
				Provider: ProviderOpenAI, Model: tc.model, APIKey: "sk", BaseURL: srv.URL,
			}, texts("hello")); err != nil {
				srv.Close()
				t.Fatalf("model %q: %v", tc.model, err)
			}
			_, hasSchema := (*got)["response_format"]
			system, _ := (*got)["messages"].([]any)[0].(map[string]any)["content"].(string)
			spellsOutShape := strings.Contains(system, `{"results"`)
			srv.Close()

			if hasSchema != tc.wantSchema {
				t.Errorf("model %q: schema on the wire = %v, want %v", tc.model, hasSchema, tc.wantSchema)
			}
			// Exactly one of the two must be telling the model the shape.
			if hasSchema == spellsOutShape {
				t.Errorf("model %q: schema=%v and prose=%v — the model is told the shape twice or not at all",
					tc.model, hasSchema, spellsOutShape)
			}
		}
	})

	t.Run("QE+APE sends the post-editing schema, not MT's", func(t *testing.T) {
		srv, got := captureBody(t, false)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.PredictBatch(context.Background(), PredictConfig{
			Provider: ProviderOpenAI, Model: testOpenAIModel, APIKey: "sk", BaseURL: srv.URL,
			Mode: ModeQEAPE,
		}, []PredictInput{{ID: "1", Source: "a", Translation: "b"}}); err != nil {
			t.Fatal(err)
		}
		schema := (*got)["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
		props := schema["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		if _, ok := props["postedit"]; !ok {
			t.Errorf("an APE request must allow a postedit: %#v", props)
		}
		if _, ok := props["target"]; ok {
			t.Error("an APE request must not ask for MT's target field")
		}
	})
}
