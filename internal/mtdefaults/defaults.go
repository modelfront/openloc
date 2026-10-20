// Package mtdefaults gives a NEW org the MT engines this deployment supplies
// for free, so it can translate before anyone has entered a key.
//
// It runs once, at tenant creation. The rows are the org's from then on, so
// there is deliberately no sweep that
// revisits existing tenants.
//
// Separate from internal/mtengine because that package imports internal/auth
// for request scoping, and the auth package must call this on tenant creation —
// keeping it dependency-light (sqlcgen only) is what breaks the cycle.
package mtdefaults

import (
	"context"
	"fmt"
	"os"
	"strings"

	db "openloc/internal/database/sqlcgen"
)

// Engine types a deployment can supply without any per-tenant credential.
const (
	// TypeLibreTranslate is exported so callers can name the type without
	// re-typing the string.
	TypeLibreTranslate = "libretranslate"
	// TypeMock returns canned text. Provisioned OUTSIDE production only — an
	// engine that invents translations should not be one click from a live
	// project, and its whole purpose is testing the pipeline without a vendor.
	TypeMock = "mock"
)

type builtIn struct {
	engineType string
	name       string
	// url is nil when the engine needs no endpoint (mock). A non-nil pointer to
	// an empty string means "configured, but this deployment has not set it" —
	// which provisions nothing.
	url *string
}

// LibreTranslateURL is the single reader of LIBRETRANSLATE_URL on the server
// side (the seed reads the same variable for its own run). Deliberately NOT a
// config field: it is needed at startup and again whenever a tenant is created,
// and two readers of one variable can only ever drift apart.
//
// Blank means this deployment does not run LibreTranslate.
func LibreTranslateURL() string {
	return strings.TrimSpace(os.Getenv("LIBRETRANSLATE_URL"))
}

// environment reads ENV directly, for the same reason LibreTranslateURL does:
// this runs at startup AND on every tenant creation, from packages that hold no
// config, and threading it through three auth handlers only creates places for
// the two to disagree. Matches config's default.
func environment() string {
	if v := strings.TrimSpace(os.Getenv("ENV")); v != "" {
		return v
	}
	return "development"
}

// engines lists what THIS deployment supplies, given its environment. An engine
// absent from the list is never created, so the AI settings tile for it stays
// hidden — the page shows what exists, not what could.
func engines(environment string) []builtIn {
	var out []builtIn
	if url := LibreTranslateURL(); url != "" {
		out = append(out, builtIn{engineType: TypeLibreTranslate, name: "LibreTranslate", url: &url})
	}
	// Production never gets a mock: a translation that is silently fabricated is
	// worse than no translation, and nothing in production should be testing the
	// pipeline against canned text.
	if environment != "production" {
		out = append(out, builtIn{engineType: TypeMock, name: "Mock", url: nil})
	}
	return out
}

// Supplied is one engine type this deployment provides without a credential.
type Supplied struct {
	EngineType string
	Name       string
	// ApiUrl is nil for a type that needs no endpoint (mock). It is read from
	// the environment, never from a request — LibreTranslate's URL must never be
	// a field a user can set.
	ApiUrl *string
}

// Available reports what this deployment supplies right now, for callers that
// must offer only what they can actually build: the MT engine catalog, and the
// AI settings grid deciding which built-in tiles to render.
//
// Environment-dependent by design. A production deployment supplies no mock, so
// no surface can offer to create one — the whole point of keeping the list here
// rather than hardcoding it per page.
func Available() []Supplied {
	out := []Supplied{}
	for _, e := range engines(environment()) {
		out = append(out, Supplied{EngineType: e.engineType, Name: e.name, ApiUrl: e.url})
	}
	return out
}

// EnsureForTenant gives a NEW tenant the engines this deployment supplies.
//
// Once, at tenant creation, and never again. They are ordinary engines from
// that moment: an org may rename, switch off or delete one, and re-running this
// on every startup would undo a delete — the row would quietly come back and
// the user would be told nothing. A deployment that changes LIBRETRANSLATE_URL
// after a tenant exists therefore does not heal that tenant's row; that is the
// price of the row being the org's to keep.
func EnsureForTenant(ctx context.Context, q db.Querier, tenantID int64) error {
	for _, w := range engines(environment()) {
		if err := q.EnsureDeploymentMTEngine(ctx, db.EnsureDeploymentMTEngineParams{
			Name:       w.name,
			EngineType: w.engineType,
			ApiUrl:     w.url,
			TenantID:   tenantID,
		}); err != nil {
			return fmt.Errorf("ensure %s engine for tenant %d: %w", w.engineType, tenantID, err)
		}
	}
	return nil
}
