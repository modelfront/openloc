package mt

import (
	"context"
	"fmt"
	"strings"
)

// Mock is a fake MT engine for development and testing.
// It prefixes each text with the target language code.
type Mock struct{}

func (m Mock) Name() string { return "mock" }

func (m Mock) Translate(_ context.Context, req TranslateRequest) ([]TranslateResult, error) {
	results := make([]TranslateResult, len(req.Texts))
	prefix := fmt.Sprintf("[%s] ", strings.ToUpper(req.TargetLang))
	for i, text := range req.Texts {
		results[i] = TranslateResult{Text: prefix + text}
	}
	return results, nil
}
