// Package stepdef is the step-definitions library: the
// only place a workflow step is created. A definition carries a step's
// identity — name, step_type, role gate, optional AI router — and library
// workflows / project profiles compose or snapshot it.
//
// Access scope: list is open to every manager (owner/admin/project_manager);
// create/update/delete are manager-gated too. Linguists never reach this
// surface (App.tsx hides it; api.go wraps the group in RequireManager).
package stepdef

import (
	"context"
	"fmt"
	"strings"

	"openloc/internal/airouter"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/systemworkflow"
)

// Step types. `hybrid` and `ai` both run the QE+APE engine their AI router
// picks for the job when the step starts; only `ai` completes itself afterwards.
// `pretranslate` belongs to the built-in Pre-translation step only: the
// pre-translation pipeline is its work, and it completes itself when that
// finishes. The step library never offers it (ValidType).
const (
	TypeHuman        = "human"
	TypeHybrid       = "hybrid"
	TypeAI           = "ai"
	TypePretranslate = systemworkflow.StepType
)

// DefaultRole is written to every definition's role_required column. The
// column is a leftover from the per-step role gate; completion is gated by
// assignment or manager rank (workflow.CanActOnStep), not by this value.
const DefaultRole = auth.RoleLinguist

// maxNameLen caps definition names defensively (UI spillover, not a DB limit).
const maxNameLen = 200

// DefaultNames are the two step definitions every tenant starts with. They
// back the default project workflow (Translation → Review) and are matched by
// name, case-insensitively, when a legacy step row is linked to the library.
var DefaultNames = []string{"Translation", "Review"}

// ValidType reports whether t is one of the three step types.
func ValidType(t string) bool {
	return t == TypeHuman || t == TypeHybrid || t == TypeAI
}

// RunsAI reports whether a step of this type runs its AI router on start.
func RunsAI(t string) bool { return t == TypeHybrid || t == TypeAI }

// IsAutomated reports whether a step of this type completes itself, so nobody
// is assigned to it and it carries no deadline.
func IsAutomated(t string) bool { return t == TypeAI || t == TypePretranslate }

// Input is the writable shape of a definition (create + update).
type Input struct {
	Name       string `json:"name"`
	StepType   string `json:"step_type"`
	AIRouterID *int64 `json:"ai_router_id"`
}

// Validate normalizes in and returns a client-safe message when it is not a
// legal definition: empty/long name, unknown type, a human step carrying a
// router, an AI-running step without one, or a router that
// airouter.ValidateStepRouter rejects (not this tenant's). An empty message means valid.
func Validate(ctx context.Context, q db.Querier, tenantID int64, in *Input) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "name is required"
	}
	if len(in.Name) > maxNameLen {
		return fmt.Sprintf("name too long (max %d characters)", maxNameLen)
	}
	if !ValidType(in.StepType) {
		return "step_type must be human, hybrid or ai"
	}
	switch {
	case in.StepType == TypeHuman && in.AIRouterID != nil:
		return "a human step cannot have an AI router"
	case RunsAI(in.StepType) && in.AIRouterID == nil:
		return fmt.Sprintf("a %s step needs an AI router", in.StepType)
	}
	if msg := airouter.ValidateStepRouter(ctx, q, tenantID, in.AIRouterID); msg != "" {
		return msg
	}
	return ""
}

// EnsureDefaults creates the tenant's default definitions (DefaultNames) when
// missing. Idempotent — the partial unique index is the conflict target — so
// it is safe to call on every project create.
func EnsureDefaults(ctx context.Context, q db.Querier, tenantID int64) error {
	for _, name := range DefaultNames {
		if err := q.EnsureStepDefinition(ctx, db.EnsureStepDefinitionParams{TenantID: tenantID, Name: name}); err != nil {
			return fmt.Errorf("ensure step definition %q: %w", name, err)
		}
	}
	return nil
}

// MaxWorkflowSteps caps how many steps a workflow (library, template or
// project) can have. Kept single-digit so the editor's row-number gutter never
// needs more than one digit + a dot.
const MaxWorkflowSteps = 9
