package chat

// SavedTourLengths maps each playbook with a saved tour to its step count, so
// external tests can tell a complete tour from one with dropped steps.
func SavedTourLengths() map[string]int {
	out := map[string]int{}
	for _, pb := range playbooks {
		if len(pb.Tour) > 0 {
			out[pb.Name] = len(pb.Tour)
		}
	}
	return out
}

// NewOpenAIResponses is the OpenAI adapter on the Responses API at baseURL,
// which NewOpenAI picks only for api.openai.com and Azure.
func NewOpenAIResponses(apiKey, baseURL, model string) LLM {
	l := NewOpenAI(apiKey, baseURL, model).(*openaiLLM)
	l.responses = true
	return l
}
