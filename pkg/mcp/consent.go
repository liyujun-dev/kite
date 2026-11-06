package mcp

import (
	"html/template"
	"strings"
)

type consentPage struct {
	ClientName  string
	ClientID    string
	RedirectURI string
	Scope       string
	Issuer      string
	Request     string
	Action      string
}

var consentTemplate = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Authorize MCP client - Kite</title>
<style>
  :root { color-scheme: light dark; --fg:#111827; --muted:#6b7280; --bg:#f9fafb; --card:#ffffff; --border:#e5e7eb; --accent:#2563eb; }
  @media (prefers-color-scheme: dark) {
    :root { --fg:#f9fafb; --muted:#9ca3af; --bg:#0b0f19; --card:#111827; --border:#374151; --accent:#60a5fa; }
  }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center; padding:24px;
         background:var(--bg); color:var(--fg); font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif; }
  .card { width:100%; max-width:460px; background:var(--card); border:1px solid var(--border); border-radius:14px; padding:28px; }
  .brand { font-size:13px; letter-spacing:.12em; text-transform:uppercase; color:var(--muted); }
  h1 { font-size:20px; margin:10px 0 6px; }
  p { color:var(--muted); line-height:1.55; font-size:14px; margin:8px 0; }
  dl { margin:20px 0; padding:14px; border:1px solid var(--border); border-radius:10px; font-size:13px; }
  dt { color:var(--muted); margin-top:8px; }
  dt:first-child { margin-top:0; }
  dd { margin:2px 0 0; word-break:break-all; }
  .actions { display:flex; gap:10px; margin-top:22px; }
  button { flex:1; padding:11px 14px; border-radius:9px; font-size:14px; font-weight:600; cursor:pointer; border:1px solid var(--border); }
  .allow { background:var(--accent); border-color:var(--accent); color:#fff; }
  .deny { background:transparent; color:var(--fg); }
  .note { font-size:12px; color:var(--muted); margin-top:18px; }
</style>
</head>
<body>
  <main class="card">
    <div class="brand">Kite</div>
    <h1>Authorize {{.ClientName}}</h1>
    <p>This MCP client is asking for access to your Kubernetes clusters. It will act with your Kite account and RBAC permissions.</p>
    <dl>
      <dt>Client</dt>
      <dd>{{.ClientName}} ({{.ClientID}})</dd>
      <dt>Redirect URI</dt>
      <dd>{{.RedirectURI}}</dd>
      <dt>Scope</dt>
      <dd>{{.Scope}}</dd>
    </dl>
    <form method="post" action="{{.Action}}">
      <input type="hidden" name="request" value="{{.Request}}">
      <div class="actions">
        <button class="deny" type="submit" name="decision" value="deny">Deny</button>
        <button class="allow" type="submit" name="decision" value="allow">Authorize</button>
      </div>
    </form>
    <p class="note">Only authorize clients you trust. You can revoke access by disabling your Kite account or removing the client.</p>
  </main>
</body>
</html>`))

func renderConsentPage(page consentPage) (string, error) {
	var out strings.Builder
	if err := consentTemplate.Execute(&out, page); err != nil {
		return "", err
	}
	return out.String(), nil
}
