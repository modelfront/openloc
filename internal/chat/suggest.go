// Chart-prompt suggestions for the dashboard prompt cards.
// Two tiers, decided server-side because only the server
// can see which charts exist and whether an AI key is connected:
//
//  1. ai — one-shot LLM call with the tenant's visible charts as context,
//     asked for ONE chart that isn't there yet.
//  2. none — no key connected (or the call failed): the FE shows a greyed,
//     non-sendable hint and the user types from scratch.
//
// There is no static pool: a fixed list cannot know what a tenant already has.
// A suggestion names no time window either — the dashboard's date-range
// control owns it.

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/metrics"
)

// fallbackHint is the greyed, non-sendable placeholder shown when no AI key is
// connected. It is an EXAMPLE of what to type, not a prompt to send — hence no
// window in it, since the dashboard controls own that.
const fallbackHint = "Create a chart of words processed by target language"

type suggestResponse struct {
	// Source: "ai" | "none".
	Source string `json:"source"`
	// Prompt is the ready-to-send text. Empty for none.
	Prompt string `json:"prompt,omitempty"`
	// Label is the short human line the CARD displays. Empty for none.
	Label string `json:"label,omitempty"`
	// Hint is the greyed placeholder for none.
	Hint string `json:"hint,omitempty"`
}

// existingChart is one chart the model is told about, so it suggests something
// that isn't there rather than a near-duplicate.
type existingChart struct {
	Title   string   `json:"title"`
	Measure string   `json:"measure"`
	Dims    []string `json:"grouped_by,omitempty"`
	SplitBy string   `json:"split_by,omitempty"`
}

// HandleSuggestChart returns the prompt for a dashboard prompt card.
// Optional ?dashboard_id scopes the phrasing (add-to-this-dashboard vs
// create-a-dashboard); the chart context is everything the caller can see, so
// the suggestion stays new no matter which dashboard the charts were saved to.
func (h *Handler) HandleSuggestChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())

	// Dashboard name for the phrasing, when the card sits inside one.
	dashboardName := ""
	if idStr := r.URL.Query().Get("dashboard_id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			httputil.BadRequest(w, "invalid dashboard_id")
			return
		}
		// UserID is required, not optional: without it the predicate matches
		// only shared dashboards, and the prompt card inside a personal one
		// would 404.
		d, err := h.q.GetDashboardTenant(r.Context(), db.GetDashboardTenantParams{
			ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
		})
		if httputil.DBError(w, err, "dashboard") {
			return
		}
		dashboardName = d.Name
	}

	// ?shared=1 from the Organization dashboards tab: the suggestion should land
	// org-wide rather than in the caller's own space. Only offered to those who may actually
	// create one, so the prompt never asks for something the tool will refuse.
	wantShared := r.URL.Query().Get("shared") == "1" && auth.IsAdmin(r)

	rows, err := h.q.ListVisibleChartDefinitions(r.Context(), db.ListVisibleChartDefinitionsParams{
		TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "loading chart definitions failed", "chat-suggest-defs-failed")
		httputil.ServerError(w, "load", "chart suggestions")
		return
	}
	existing := make([]existingChart, 0, len(rows))
	for _, row := range rows {
		var def struct {
			Measure    string   `json:"measure"`
			Dimensions []string `json:"dimensions"`
		}
		if err := json.Unmarshal(row.Definition, &def); err != nil {
			continue // corrupt row: doesn't block suggestions
		}
		var viz struct {
			SeriesKey string `json:"series_key"`
		}
		_ = json.Unmarshal(row.Viz, &viz) // viz is advisory context only
		existing = append(existing, existingChart{
			Title: row.Title, Measure: def.Measure, Dims: def.Dimensions, SplitBy: viz.SeriesKey,
		})
	}

	if prompt := h.aiSuggestion(r, dashboardName, existing, wantShared); prompt != "" {
		// The prompt is already one human sentence — the label IS the prompt.
		httputil.Data(w, suggestResponse{Source: "ai", Prompt: prompt, Label: prompt})
		return
	}
	httputil.Data(w, suggestResponse{Source: "none", Hint: fallbackHint})
}

// aiSuggestTimeout bounds the one-shot suggestion call — a prompt card is
// not worth a slow page.
const aiSuggestTimeout = 10 * time.Second

func (h *Handler) aiSuggestion(r *http.Request, dashboardName string, existing []existingChart, wantShared bool) string {
	apiKey, baseURL, err := h.vendorKey(r, providerForModel(DefaultModel))
	if err != nil {
		return "" // no key connected → tier 2 (greyed hint)
	}
	ctx, cancel := context.WithTimeout(r.Context(), aiSuggestTimeout)
	defer cancel()

	// The vocabulary comes from the registry, not a copy: a measure added or
	// renamed there must not leave this prompt suggesting something the
	// compiler will reject.
	defs := metrics.Definitions()
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	slices.Sort(names) // stable prompt → prompt-cache friendly
	var vocab strings.Builder
	for _, name := range names {
		fmt.Fprintf(&vocab, "- %s: %s\n  group or split by: %s\n",
			name, defs[name], strings.Join(metrics.DimensionsFor(name), ", "))
	}

	target := "an appropriately named new dashboard"
	if dashboardName != "" {
		target = fmt.Sprintf("the %q dashboard", dashboardName)
	}
	if wantShared {
		target += ", shared with the whole organization"
	}

	sys := "You suggest ONE chart for a translation-management analytics dashboard.\n\n" +
		"Measures, with what each means and what it can be grouped or split by:\n" + vocab.String() +
		"\nDay, week and month are available on every measure as time groupings.\n\n" +
		"Capabilities worth using:\n" +
		"- Splitting one number by a category draws a stacked band per value — e.g. words split by kind " +
		"(tm/mt/ape/human) shows how much of the work the automation did.\n" +
		"- Volume measures report words, segments AND characters, so a chart can be read in any of them.\n" +
		"- A dashboard carries its own date-range, unit and granularity controls, and filters for " +
		"language, customer, project and user.\n\n" +
		"Rules:\n" +
		"1. Reply with EXACTLY one imperative sentence the user can send to an assistant. No preamble, " +
		"no quotes, no markdown, no explanation.\n" +
		"2. NEVER mention a time window or interval ('last 90 days', 'per week'). The dashboard's own " +
		"controls set those, so a suggestion naming one is wrong the moment someone changes it.\n" +
		"3. It must answer a question none of the existing charts answers — a different measure, a " +
		"different breakdown, or a split the others don't have. Not a rewording.\n" +
		"4. Say where it goes: " + target + ".\n" +
		"5. Ask to query the metric first, then save the chart, so the user sees the data before it is kept."

	user := "There are no charts yet."
	if len(existing) > 0 {
		b, err := json.Marshal(existing)
		if err == nil {
			user = "Charts that already exist (do not repeat these):\n" + string(b)
		}
	}

	llm := h.newLLM(providerForModel(DefaultModel), apiKey, baseURL, DefaultModel)
	turn, err := llm.Stream(ctx, Request{
		System:    sys,
		Messages:  []Message{{Role: "user", Text: user}},
		MaxTokens: 200,
	}, func(string) {})
	if err != nil {
		logging.Warn(r.Context(), err, "chart suggestion LLM call failed", "chat-suggest-llm-failed")
		return ""
	}
	return sanitizeSuggestion(turn.Text)
}

// sanitizeSuggestion keeps the first line and strips the wrapping a model adds
// anyway. An unusable reply returns "" and the card falls back to the hint —
// a prompt card is never worth surfacing an error for.
func sanitizeSuggestion(raw string) string {
	prompt := strings.TrimSpace(strings.SplitN(strings.TrimSpace(raw), "\n", 2)[0])
	prompt = strings.Trim(prompt, "\"'` ")
	// Rule 2 is the one a model most often ignores, and a suggestion naming a
	// window contradicts the dashboard's own control — drop it rather than show
	// advice we just told it not to give.
	if windowMention.MatchString(prompt) {
		return ""
	}
	if len(prompt) < 15 || len(prompt) > 400 {
		return ""
	}
	return prompt
}

// windowMention catches the phrasings that bake a window into the suggestion.
var windowMention = regexp.MustCompile(`(?i)\b(last\s+\d+\s+(day|week|month)s?|per\s+(day|week|month)|daily|weekly|monthly)\b`)
