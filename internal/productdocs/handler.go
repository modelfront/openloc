package productdocs

import (
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"openloc/internal/auth"
	"openloc/internal/httputil"
)

// Handler serves a documentation set. Pages are filtered per role inside the
// index, so the route group must NOT add RequireManager — a linguist reading
// the CAT editor guide is the point. Mounted without a session (the public
// help), the caller is RoleAnonymous and sees only the pages every role sees.
type Handler struct{ idx *Index }

func NewHandler(idx *Index) *Handler { return &Handler{idx: idx} }

// readerRole is the caller's role, or RoleAnonymous without a session.
func readerRole(r *http.Request) string {
	if c, ok := auth.ClaimsFromContext(r.Context()); ok && c != nil {
		return c.Role
	}
	return RoleAnonymous
}

// pageSlugRe is section/stem — exactly two kebab-case segments.
var pageSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*/[a-z0-9]+(-[a-z0-9]+)*$`)

// HandleTree returns the navigation tree the caller's role may see.
func (h *Handler) HandleTree(w http.ResponseWriter, r *http.Request) {
	httputil.Data(w, h.idx.TreeFor(readerRole(r)))
}

// HandlePage returns one page by slug. Unknown slugs and slugs the caller's
// role may not read both 404 — existence must not leak across roles.
func (h *Handler) HandlePage(w http.ResponseWriter, r *http.Request) {
	slug := strings.Trim(chi.URLParam(r, "*"), "/")
	if !pageSlugRe.MatchString(slug) {
		httputil.NotFound(w, "page")
		return
	}
	page, ok := h.idx.PageFor(slug, readerRole(r))
	if !ok {
		httputil.NotFound(w, "page")
		return
	}
	httputil.Data(w, page)
}

// maxQueryLen bounds the search string; anything longer is not a search.
const maxQueryLen = 200

// HandleSearch ranks pages against ?q=.
func (h *Handler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httputil.BadRequest(w, "q is required")
		return
	}
	if len(q) > maxQueryLen {
		httputil.BadRequest(w, "q is too long")
		return
	}
	httputil.Data(w, h.idx.Search(q, readerRole(r)))
}

// HandleAsset serves an embedded image. The name is a bare file name (assets
// are a flat directory); anything with a slash or that fails the pattern 404s,
// which also closes path traversal. Responses carry an ETag so the browser
// revalidates cheaply and a re-shot screenshot with the same name shows up
// after deploy.
func (h *Handler) HandleAsset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "*")
	if name != path.Base(name) || !assetRe.MatchString(name) {
		httputil.NotFound(w, "asset")
		return
	}
	a, ok := h.idx.Asset(name)
	if !ok {
		httputil.NotFound(w, "asset")
		return
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == a.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("ETag", a.ETag)
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(a.Data)
	}
}
