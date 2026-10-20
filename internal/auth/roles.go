package auth

import (
	"context"
	"fmt"
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// The four-role model:
//   - owner            : full super-admin (everything admin can + owner-only deletes)
//   - admin            : full operational power, minus owner-only deletes
//   - project_manager  : admin minus delete-project/TM/TB; own tasks only
//   - linguist         : restricted — Tasks + the CAT editor for jobs assigned to
//                        them. This is the role every vendor invite creates.
const (
	RoleOwner          = "owner"
	RoleAdmin          = "admin"
	RoleProjectManager = "project_manager"
	RoleLinguist       = "linguist"
)

func roleOf(r *http.Request) string { return RoleFromContext(r.Context()) }

// IsAdmin reports owner OR admin (the full-access tier).
func IsAdmin(r *http.Request) bool {
	return IsAdminRole(roleOf(r))
}

// IsAdminRole reports whether role is owner or admin.
func IsAdminRole(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

// IsManager reports owner, admin, OR project_manager — the roles that may manage
// projects, TMs, terminology, customers, vendors, templates, engines, QA.
func IsManager(r *http.Request) bool {
	role := roleOf(r)
	return role == RoleOwner || role == RoleAdmin || role == RoleProjectManager
}

// IsManagerRole is IsManager for a role string already pulled off the context.
func IsManagerRole(role string) bool {
	return role == RoleOwner || role == RoleAdmin || role == RoleProjectManager
}

// roleRank orders the four roles by how much each may do.
var roleRank = map[string]int{RoleLinguist: 1, RoleProjectManager: 2, RoleAdmin: 3, RoleOwner: 4}

// RoleAtLeast reports whether role ranks the same as or above other. An
// unknown role on either side is never at least anything.
func RoleAtLeast(role, other string) bool {
	r, o := roleRank[role], roleRank[other]
	return r > 0 && o > 0 && r >= o
}

// RoleAbove reports whether role ranks strictly above other. An unknown other
// (someone no longer in the org) ranks below every known role.
func RoleAbove(role, other string) bool {
	r := roleRank[role]
	return r > 0 && r > roleRank[other]
}

// IsLinguist reports the restricted linguist role.
func IsLinguist(r *http.Request) bool { return roleOf(r) == RoleLinguist }

// RequireManager blocks non-managers (i.e. linguists) from a route group with
// 403. Since linguist is the only non-manager role, this is the single gate for
// the entire management / enumeration surface (project lists, TM/terminology/
// customer/template admin, invites, analytics). Linguist-permitted routes
// (Tasks, their assigned job's CAT editor, shared translator lookups) don't use it.
func RequireManager() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsManager(r) {
				httputil.Error(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// LinguistMayAccessJob is the linguist job gate without HTTP: non-linguists
// always pass; a linguist passes only when assigned to a step of the job.
// Shared by the REST middleware and the MCP backend, which has no middleware.
func LinguistMayAccessJob(ctx context.Context, queries db.Querier, jobID int64) (bool, error) {
	if RoleFromContext(ctx) != RoleLinguist {
		return true, nil
	}
	userID := UserIDFromContext(ctx)
	return queries.IsUserAssignedToJob(ctx, db.IsUserAssignedToJobParams{JobID: jobID, UserID: &userID})
}

// LinguistMayAccessFile is LinguistMayAccessJob for a file: a linguist passes
// when assigned to any job of the file. Only for what every language shares
// (source segments, the source download); translations go through
// LinguistMayAccessFileLanguage or LinguistMayAccessTranslation.
func LinguistMayAccessFile(ctx context.Context, queries db.Querier, fileID int64) (bool, error) {
	if RoleFromContext(ctx) != RoleLinguist {
		return true, nil
	}
	userID := UserIDFromContext(ctx)
	return queries.IsUserAssignedToFile(ctx, db.IsUserAssignedToFileParams{FileID: fileID, UserID: &userID})
}

// LinguistMayAccessFileLanguage is the linguist gate for one language of a
// file: a linguist passes only when assigned to the file's job in lang.
// Non-linguists always pass.
func LinguistMayAccessFileLanguage(ctx context.Context, queries db.Querier, fileID int64, lang string) (bool, error) {
	if RoleFromContext(ctx) != RoleLinguist {
		return true, nil
	}
	userID := UserIDFromContext(ctx)
	return queries.IsUserAssignedToFileLanguage(ctx, db.IsUserAssignedToFileLanguageParams{
		FileID: fileID, TargetLanguage: lang, UserID: &userID,
	})
}

// LinguistMayAccessTranslation is LinguistMayAccessFileLanguage for the file
// and target language of one translation.
func LinguistMayAccessTranslation(ctx context.Context, queries db.Querier, translationID int64) (bool, error) {
	if RoleFromContext(ctx) != RoleLinguist {
		return true, nil
	}
	userID := UserIDFromContext(ctx)
	return queries.IsUserAssignedToTranslation(ctx, db.IsUserAssignedToTranslationParams{
		TranslationID: translationID, UserID: &userID,
	})
}

// LinguistFileLanguageAccess applies LinguistMayAccessFileLanguage to a
// request. On refusal or lookup failure it writes the response itself and
// returns false, so the caller just returns.
func LinguistFileLanguageAccess(w http.ResponseWriter, r *http.Request, queries db.Querier, fileID int64, lang string) bool {
	assigned, err := LinguistMayAccessFileLanguage(r.Context(), queries, fileID, lang)
	if err != nil {
		httputil.ServiceError(w, fmt.Errorf("verify language assignment: %w", err), "verify", "language assignment")
		return false
	}
	if !assigned {
		httputil.Error(w, http.StatusForbidden, "this language is not assigned to you")
		return false
	}
	return true
}

// RequireLinguistJobAccess restricts LINGUISTS to jobs they're assigned to; all
// other roles pass through (their tenant scoping still applies via
// RequireJobOwnership, which must run before this). Apply inside the
// /api/jobs/{jobID} group so a linguist can only touch their own assigned job.
func RequireLinguistJobAccess(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsLinguist(r) {
				next.ServeHTTP(w, r)
				return
			}
			jobID, err := httputil.ParseID(r, "jobID")
			if err != nil {
				httputil.BadRequest(w, "invalid job ID")
				return
			}
			if !LinguistJobAccess(w, r, queries, jobID) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// LinguistJobAccess applies the linguist job gate to a job id that did not
// come from the URL (a body field, say). Non-linguists always pass. For a
// linguist it requires an assignment to the job; on refusal or lookup failure
// it writes the response itself and returns false, so the caller just returns.
func LinguistJobAccess(w http.ResponseWriter, r *http.Request, queries db.Querier, jobID int64) bool {
	if !IsLinguist(r) {
		return true
	}
	assigned, err := LinguistMayAccessJob(r.Context(), queries, jobID)
	if err != nil {
		httputil.ServerError(w, "verify", "job assignment")
		return false
	}
	if !assigned {
		httputil.Error(w, http.StatusForbidden, "job is not assigned to you")
		return false
	}
	return true
}

// RequireLinguistTranslationAccess restricts LINGUISTS to translations of a
// job they're assigned to (the translation's file AND target language); other
// roles pass through. Apply to every {translationID} route.
func RequireLinguistTranslationAccess(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsLinguist(r) {
				next.ServeHTTP(w, r)
				return
			}
			translationID, err := httputil.ParseID(r, "translationID")
			if err != nil {
				httputil.BadRequest(w, "invalid translation ID")
				return
			}
			assigned, err := LinguistMayAccessTranslation(r.Context(), queries, translationID)
			if err != nil {
				httputil.ServerError(w, "verify", "translation assignment")
				return
			}
			if !assigned {
				httputil.Error(w, http.StatusForbidden, "translation is not part of your assigned work")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireLinguistFileAccess restricts LINGUISTS to files that have a job assigned
// to them (they need the file's source segments to translate); all other roles
// pass through. Apply inside the /api/files/{fileID} group, after
// RequireFileOwnership. It passes on any job of the file, so a route under it
// that reads or writes one language adds the language gate.
func RequireLinguistFileAccess(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsLinguist(r) {
				next.ServeHTTP(w, r)
				return
			}
			fileID, err := httputil.ParseID(r, "fileID")
			if err != nil {
				httputil.BadRequest(w, "invalid file ID")
				return
			}
			assigned, err := LinguistMayAccessFile(r.Context(), queries, fileID)
			if err != nil {
				httputil.ServerError(w, "verify", "file assignment")
				return
			}
			if !assigned {
				httputil.Error(w, http.StatusForbidden, "file is not assigned to you")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
