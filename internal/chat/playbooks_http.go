package chat

import (
	"net/http"
	"slices"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// playbookView is a playbook as the UI lists it. Steps are the saved tour,
// validated and with routes built — the browser runs them without the model.
// A playbook without a saved tour is started by asking the assistant.
type playbookView struct {
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Steps       []TourStep `json:"steps"`
	// HasTour is true for a playbook with a saved tour, even when this org has
	// nothing for its steps: the Playground always has.
	HasTour bool `json:"has_tour"`
}

// HandleListPlaybooks is GET /api/chat/playbooks: the playbooks the caller's
// role may use. Any member; the list is filtered by role.
func (h *Handler) HandleListPlaybooks(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if playbooksErr != nil {
		httputil.ServerError(w, "load", "playbooks")
		return
	}
	pick := tenantPicker(r.Context(), h.q, claims)
	out := make([]playbookView, 0, len(playbooks))
	for _, pb := range playbooks {
		// A saved tour runs only in the Playground; with it off, its playbook is not offered.
		if !slices.Contains(pb.Roles, claims.Role) || !pb.offered(h.issueReports) || (len(pb.Tour) > 0 && !h.playground) {
			continue
		}
		v := playbookView{Name: pb.Name, Title: pb.Title, Description: pb.Description, Steps: []TourStep{}, HasTour: len(pb.Tour) > 0}
		if len(pb.Tour) > 0 {
			raws, err := playbookTourArgs(pb.Name, pick, claims.Demo)
			if err != nil {
				logging.Error(r.Context(), err, "resolving playbook tour picks failed", "chat-playbook-tour-picks-failed", logging.Label("playbook", pb.Name))
				httputil.ServerError(w, "load", "playbooks")
				return
			}
			if len(raws) > 0 {
				if v.Steps, err = buildTourSteps(raws, claims.Demo); err != nil {
					// Pinned impossible by TestPlaybooksAreValid; list it as guided.
					logging.Warn(r.Context(), err, "playbook tour is invalid", "chat-playbook-tour-invalid", logging.Label("playbook", pb.Name))
					v.Steps = []TourStep{}
				}
			}
		}
		out = append(out, v)
	}
	httputil.Data(w, out)
}

// SetPlayground turns the Playground on. Saved tours run only there, so off
// (the default) their playbooks are not listed and the assistant starts none.
func (h *Handler) SetPlayground(on bool) {
	h.playground = on
}

// SetIssueReports tells the assistant the deployment takes issue reports, so
// the read-only prompt names reporting as the one thing it may still do, and
// the playbook that writes a report is offered.
func (h *Handler) SetIssueReports(on bool) {
	h.issueReports = on
}
