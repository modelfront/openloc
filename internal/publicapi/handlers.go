// Package publicapi serves the customer API under /api/v1.
//
// v1 is a stable contract built on the services the app already uses. A v1
// handler reads its own request shape, calls the shared service, and maps the
// result to its own response type (dto.go), so a database change never leaks
// into the contract. Routes with no JSON response — deletes and file
// downloads — mount the app's handler directly.
//
// Every route carries the same role and ownership gates as the app route it
// mirrors; the parity test pins that. The spec in openapi/ documents every
// route, and the spec tests check routes and responses against it.
package publicapi

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	"openloc/internal/aiprompt"
	"openloc/internal/comments"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/invite"
	"openloc/internal/job"
	"openloc/internal/project"
	tmpl "openloc/internal/template"
	"openloc/internal/terminology"
	"openloc/internal/translation"
	"openloc/internal/translationmemory"
)

// Handlers are the app's handler instances the v1 routes delegate to. They
// are the same instances the app routes use, so both run with the same
// enqueuers and notifiers.
type Handlers struct {
	Queries  db.Querier
	Pool     *pgxpool.Pool
	Spec     *Spec
	Projects *project.Handler
	Files    *file.Handler
	Jobs     *job.Handler
	// Translations backs the segment routes; Comments the segment comments.
	Translations *translation.Handler
	Comments     *comments.Service
	TMs          *translationmemory.Handler
	Glossaries   *terminology.Handler
	Invites      *invite.Service
	Templates    *tmpl.Handler
	AIPrompts    *aiprompt.Handler
	// AICatalog gates which models an AI engine may use.
	AICatalog *ai.Catalog
}
