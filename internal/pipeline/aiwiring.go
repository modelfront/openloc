package pipeline

import (
	"openloc/internal/ai"
	"openloc/internal/aiusage"
	"openloc/internal/llmdebug"
	"openloc/internal/secrets"
)

// ConfigureAI wires everything the pipeline needs to talk to AI providers: the
// LLM translator/predictor (AI-as-MT and LLM QE+APE), the decryptor for stored
// provider keys, the token ledger (ai_usage_events) and the llmdebug recorder. Returns the encryptor concretely
// (not as the Decryptor interface) so a caller can hand it straight to
// extension.NewService.
//
// The HTTP router and the worker both call this, so they wire identical AI
// dependencies — MT (run by a handler) and QE+APE (run by the worker) are
// captured by the same recorder.
//
// A bad secrets key leaves the AI provider unwired but STILL sets the
// recorder.
func (s *Service) ConfigureAI(secretsKey string, rec llmdebug.Recorder) (*secrets.Encryptor, error) {
	// Guarded so a typed nil can't land in the interface field — a non-nil
	// interface holding a nil pointer passes `!= nil` and defeats the rec==nil
	// fast path at every capture site.
	if rec != nil {
		s.SetLLMDebugRecorder(rec)
	}

	enc, err := secrets.NewEncryptor(secretsKey)
	if err != nil {
		return nil, err
	}

	llm := ai.NewHTTPTranslator().WithUsageRecorder(aiusage.NewStore(s.queries))
	if rec != nil {
		llm.WithRecorder(rec)
	}
	s.SetAIProvider(llm, llm, enc)
	return enc, nil
}
