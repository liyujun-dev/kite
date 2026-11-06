package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	kiteauth "github.com/zxh326/kite/pkg/auth"
	"github.com/zxh326/kite/pkg/common"
	"github.com/zxh326/kite/pkg/model"
	"github.com/zxh326/kite/pkg/rbac"
)

const (
	authorizationCodeTTL = 5 * time.Minute
	consentTTL           = 5 * time.Minute
)

// RegisterRoutes registers the MCP endpoint and the OAuth 2.1 endpoints used by MCP clients.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/.well-known/oauth-protected-resource", h.protectedResourceMetadata)
	r.GET("/.well-known/oauth-protected-resource/mcp", h.protectedResourceMetadata)
	r.GET("/.well-known/oauth-authorization-server", h.authorizationServerMetadata)
	r.GET("/.well-known/openid-configuration", h.authorizationServerMetadata)
	r.POST("/api/oauth/register", h.registerClient)
	r.GET("/api/oauth/authorize", h.authorize)
	r.POST("/api/oauth/authorize", h.authorizeDecision)
	r.POST("/api/oauth/token", h.token)
	r.Any("/mcp", gin.WrapH(h))
}

func (h *Handler) protectedResourceMetadata(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, oauthex.ProtectedResourceMetadata{
		Resource:               h.resourceURL(c.Request),
		AuthorizationServers:   []string{h.issuerURL(c.Request)},
		ScopesSupported:        []string{mcpScope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Kite MCP",
	})
}

type authServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

func (h *Handler) authorizationServerMetadata(c *gin.Context) {
	issuer := h.issuerURL(c.Request)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, authServerMetadata{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/api/oauth/authorize",
		TokenEndpoint:                     issuer + "/api/oauth/token",
		RegistrationEndpoint:              issuer + "/api/oauth/register",
		ScopesSupported:                   []string{mcpScope},
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		CodeChallengeMethodsSupported:     []string{"S256"},
	})
}

type registrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope"`
}

type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientSecretExpiresAt   int64    `json:"client_secret_expires_at,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope"`
}

func (h *Handler) registerClient(c *gin.Context) {
	var req registrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON body")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthError(c, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	if len(req.RedirectURIs) > 10 {
		oauthError(c, http.StatusBadRequest, "invalid_redirect_uri", "too many redirect_uris")
		return
	}
	for _, raw := range req.RedirectURIs {
		if err := validateRedirectURI(raw); err != nil {
			oauthError(c, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}

	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}
	if !slices.Contains(grantTypes, "authorization_code") {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "grant_types must include authorization_code")
		return
	}
	for _, grant := range grantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant type: "+grant)
			return
		}
	}
	for _, responseType := range req.ResponseTypes {
		if responseType != "code" {
			oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "unsupported response type: "+responseType)
			return
		}
	}

	authMethod := req.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "none"
	}
	var clientSecret string
	switch authMethod {
	case "none":
	case "client_secret_post", "client_secret_basic":
		clientSecret = randomCode()
	default:
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return
	}

	client := &model.MCPOAuthClient{
		ClientID:                "kite-mcp-" + randomID(12),
		ClientSecretHash:        hashSecret(clientSecret),
		Name:                    req.ClientName,
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              grantTypes,
		ResponseTypes:           []string{"code"},
		Scope:                   mcpScope,
		TokenEndpointAuthMethod: authMethod,
	}
	if clientSecret == "" {
		client.ClientSecretHash = ""
	}
	if err := model.CreateMCPOAuthClient(client); err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to register client")
		return
	}

	c.JSON(http.StatusCreated, registrationResponse{
		ClientID:                client.ClientID,
		ClientSecret:            clientSecret,
		ClientIDIssuedAt:        time.Now().Unix(),
		ClientSecretExpiresAt:   0,
		ClientName:              client.Name,
		RedirectURIs:            client.RedirectURIs,
		GrantTypes:              client.GrantTypes,
		ResponseTypes:           client.ResponseTypes,
		TokenEndpointAuthMethod: client.TokenEndpointAuthMethod,
		Scope:                   client.Scope,
	})
}

type authorizeParams struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	State               string `json:"state,omitempty"`
	Scope               string `json:"scope,omitempty"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource,omitempty"`
	ExpiresAt           int64  `json:"exp"`
}

func (h *Handler) validateAuthorize(c *gin.Context) (*authorizeParams, *model.MCPOAuthClient, error) {
	params := &authorizeParams{
		ClientID:            c.Query("client_id"),
		RedirectURI:         c.Query("redirect_uri"),
		ResponseType:        c.Query("response_type"),
		State:               c.Query("state"),
		Scope:               c.Query("scope"),
		CodeChallenge:       c.Query("code_challenge"),
		CodeChallengeMethod: c.Query("code_challenge_method"),
		Resource:            c.Query("resource"),
	}
	if params.ClientID == "" {
		return nil, nil, fmt.Errorf("client_id is required")
	}
	client, err := model.GetMCPOAuthClient(params.ClientID)
	if err != nil {
		return nil, nil, fmt.Errorf("unknown client")
	}
	if params.RedirectURI == "" && len(client.RedirectURIs) == 1 {
		params.RedirectURI = client.RedirectURIs[0]
	}
	if params.RedirectURI == "" || !slices.Contains(client.RedirectURIs, params.RedirectURI) {
		return nil, nil, fmt.Errorf("redirect_uri does not match the registered client")
	}
	if params.ResponseType != "code" {
		return nil, nil, fmt.Errorf("response_type must be code")
	}
	if params.CodeChallenge == "" {
		return nil, nil, fmt.Errorf("code_challenge is required")
	}
	if params.CodeChallengeMethod == "" {
		params.CodeChallengeMethod = "S256"
	}
	if params.CodeChallengeMethod != "S256" {
		return nil, nil, fmt.Errorf("code_challenge_method must be S256")
	}
	if params.Resource != "" && strings.TrimRight(params.Resource, "/") != h.resourceURL(c.Request) {
		return nil, nil, fmt.Errorf("unsupported resource")
	}
	if params.Scope == "" {
		params.Scope = mcpScope
	}
	return params, client, nil
}

func (h *Handler) authorize(c *gin.Context) {
	params, client, err := h.validateAuthorize(c)
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	user, err := h.currentUser(c)
	if err != nil {
		c.Redirect(http.StatusFound, common.Base+"/login?redirect="+url.QueryEscape(c.Request.URL.RequestURI()))
		return
	}
	if len(rbac.GetUserRoles(*user)) == 0 {
		c.String(http.StatusForbidden, "Your Kite account has no RBAC roles; ask an administrator to assign a role before authorizing MCP clients.")
		return
	}

	params.ExpiresAt = time.Now().Add(consentTTL).Unix()
	payload, err := json.Marshal(params)
	if err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to build consent request")
		return
	}

	lang := consentLanguage(c)
	texts := consentTexts[lang]
	clientName := client.Name
	if clientName == "" {
		clientName = client.ClientID
	}
	account := user.Username
	if user.Name != "" && user.Name != user.Username {
		account = user.Name + " (" + user.Username + ")"
	}
	page := consentPage{
		Lang:        lang,
		PageTitle:   texts.Title + " - Kite",
		LogoURL:     common.Base + "/assets/logo.svg",
		Action:      common.Base + "/api/oauth/authorize",
		Request:     h.tokens.signConsent(payload),
		ClientName:  clientName,
		Account:     account,
		RedirectURI: params.RedirectURI,
		Permission:  texts.PermissionValue,
		Description: strings.ReplaceAll(texts.Description, "{client}", clientName),
		Texts:       texts,
		Languages:   consentLanguageLinks(c, lang),
	}
	html, err := renderConsentPage(page)
	if err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to render consent page")
		return
	}
	c.Header("Cache-Control", "no-store")
	// Note: form-action must not be restricted here. After the consent form is
	// submitted, the authorize endpoint redirects to the OAuth client's callback
	// (a different origin), and browsers apply form-action to that redirect too.
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; frame-ancestors 'none'")
	c.Header("X-Frame-Options", "DENY")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func (h *Handler) authorizeDecision(c *gin.Context) {
	if origin := c.GetHeader("Origin"); origin != "" {
		expected := strings.TrimRight(kiteauth.GetRequestHostFromRequest(c.Request), "/")
		if strings.TrimRight(origin, "/") != expected {
			oauthError(c, http.StatusForbidden, "invalid_request", "cross-origin consent submission is not allowed")
			return
		}
	}
	params, err := h.verifyAuthorizeState(c.PostForm("request"))
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user, err := h.currentUser(c)
	if err != nil {
		c.Redirect(http.StatusFound, common.Base+"/login?redirect="+url.QueryEscape(authorizePath(params)))
		return
	}
	if c.PostForm("decision") != "allow" {
		redirectAuthorizeError(c, params, h.issuerURL(c.Request), "access_denied", "The user denied the request")
		return
	}
	if len(rbac.GetUserRoles(*user)) == 0 {
		redirectAuthorizeError(c, params, h.issuerURL(c.Request), "access_denied", "The user has no RBAC roles")
		return
	}

	// Re-check the client and redirect URI in case they changed after the consent page rendered.
	client, err := model.GetMCPOAuthClient(params.ClientID)
	if err != nil || !slices.Contains(client.RedirectURIs, params.RedirectURI) {
		oauthError(c, http.StatusBadRequest, "invalid_request", "client or redirect_uri is no longer valid")
		return
	}

	_ = model.DeleteExpiredMCPOAuthTokens()

	code := randomCode()
	if err := model.CreateMCPOAuthCode(&model.MCPOAuthCode{
		CodeHash:            hashSecret(code),
		ClientID:            params.ClientID,
		UserID:              user.ID,
		RedirectURI:         params.RedirectURI,
		Scope:               mcpScope,
		CodeChallenge:       params.CodeChallenge,
		CodeChallengeMethod: params.CodeChallengeMethod,
		ExpiresAt:           time.Now().Add(authorizationCodeTTL),
	}); err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to issue authorization code")
		return
	}

	target, err := url.Parse(params.RedirectURI)
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}
	query := target.Query()
	query.Set("code", code)
	query.Set("iss", h.issuerURL(c.Request))
	if params.State != "" {
		query.Set("state", params.State)
	}
	target.RawQuery = query.Encode()
	c.Redirect(http.StatusFound, target.String())
}

func (h *Handler) token(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")

	clientID := c.PostForm("client_id")
	clientSecret := c.PostForm("client_secret")
	if id, secret, ok := c.Request.BasicAuth(); ok {
		clientID = id
		clientSecret = secret
	}
	if clientID == "" {
		oauthError(c, http.StatusBadRequest, "invalid_client", "client_id is required")
		return
	}
	client, err := model.GetMCPOAuthClient(clientID)
	if err != nil {
		oauthError(c, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	if client.TokenEndpointAuthMethod != "none" {
		if clientSecret == "" || subtle.ConstantTimeCompare([]byte(hashSecret(clientSecret)), []byte(client.ClientSecretHash)) != 1 {
			c.Header("WWW-Authenticate", `Basic realm="kite-mcp"`)
			oauthError(c, http.StatusUnauthorized, "invalid_client", "invalid client credentials")
			return
		}
	}

	switch c.PostForm("grant_type") {
	case "authorization_code":
		h.exchangeAuthorizationCode(c, client)
	case "refresh_token":
		h.exchangeRefreshToken(c, client)
	default:
		oauthError(c, http.StatusBadRequest, "unsupported_grant_type", "supported grant types are authorization_code and refresh_token")
	}
}

func (h *Handler) exchangeAuthorizationCode(c *gin.Context, client *model.MCPOAuthClient) {
	code := c.PostForm("code")
	verifier := c.PostForm("code_verifier")
	redirectURI := c.PostForm("redirect_uri")
	if code == "" || verifier == "" || redirectURI == "" {
		oauthError(c, http.StatusBadRequest, "invalid_request", "code, code_verifier, and redirect_uri are required")
		return
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "invalid code_verifier")
		return
	}

	record, err := model.ConsumeMCPOAuthCode(hashSecret(code))
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired, or already used")
		return
	}
	if record.ClientID != client.ClientID || record.RedirectURI != redirectURI {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "authorization code does not match the client")
		return
	}
	if record.CodeChallengeMethod != "S256" {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "unsupported code_challenge_method")
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(computed), []byte(record.CodeChallenge)) != 1 {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	user, err := model.GetUserByID(uint64(record.UserID))
	if err != nil || !user.Enabled || len(rbac.GetUserRoles(*user)) == 0 {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "user is not allowed to access Kite")
		return
	}
	h.issueTokens(c, user, client.ClientID, randomID(16))
}

func (h *Handler) exchangeRefreshToken(c *gin.Context, client *model.MCPOAuthClient) {
	refreshToken := c.PostForm("refresh_token")
	if refreshToken == "" {
		oauthError(c, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	claims, err := h.tokens.Validate(refreshToken, tokenTypeRefresh, h.issuerURL(c.Request), h.resourceURL(c.Request))
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	if claims.ClientID != client.ClientID {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "refresh token does not match the client")
		return
	}
	if _, err := model.ConsumeMCPOAuthRefreshToken(claims.ID, hashSecret(refreshToken)); err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired, or already used")
		return
	}
	user, err := model.GetUserByID(uint64(claims.UserID))
	if err != nil || !user.Enabled || len(rbac.GetUserRoles(*user)) == 0 {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "user is not allowed to access Kite")
		return
	}
	family := claims.Family
	if family == "" {
		family = randomID(16)
	}
	h.issueTokens(c, user, client.ClientID, family)
}

func (h *Handler) issueTokens(c *gin.Context, user *model.User, clientID, familyID string) {
	issuer := h.issuerURL(c.Request)
	resource := h.resourceURL(c.Request)
	accessToken, err := h.tokens.Issue(user.ID, clientID, issuer, resource, tokenTypeAccess, "", randomID(16), accessTokenTTL)
	if err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to issue access token")
		return
	}
	refreshID := randomID(16)
	refreshToken, err := h.tokens.Issue(user.ID, clientID, issuer, resource, tokenTypeRefresh, familyID, refreshID, refreshTokenTTL)
	if err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to issue refresh token")
		return
	}
	if err := model.CreateMCPOAuthRefreshToken(&model.MCPOAuthRefreshToken{
		TokenID:   refreshID,
		TokenHash: hashSecret(refreshToken),
		FamilyID:  familyID,
		ClientID:  clientID,
		UserID:    user.ID,
		Scope:     mcpScope,
		ExpiresAt: time.Now().Add(refreshTokenTTL),
	}); err != nil {
		oauthError(c, http.StatusInternalServerError, "server_error", "failed to store refresh token")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
		"refresh_token": refreshToken,
		"scope":         mcpScope,
	})
}

func (h *Handler) currentUser(c *gin.Context) (*model.User, error) {
	token, err := c.Cookie("auth_token")
	if err != nil || token == "" {
		return nil, fmt.Errorf("not authenticated")
	}
	claims, err := h.manager.ValidateJWT(token)
	if err != nil {
		return nil, err
	}
	user, err := model.GetUserByID(uint64(claims.UserID))
	if err != nil {
		return nil, err
	}
	if !user.Enabled {
		return nil, fmt.Errorf("user disabled")
	}
	user.Roles = rbac.GetUserRoles(*user)
	return user, nil
}

func (h *Handler) verifyAuthorizeState(blob string) (*authorizeParams, error) {
	payload, err := h.tokens.verifyConsent(blob)
	if err != nil {
		return nil, err
	}
	var params authorizeParams
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, fmt.Errorf("invalid consent request")
	}
	if params.ExpiresAt == 0 || time.Unix(params.ExpiresAt, 0).Before(time.Now()) {
		return nil, fmt.Errorf("consent request expired; start again")
	}
	if params.CodeChallengeMethod != "S256" || params.CodeChallenge == "" {
		return nil, fmt.Errorf("invalid PKCE parameters")
	}
	return &params, nil
}

// authorizePath rebuilds a local authorize URL for redirecting an expired session through login.
func authorizePath(params *authorizeParams) string {
	query := url.Values{}
	query.Set("client_id", params.ClientID)
	query.Set("redirect_uri", params.RedirectURI)
	query.Set("response_type", "code")
	query.Set("code_challenge", params.CodeChallenge)
	query.Set("code_challenge_method", params.CodeChallengeMethod)
	query.Set("scope", params.Scope)
	if params.State != "" {
		query.Set("state", params.State)
	}
	if params.Resource != "" {
		query.Set("resource", params.Resource)
	}
	return common.Base + "/api/oauth/authorize?" + query.Encode()
}

func redirectAuthorizeError(c *gin.Context, params *authorizeParams, issuer, code, description string) {
	target, err := url.Parse(params.RedirectURI)
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}
	query := target.Query()
	query.Set("error", code)
	query.Set("error_description", description)
	query.Set("iss", issuer)
	if params.State != "" {
		query.Set("state", params.State)
	}
	target.RawQuery = query.Encode()
	c.Redirect(http.StatusFound, target.String())
}

func oauthError(c *gin.Context, status int, code, description string) {
	c.JSON(status, gin.H{
		"error":             code,
		"error_description": description,
	})
}

// validateRedirectURI accepts HTTPS, loopback HTTP (native apps), and custom
// native-app schemes. Dangerous web schemes are rejected.
func validateRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("redirect_uri must be an absolute URI")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("redirect_uri must not contain a fragment")
	}
	if parsed.User != nil {
		return fmt.Errorf("redirect_uri must not contain userinfo")
	}
	if strings.Contains(raw, "*") {
		return fmt.Errorf("redirect_uri must not contain wildcards")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		if parsed.Host == "" {
			return fmt.Errorf("redirect_uri must include a host")
		}
		return nil
	case "http":
		switch parsed.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		default:
			return fmt.Errorf("http redirect_uri is only allowed for loopback hosts")
		}
	case "javascript", "data", "file", "vbscript":
		return fmt.Errorf("unsupported redirect_uri scheme %q", parsed.Scheme)
	default:
		return nil
	}
}
