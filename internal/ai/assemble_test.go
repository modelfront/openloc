package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// The preview is only worth anything if it is byte-identical to what a run
// sends. These assert against the runtime builders directly, so a change to
// either suffix that the preview doesn't pick up fails here rather than in
// production.
func TestAssemblePrompt_MatchesRuntimeSystemPrompt(t *testing.T) {
	const text = "You are a strict reviewer."

	tests := []struct {
		mode string
		want string
	}{
		{ModeMT, systemPrompt(TranslateConfig{PromptText: text, SourceLang: "en", TargetLang: "de"})},
		{ModeQE, predictSystemPrompt(PredictConfig{PromptText: text, SourceLang: "en", TargetLang: "de", Mode: ModeQE})},
		{ModeAPE, predictSystemPrompt(PredictConfig{PromptText: text, SourceLang: "en", TargetLang: "de", Mode: ModeAPE})},
		{ModeQEAPE, predictSystemPrompt(PredictConfig{PromptText: text, SourceLang: "en", TargetLang: "de", Mode: ModeQEAPE})},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			got := AssemblePrompt(tt.mode, text, "en", "de", "")
			if got.SystemPrompt != tt.want {
				t.Errorf("system prompt differs from the runtime's\n got: %q\nwant: %q", got.SystemPrompt, tt.want)
			}
			if got.Instruction+got.Appended != got.SystemPrompt {
				t.Errorf("Instruction + Appended must equal SystemPrompt")
			}
		})
	}
}

// QE must not be told it may post-edit, and MT must not be given the QE
// contract — the halves are what the preview shows the author, so a mode
// mix-up here is a mix-up on screen.
func TestAssemblePrompt_ModeSelectsContract(t *testing.T) {
	// noSchema takes no structured output, so the prose contract is what it
	// gets — and the prose is the only half a mode mix-up could corrupt.
	const noSchema = "gpt-4-turbo"
	qe := AssemblePrompt(ModeQE, "x", "en", "de", noSchema).Appended
	if strings.Contains(qe, `"postedit"`) {
		t.Errorf("QE contract must not offer a postedit field:\n%s", qe)
	}
	ape := AssemblePrompt(ModeAPE, "x", "en", "de", noSchema).Appended
	if !strings.Contains(ape, `"postedit"`) {
		t.Errorf("APE contract must offer a postedit field:\n%s", ape)
	}
	mt := AssemblePrompt(ModeMT, "x", "en", "de", noSchema).Appended
	if !strings.Contains(mt, `"target"`) || strings.Contains(mt, `"approved"`) {
		t.Errorf("MT contract must ask for a target and not a verdict:\n%s", mt)
	}
}

// An empty draft still previews: the author sees the stand-in the runtime would
// use rather than a prompt that starts with the language line.
func TestAssemblePrompt_EmptyTextUsesBuiltInInstruction(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQEAPE} {
		got := AssemblePrompt(mode, "   ", "en", "de", "")
		if got.Instruction == "" {
			t.Errorf("%s: expected a built-in instruction for empty prompt text", mode)
		}
		if strings.HasPrefix(got.SystemPrompt, "\n") {
			t.Errorf("%s: system prompt must not start with the appended half", mode)
		}
	}
}

// An unknown mode must not produce an MT prompt for a QE engine — defaulting
// the other way would silently ask a reviewer engine to translate.
func TestAssemblePrompt_UnknownModeFallsBackToQEAPE(t *testing.T) {
	got := AssemblePrompt("nonsense", "x", "en", "de", "")
	want := AssemblePrompt(ModeQEAPE, "x", "en", "de", "")
	if got.SystemPrompt != want.SystemPrompt {
		t.Errorf("unknown mode should assemble as QE+APE\n got: %q\nwant: %q", got.SystemPrompt, want.SystemPrompt)
	}
}

// The web editor fetches the appended half keyed by output mode alone, and
// never refetches while the author types. That is only safe while the appended
// half ignores the instruction text — so it is pinned here rather than left as
// an assumption in a React component.
func TestAssemblePrompt_AppendedIgnoresInstruction(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		a := AssemblePrompt(mode, "one instruction", "en", "de", "").Appended
		b := AssemblePrompt(mode, "a completely different instruction", "en", "de", "").Appended
		if a != b {
			t.Errorf("%s: appended half must not depend on the instruction text\n a: %q\n b: %q", mode, a, b)
		}
	}
}

// A model the provider will hold to a schema is not told the shape twice: the
// suffix drops the JSON example, the field docs and the no-markdown plea, all
// of which ResultsSchema already guarantees.
func TestAssemblePrompt_SchemaModelDropsTheRedundantHalf(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		t.Run(mode, func(t *testing.T) {
			long := AssemblePrompt(mode, "x", "en", "de", "gpt-4-turbo").Appended
			short := AssemblePrompt(mode, "x", "en", "de", "gpt-4o").Appended

			if !strings.Contains(long, `{"results"`) {
				t.Errorf("a model with no schema must still be told the shape:\n%s", long)
			}
			if strings.Contains(short, `{"results"`) {
				t.Errorf("a schema-enforced model must not be told the shape twice:\n%s", short)
			}
			if strings.Contains(short, "No markdown") {
				t.Errorf("the no-markdown plea is the schema's job now:\n%s", short)
			}
			if len(short) >= len(long) {
				t.Errorf("the enforced form should be shorter (%d vs %d)", len(short), len(long))
			}

			// What a schema cannot express survives either way: the reply must
			// carry one result per input segment with the id echoed back, which
			// is how every verdict is routed. The input itself is never
			// described — a segment arrives as JSON with named keys.
			for _, form := range []string{long, short} {
				if !strings.Contains(form, "one result per input segment") {
					t.Errorf("the one-result-per-segment rule must survive:\n%s", form)
				}
				if strings.Contains(form, "The user message") {
					t.Errorf("the input format must not be described:\n%s", form)
				}
			}
		})
	}
}

// With no model in hand — the prompt editor, previewing a prompt no engine is
// attached to — a schema is assumed. Printing the prose contract there would
// put it beside the schema that replaced it, and every current model would be
// shown a block it never receives. A model known to take no schema still gets
// the long form; TestAssemblePrompt_ModeSelectsContract covers that.
func TestAssemblePrompt_NoModelAssumesASchema(t *testing.T) {
	got := AssemblePrompt(ModeQEAPE, "x", "en", "de", "").Appended
	if strings.Contains(got, `{"results"`) {
		t.Errorf("no model given, so the prose contract must be left to the schema:\n%s", got)
	}
	// The pairing rule is not in the schema and stays in words either way.
	if !strings.Contains(got, "Return exactly one result") {
		t.Errorf("the id-alignment instruction must survive:\n%s", got)
	}
}

// A preview with no job in scope passes the markers in as the languages, and
// they come back out through the instruction's own tokens — which is what lets
// the editor show a prompt's shape before any job exists.
func TestAssemblePrompt_SubstitutesLanguagesVerbatim(t *testing.T) {
	got := AssemblePrompt(ModeQEAPE,
		"Review this <source_lang> to <target_lang> translation.",
		"<source_lang>", "<target_lang>", "")
	if !strings.Contains(got.Instruction, "<source_lang> to <target_lang>") {
		t.Errorf("expected the language markers verbatim:\n%s", got.Instruction)
	}
}

// The prompt editor offers <source_lang> and <target_lang> as insertable
// tokens and the starter templates are written around them, so they have to
// resolve. Sending the literal token is worse than saying nothing: the model
// reads "<target_lang>" as the name of the language.
func TestAssemblePrompt_SubstitutesLanguageTokens(t *testing.T) {
	got := AssemblePrompt(ModeQEAPE,
		"You are a <source_lang>→<target_lang> reviewer.", "en", "de", "")

	if strings.Contains(got.SystemPrompt, "<source_lang>") || strings.Contains(got.SystemPrompt, "<target_lang>") {
		t.Errorf("language tokens must be substituted, got:\n%s", got.SystemPrompt)
	}
	if !strings.Contains(got.Instruction, "English (en)→German (de) reviewer") {
		t.Errorf("instruction should read with the job's languages, got: %q", got.Instruction)
	}
}

// A code alone leaves the model to work out which variant of a language the job
// wants — the one thing a regional pair exists to pin down.
func TestAssemblePrompt_NamesRegionalVariants(t *testing.T) {
	got := AssemblePrompt(ModeMT,
		"Translate from <source_lang> to <target_lang>.", "en-gb", "de-at", "")

	const want = "Translate from British English (en-GB) to Austrian German (de-AT)."
	if !strings.Contains(got.Instruction, want) {
		t.Errorf("instruction should name both variants, got: %q", got.Instruction)
	}
}

// The platform never names the language pair — the instruction does,
// through the tokens. A prompt that drops them is a prompt that chose not to
// say, which is the author's call to make and to see.
func TestAssemblePrompt_AppendsNoLanguageLine(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		got := AssemblePrompt(mode, "You are a reviewer.", "en", "de", "").Appended
		if strings.Contains(got, "from en to de") || strings.Contains(got, "en to de") {
			t.Errorf("%s: the pair belongs to the instruction, not the suffix:\n%s", mode, got)
		}
	}
}

// The contract describes the content block, so it has to describe the block
// the segment will actually carry. Naming a field the prompt never sends tells
// the model to look for something that is not there — and, for tm_match, to
// treat a missing entry as meaningful.
func TestAssemblePrompt_ContractNamesNoOptionalContext(t *testing.T) {
	for _, chosen := range [][]string{nil, {"tm_match", "terms"}} {
		got := AssemblePromptWithContext(ModeQEAPE, "Review.", "en", "de", "", chosen).Appended
		for _, key := range []string{"tm_match", "terms", "note", "comments"} {
			if strings.Contains(got, key) {
				t.Errorf("context=%v: the contract is shape only, so %q must not be named:\n%s", chosen, key, got)
			}
		}
	}
}

func TestAssemblePrompt_FormatsPartitionRuntimeContract(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		for _, model := range []string{"", "gpt-4o"} {
			t.Run(mode+model, func(t *testing.T) {
				got := AssemblePromptWithContext(mode, "Review.", "en", "de-de", model, []string{"tm_match"})
				// The appended half is the output format and nothing else: the
				// input needs no description, since a segment arrives as JSON
				// with named keys. Asserted against the whole system prompt
				// rather than against OutputFormat — both fields are written
				// from one expression, so comparing them proves nothing.
				if got.SystemPrompt != got.Instruction+got.OutputFormat {
					t.Fatalf("the system prompt must be the instruction plus the output format alone:\n system:      %q\n instruction: %q\n output:      %q",
						got.SystemPrompt, got.Instruction, got.OutputFormat)
				}
				if !strings.HasPrefix(got.OutputFormat, "\n\nReturn exactly one result") {
					t.Fatalf("wrong output section: %s", got.OutputFormat)
				}
				if strings.Contains(got.Appended, "The user message") || strings.Contains(got.Appended, `"segments"`) {
					t.Fatalf("the input format must not be described at all:\n%s", got.Appended)
				}
			})
		}
	}
}

func TestPreviewContextMatchesSelectedFields(t *testing.T) {
	for _, mode := range []string{ModeMT, ModeQE, ModeAPE, ModeQEAPE} {
		for _, keys := range [][]string{nil, {"tm_match", "terms"}} {
			raw, err := PreviewContext(mode, keys)
			if err != nil {
				t.Fatal(err)
			}
			// A flat array of segments: the id sits beside the context keys.
			var body []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || string(body[0]["id"]) != `"1"` {
				t.Fatalf("wrong request shape: %s", raw)
			}
			content := body[0]
			if len(content["source"]) == 0 {
				t.Fatal("missing source")
			}
			if (len(content["translation"]) > 0) != (mode != ModeMT) {
				t.Fatalf("wrong target context for %s", mode)
			}
			for _, key := range []string{"tm_matches", "terms"} {
				if (len(content[key]) > 0) != (len(keys) > 0) {
					t.Fatalf("wrong optional context: %s", raw)
				}
			}
			if len(keys) > 0 {
				var matches []map[string]any
				if err := json.Unmarshal(content["tm_matches"], &matches); err != nil || len(matches) != 1 {
					t.Fatalf("tm_matches should be a one-item list: %s", raw)
				}
				if _, ok := matches[0]["score"]; !ok {
					t.Fatalf("the preview match should show its score: %s", raw)
				}
			}
		}
	}
}
