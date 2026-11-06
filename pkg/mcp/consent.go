package mcp

import (
	"html/template"
	"strings"

	"github.com/gin-gonic/gin"
)

type consentText struct {
	Title           string
	Description     string
	Client          string
	Account         string
	Redirect        string
	Permission      string
	PermissionValue string
	Deny            string
	Authorize       string
}

// consentTexts mirrors the frontend's English/Chinese support for the server-rendered
// consent page. Language is picked from ?lang= or the Accept-Language header.
var consentTexts = map[string]consentText{
	"en": {
		Title:           "Authorize MCP client",
		Description:     "{client} wants to access your Kubernetes clusters through Kite using your account and RBAC permissions.",
		Client:          "Client",
		Account:         "Account",
		Redirect:        "Redirect URI",
		Permission:      "Permissions",
		PermissionValue: "Your Kite RBAC permissions",
		Deny:            "Deny",
		Authorize:       "Authorize",
	},
	"zh": {
		Title:           "授权 MCP 客户端",
		Description:     "{client} 请求使用你的 Kite 账号和 RBAC 权限访问 Kubernetes 集群。",
		Client:          "客户端",
		Account:         "当前账号",
		Redirect:        "回调地址",
		Permission:      "权限范围",
		PermissionValue: "你的 Kite RBAC 权限",
		Deny:            "拒绝",
		Authorize:       "授权",
	},
}

type consentLangLink struct {
	Label  string
	Href   string
	Active bool
}

type consentPage struct {
	Lang        string
	PageTitle   string
	LogoURL     string
	Action      string
	Request     string
	ClientName  string
	Account     string
	RedirectURI string
	Permission  string
	Description string
	Texts       consentText
	Languages   []consentLangLink
}

var consentTemplate = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.PageTitle}}</title>
<style>
  :root {
    color-scheme: light dark;
    --bg: #f9fafb;
    --card: #ffffff;
    --fg: #0f172a;
    --muted: #64748b;
    --border: #e2e8f0;
    --muted-bg: #f8fafc;
    --primary: oklch(0.55 0.22 235);
    --primary-fg: oklch(0.98 0.01 235);
    --shadow: 0 10px 30px rgba(15, 23, 42, .06);
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #0b0f19;
      --card: #111827;
      --fg: #f8fafc;
      --muted: #94a3b8;
      --border: #263041;
      --muted-bg: #0f172a;
      --primary: oklch(0.65 0.18 235);
      --primary-fg: oklch(0.15 0.02 235);
      --shadow: 0 10px 30px rgba(0, 0, 0, .35);
    }
    .logo { filter: invert(1); }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    padding: 24px; background: var(--bg); color: var(--fg);
    font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  }
  .wrap { width: 100%; max-width: 420px; }
  .brand { display: flex; align-items: center; justify-content: center; gap: 10px; margin-bottom: 20px; }
  .logo { width: 40px; height: 40px; }
  .brand-name { font-size: 22px; font-weight: 700; letter-spacing: -.02em; }
  .card {
    background: var(--card); border: 1px solid var(--border); border-radius: 16px;
    padding: 28px; box-shadow: var(--shadow);
  }
  h1 { margin: 0; font-size: 20px; font-weight: 650; letter-spacing: -.01em; }
  .description { margin: 10px 0 0; color: var(--muted); font-size: 14px; line-height: 1.6; }
  dl {
    margin: 22px 0 0; padding: 0; border: 1px solid var(--border);
    border-radius: 12px; background: var(--muted-bg); overflow: hidden;
  }
  .row { display: flex; gap: 16px; justify-content: space-between; padding: 12px 14px; font-size: 13px; }
  .row + .row { border-top: 1px solid var(--border); }
  dt { color: var(--muted); flex: 0 0 auto; }
  dd { margin: 0; text-align: right; word-break: break-all; font-weight: 500; }
  .actions { display: flex; gap: 10px; margin-top: 22px; }
  button {
    flex: 1; padding: 11px 16px; border-radius: 10px; font-size: 14px; font-weight: 600;
    cursor: pointer; transition: opacity .15s ease, transform .05s ease;
  }
  button:active { transform: translateY(1px); }
  .deny { background: transparent; color: var(--fg); border: 1px solid var(--border); }
  .deny:hover { background: var(--muted-bg); }
  .allow { background: var(--primary); color: var(--primary-fg); border: 1px solid var(--primary); }
  .allow:hover { opacity: .9; }
  .lang { display: flex; justify-content: center; gap: 10px; margin-top: 18px; font-size: 12px; }
  .lang a { color: var(--muted); text-decoration: none; }
  .lang a.active { color: var(--fg); font-weight: 600; }
  .lang a:hover { text-decoration: underline; }
</style>
</head>
<body>
  <div class="wrap">
    <div class="brand">
      <img class="logo" src="{{.LogoURL}}" alt="Kite">
      <span class="brand-name">Kite</span>
    </div>
    <main class="card">
      <h1>{{.Texts.Title}}</h1>
      <p class="description">{{.Description}}</p>
      <dl>
        <div class="row"><dt>{{.Texts.Client}}</dt><dd>{{.ClientName}}</dd></div>
        <div class="row"><dt>{{.Texts.Account}}</dt><dd>{{.Account}}</dd></div>
        <div class="row"><dt>{{.Texts.Redirect}}</dt><dd>{{.RedirectURI}}</dd></div>
        <div class="row"><dt>{{.Texts.Permission}}</dt><dd>{{.Permission}}</dd></div>
      </dl>
      <form method="post" action="{{.Action}}">
        <input type="hidden" name="request" value="{{.Request}}">
        <div class="actions">
          <button class="deny" type="submit" name="decision" value="deny">{{.Texts.Deny}}</button>
          <button class="allow" type="submit" name="decision" value="allow">{{.Texts.Authorize}}</button>
        </div>
      </form>
    </main>
    <div class="lang">
      {{- range .Languages -}}
      <a href="{{.Href}}"{{if .Active}} class="active" aria-current="true"{{end}}>{{.Label}}</a>
      {{- end -}}
    </div>
  </div>
</body>
</html>`))

func renderConsentPage(page consentPage) (string, error) {
	var out strings.Builder
	if err := consentTemplate.Execute(&out, page); err != nil {
		return "", err
	}
	return out.String(), nil
}

// consentLanguage returns "zh" or "en" for the consent page. An explicit ?lang=
// wins, otherwise the Accept-Language header is used with English as fallback.
func consentLanguage(c *gin.Context) string {
	if lang := strings.ToLower(strings.TrimSpace(c.Query("lang"))); lang == "zh" || lang == "en" {
		return lang
	}
	for _, part := range strings.Split(strings.ToLower(c.GetHeader("Accept-Language")), ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.HasPrefix(tag, "zh") {
			return "zh"
		}
		if strings.HasPrefix(tag, "en") {
			return "en"
		}
	}
	return "en"
}

func consentLanguageLinks(c *gin.Context, current string) []consentLangLink {
	labels := map[string]string{"en": "English", "zh": "中文"}
	links := make([]consentLangLink, 0, len(labels))
	for _, lang := range []string{"en", "zh"} {
		query := c.Request.URL.Query()
		query.Set("lang", lang)
		links = append(links, consentLangLink{
			Label:  labels[lang],
			Href:   c.Request.URL.Path + "?" + query.Encode(),
			Active: lang == current,
		})
	}
	return links
}
