package oauthserver

import (
	"html/template"
	"net/http"

	"openloc/internal/logging"
)

// The pages a user meets mid-connection, rendered by the server: the consent
// screen and the error page. Both share one frame — the OpenLoc favicon, mark
// and wordmark, and the app's colours in light and dark — so the user can tell
// they are on OpenLoc and not on the tool that sent them. No JavaScript and no
// external assets: the favicons come from the app's own static files.

const pageHead = `{{define "head"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<meta name="color-scheme" content="light dark">
<title>{{.}} — OpenLoc</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/svg+xml" href="/favicon-dark.svg" media="(prefers-color-scheme: dark)">
<style>
  :root { --bg: #f7f7f8; --card: #fff; --border: #e5e5e7; --text: #171719; --muted: #6b6b70;
          --soft: #f4f4f5; --field: #d4d4d8; --primary: oklch(0.546 0.245 264); --on-primary: #fff;
          --note-bg: #fffbeb; --note-border: #fde68a; --note-text: #3f3f46; }
  @media (prefers-color-scheme: dark) {
    :root { --bg: #0f0f10; --card: #1c1c1e; --border: #2e2e32; --text: #fafafa; --muted: #a1a1aa;
            --soft: #26262a; --field: #3f3f46; --primary: oklch(0.623 0.214 259.815);
            --note-bg: #2a2410; --note-border: #5c4a12; --note-text: #e4e4e7; }
  }
  body { font-family: Inter, -apple-system, system-ui, "Helvetica Neue", sans-serif; background: var(--bg);
         color: var(--text); margin: 0; padding: 16px; box-sizing: border-box; display: flex; min-height: 100vh;
         align-items: center; justify-content: center; }
  .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 32px;
          max-width: 420px; width: 100%; box-sizing: border-box; box-shadow: 0 1px 3px rgba(0,0,0,.06); }
  .brand { display: flex; align-items: center; gap: 10px; margin: 0 0 24px; }
  .brand svg { width: 28px; height: 28px; flex: none; }
  .brand .disc { fill: var(--text); } .brand .glyph { fill: var(--card); }
  .wordmark { font-family: "Source Serif 4", Georgia, "Times New Roman", serif; font-size: 22px;
              font-weight: 500; letter-spacing: -0.01em; }
  h1 { font-size: 18px; font-weight: 600; margin: 0 0 4px; }
  .sub { color: var(--muted); font-size: 14px; margin: 0 0 12px; }
  .dest { color: var(--note-text); font-size: 13px; background: var(--note-bg); border: 1px solid var(--note-border);
          border-radius: 8px; padding: 8px 10px; margin: 0 0 16px; }
  .dest strong { word-break: break-all; }
  .scope { background: var(--soft); border-radius: 8px; padding: 12px 14px; font-size: 14px; margin: 0 0 16px; }
  .scope strong { display: block; margin-bottom: 4px; }
  label { display: block; font-size: 13px; color: var(--muted); margin: 0 0 6px; }
  select { width: 100%; padding: 8px 10px; font-size: 14px; border: 1px solid var(--field); color: var(--text);
           border-radius: 8px; margin-bottom: 20px; background: var(--card); }
  .row { display: flex; gap: 10px; }
  button { flex: 1; padding: 10px 0; font-size: 14px; font-weight: 500; border-radius: 8px; cursor: pointer; }
  .approve { background: var(--primary); color: var(--on-primary); border: none; }
  .deny { background: var(--card); color: var(--text); border: 1px solid var(--field); }
  .user { font-size: 12px; color: var(--muted); margin-top: 16px; text-align: center; }
  .error p { font-size: 14px; margin: 0 0 12px; }
</style>
</head>
<body>
<div class="card">
  <div class="brand">
    <svg viewBox="0 0 64 64" aria-hidden="true"><circle class="disc" cx="32" cy="32" r="32"/><path class="glyph" fill-rule="evenodd" d="M32.032 54.000Q28.249 54.000 24.719 52.613Q21.189 51.226 18.384 48.453Q15.579 45.679 13.971 41.582Q12.364 37.484 12.364 32.000Q12.364 26.705 14.003 22.607Q15.642 18.510 18.415 15.705Q21.189 12.900 24.719 11.450Q28.249 10.000 32.032 10.000Q35.751 10.000 39.249 11.387Q42.748 12.774 45.553 15.547Q48.358 18.321 49.997 22.450Q51.636 26.579 51.636 32.000Q51.636 37.295 49.997 41.393Q48.358 45.490 45.585 48.295Q42.811 51.100 39.312 52.550Q35.814 54.000 32.032 54.000ZM32.032 50.596Q35.877 50.596 38.209 48.011Q40.542 45.427 41.645 41.203Q42.748 36.980 42.748 32.000Q42.748 27.020 41.645 22.797Q40.542 18.573 38.209 15.989Q35.877 13.404 32.032 13.404Q28.249 13.404 25.854 15.989Q23.458 18.573 22.324 22.797Q21.189 27.020 21.189 32.000Q21.189 36.980 22.324 41.203Q23.458 45.427 25.854 48.011Q28.249 50.596 32.032 50.596Z"/></svg>
    <span class="wordmark">OpenLoc</span>
  </div>
{{end}}`

const pageFoot = `{{define "foot"}}</div>
</body>
</html>{{end}}`

const consentBody = `{{template "head" (printf "Authorize %s" .ClientName)}}
  <h1>{{.ClientName}} wants access to OpenLoc</h1>
  <p class="sub">It will act as you, limited to what you approve here.</p>
  <p class="dest">After you approve, OpenLoc sends the authorization to <strong>{{.RedirectHost}}</strong>. Only continue if you recognize this destination.</p>
  <div class="scope">
    <strong>{{if .Write}}Read and write{{else}}Read-only{{end}} access</strong>
    {{if .Write}}Do what you can do in OpenLoc yourself, with your role.{{else}}View projects, jobs, translation memories, terminology, QA reports and metrics. Nothing can be modified.{{end}}
  </div>
  <form method="post" action="/oauth/authorize">
    {{range .Hidden}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}
    <label for="tenant_id">Organization</label>
    <select name="tenant_id" id="tenant_id">
      {{range .Orgs}}<option value="{{.ID}}" {{if .Selected}}selected{{end}}>{{.Name}} ({{.Role}})</option>{{end}}
    </select>
    <div class="row">
      <button class="deny" name="decision" value="deny">Deny</button>
      <button class="approve" name="decision" value="approve">Approve</button>
    </div>
  </form>
  <p class="user">Signed in as {{.UserEmail}}</p>
{{template "foot"}}`

const errorBody = `{{template "head" "Authorization error"}}
  <div class="error">
    <h1>Can't authorize this request</h1>
    <p>{{.}}</p>
    <p class="sub">Close this window and try connecting again from the app.</p>
  </div>
{{template "foot"}}`

var (
	pageFrame   = template.Must(template.New("frame").Parse(pageHead + pageFoot))
	consentTmpl = template.Must(template.Must(pageFrame.Clone()).New("consent").Parse(consentBody))
	errorTmpl   = template.Must(template.Must(pageFrame.Clone()).New("error").Parse(errorBody))
)

// renderError writes the branded error page with status and a plain-text
// message, which the template escapes.
func renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := errorTmpl.Execute(w, msg); err != nil {
		logging.Error(r.Context(), err, "error page render failed", "oauth-error-render-failed")
	}
}
