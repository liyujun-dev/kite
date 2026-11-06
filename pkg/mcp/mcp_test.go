package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zxh326/kite/pkg/cluster"
	"github.com/zxh326/kite/pkg/common"
	"github.com/zxh326/kite/pkg/model"
	"github.com/zxh326/kite/pkg/rbac"
)

const testRedirectURI = "http://localhost:33418/callback"

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	dir, err := os.MkdirTemp("", "kite-mcp-test-*")
	if err != nil {
		panic(err)
	}
	common.DBType = "sqlite"
	common.DBDSN = filepath.Join(dir, "test.db")
	common.JwtSecret = "kite-mcp-test-secret"
	model.InitDB()
	rbac.RBACConfig = &common.RolesConfig{
		Roles: []common.Role{{
			Name:       "admin",
			Clusters:   []string{"*"},
			Resources:  []string{"*"},
			Namespaces: []string{"*"},
			Verbs:      []string{"*"},
		}},
		RoleMapping: []common.RoleMapping{{
			Name:  "admin",
			Users: []string{"*"},
		}},
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	cm, err := cluster.NewClusterManager()
	if err != nil {
		t.Fatalf("create cluster manager: %v", err)
	}
	engine := gin.New()
	NewHandler(cm).RegisterRoutes(engine.Group(""))
	return engine
}

func createTestUser(t *testing.T, username string) *model.User {
	t.Helper()
	user := &model.User{Username: username, Password: "password"}
	if err := model.AddUser(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func authCookie(t *testing.T, user *model.User) *http.Cookie {
	t.Helper()
	token, err := NewHandler(nil).manager.GenerateJWT(user, "")
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}
	return &http.Cookie{Name: "auth_token", Value: token}
}

func registerTestClient(t *testing.T, engine *gin.Engine, authMethod string) map[string]any {
	t.Helper()
	body := map[string]any{
		"redirect_uris":              []string{testRedirectURI},
		"client_name":                "test client",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": authMethod,
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal registration body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/oauth/register", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("register client: status %d body %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	return response
}

func pkcePair(t *testing.T) (verifier, challenge string) {
	t.Helper()
	verifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func authorizeURLFor(clientID, challenge string) string {
	query := url.Values{}
	query.Set("client_id", clientID)
	query.Set("redirect_uri", testRedirectURI)
	query.Set("response_type", "code")
	query.Set("state", "state-123")
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	return "/api/oauth/authorize?" + query.Encode()
}

func startAuthorization(t *testing.T, engine *gin.Engine, user *model.User, clientID, challenge string) (code string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, authorizeURLFor(clientID, challenge), nil)
	request.AddCookie(authCookie(t, user))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorize GET: status %d body %s", recorder.Code, recorder.Body.String())
	}

	match := regexp.MustCompile(`name="request" value="([^"]+)"`).FindStringSubmatch(recorder.Body.String())
	if len(match) != 2 {
		t.Fatalf("consent page did not contain a signed request: %s", recorder.Body.String())
	}

	form := url.Values{}
	form.Set("request", match[1])
	form.Set("decision", "allow")
	request = httptest.NewRequest(http.MethodPost, "/api/oauth/authorize", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(authCookie(t, user))
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusFound {
		t.Fatalf("authorize POST: status %d body %s", recorder.Code, recorder.Body.String())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if location.Query().Get("state") != "state-123" {
		t.Fatalf("state not preserved: %s", location.String())
	}
	if location.Query().Get("iss") == "" {
		t.Fatalf("iss parameter missing: %s", location.String())
	}
	code = location.Query().Get("code")
	if code == "" {
		t.Fatalf("code missing: %s", location.String())
	}
	return code
}

func exchangeCode(t *testing.T, engine *gin.Engine, clientID, verifier, code string) map[string]any {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", verifier)
	request := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("token exchange: status %d body %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	return response
}

func TestOAuthAuthorizationCodeFlow(t *testing.T) {
	engine := newTestRouter(t)
	user := createTestUser(t, "alice")
	client := registerTestClient(t, engine, "none")
	clientID := client["client_id"].(string)

	unauthed := httptest.NewRequest(http.MethodGet, authorizeURLFor(clientID, "challenge"), nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, unauthed)
	if recorder.Code != http.StatusFound || !strings.Contains(recorder.Header().Get("Location"), "/login?redirect=") {
		t.Fatalf("unauthenticated authorize should redirect to login, got %d %s", recorder.Code, recorder.Header().Get("Location"))
	}

	verifier, challenge := pkcePair(t)
	code := startAuthorization(t, engine, user, clientID, challenge)
	tokens := exchangeCode(t, engine, clientID, verifier, code)

	accessToken, _ := tokens["access_token"].(string)
	refreshToken, _ := tokens["refresh_token"].(string)
	if accessToken == "" || refreshToken == "" {
		t.Fatalf("missing tokens: %v", tokens)
	}

	// The code is single use.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", verifier)
	request := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("reused code should fail, got %d", recorder.Code)
	}

	// Refresh rotates the token, and reusing the old one revokes the family.
	refresh := func(token string) *httptest.ResponseRecorder {
		form := url.Values{}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", token)
		form.Set("client_id", clientID)
		request := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	firstRefresh := refresh(refreshToken)
	if firstRefresh.Code != http.StatusOK {
		t.Fatalf("refresh failed: %d %s", firstRefresh.Code, firstRefresh.Body.String())
	}
	var refreshed map[string]any
	_ = json.Unmarshal(firstRefresh.Body.Bytes(), &refreshed)
	newRefresh, _ := refreshed["refresh_token"].(string)
	if newRefresh == "" || newRefresh == refreshToken {
		t.Fatalf("refresh token was not rotated")
	}
	if reused := refresh(refreshToken); reused.Code != http.StatusBadRequest {
		t.Fatalf("reused refresh token should fail, got %d", reused.Code)
	}
	if revoked := refresh(newRefresh); revoked.Code != http.StatusBadRequest {
		t.Fatalf("family should be revoked after reuse, got %d", revoked.Code)
	}
}

func TestPKCEVerificationFails(t *testing.T) {
	engine := newTestRouter(t)
	user := createTestUser(t, "alice-pkce")
	client := registerTestClient(t, engine, "none")
	clientID := client["client_id"].(string)
	_, challenge := pkcePair(t)
	code := startAuthorization(t, engine, user, clientID, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", strings.Repeat("x", 50))
	request := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier should fail, got %d", recorder.Code)
	}
}

func TestRegisterClientValidation(t *testing.T) {
	engine := newTestRouter(t)
	for _, redirect := range []string{
		"http://example.com/callback",
		"javascript:alert(1)",
		"https://example.com/callback#fragment",
	} {
		body, err := json.Marshal(map[string]any{"redirect_uris": []string{redirect}})
		if err != nil {
			t.Fatalf("marshal registration body: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/oauth/register", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("redirect %q should be rejected, got %d", redirect, recorder.Code)
		}
	}
}

func TestMCPRequiresBearerToken(t *testing.T) {
	engine := newTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("missing resource_metadata: %q", recorder.Header().Get("WWW-Authenticate"))
	}
}

func TestMCPInitializeWithAPIKey(t *testing.T) {
	engine := newTestRouter(t)
	keyUser, err := model.NewAPIKeyUser("mcp-bot")
	if err != nil {
		t.Fatalf("create api key user: %v", err)
	}

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+keyUser.GetAPIKey())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("initialize failed: %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "protocolVersion") {
		t.Fatalf("unexpected initialize response: %s", recorder.Body.String())
	}

	// A tool call proves the authenticated user reaches the tool handlers.
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_clusters","arguments":{}}}`
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(call))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+keyUser.GetAPIKey())
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/call failed: %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "[]") {
		t.Fatalf("unexpected list_clusters response: %s", recorder.Body.String())
	}
}

func TestEmptyAPIKeyIsRejected(t *testing.T) {
	engine := newTestRouter(t)
	user := createTestUser(t, "bob")
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", fmt.Sprintf("Bearer kite%d-", user.ID))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("empty API key must not authenticate, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestConsentTokenTampering(t *testing.T) {
	h := NewHandler(nil)
	params := &authorizeParams{
		ClientID:            "client",
		RedirectURI:         testRedirectURI,
		ResponseType:        "code",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
	}
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal consent params: %v", err)
	}
	blob := h.tokens.signConsent(payload)
	if _, err := h.verifyAuthorizeState(blob); err == nil {
		t.Fatalf("expired consent state should fail (ExpiresAt is zero)")
	}
	params.ExpiresAt = 9999999999
	payload, err = json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal consent params: %v", err)
	}
	blob = h.tokens.signConsent(payload)
	if _, err := h.verifyAuthorizeState(blob); err != nil {
		t.Fatalf("valid consent state rejected: %v", err)
	}
	tampered := "x" + blob[1:]
	if _, err := h.verifyAuthorizeState(tampered); err == nil {
		t.Fatalf("tampered consent state should fail")
	}
}

func TestConsentLanguage(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		accept string
		want   string
	}{
		{"chinese accept-language", "", "zh-CN,zh;q=0.9,en;q=0.8", "zh"},
		{"english accept-language", "", "en-US,en;q=0.9", "en"},
		{"unknown falls back to english", "", "fr-FR,fr;q=0.9", "en"},
		{"query overrides header to chinese", "lang=zh", "en-US", "zh"},
		{"query overrides header to english", "lang=en", "zh-CN", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/api/oauth/authorize?"+tc.query, nil)
			if tc.accept != "" {
				c.Request.Header.Set("Accept-Language", tc.accept)
			}
			if got := consentLanguage(c); got != tc.want {
				t.Fatalf("consentLanguage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConsentPageLocalization(t *testing.T) {
	engine := newTestRouter(t)
	user := createTestUser(t, "consent-i18n")
	client := registerTestClient(t, engine, "none")
	clientID := client["client_id"].(string)
	_, challenge := pkcePair(t)

	fetch := func(lang string) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, authorizeURLFor(clientID, challenge)+"&lang="+lang, nil)
		request.AddCookie(authCookie(t, user))
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("consent page (%s): status %d body %s", lang, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}

	zh := fetch("zh")
	for _, want := range []string{"拒绝", "授权", "/assets/logo.svg", `lang="zh"`} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese consent page missing %q", want)
		}
	}

	en := fetch("en")
	for _, want := range []string{"Deny", "Authorize", "/assets/logo.svg", `lang="en"`} {
		if !strings.Contains(en, want) {
			t.Errorf("English consent page missing %q", want)
		}
	}
}

func TestResolveRedirectURIValidation(t *testing.T) {
	valid := []string{
		"https://example.com/callback",
		"http://localhost:1234/callback",
		"http://127.0.0.1/callback",
		"cursor://anysphere.cursor/callback",
	}
	for _, value := range valid {
		if err := validateRedirectURI(value); err != nil {
			t.Errorf("expected %q to be valid: %v", value, err)
		}
	}
	invalid := []string{"http://example.com/callback", "javascript:alert(1)", "data:text/html,x", "https://example.com/cb#frag"}
	for _, value := range invalid {
		if err := validateRedirectURI(value); err == nil {
			t.Errorf("expected %q to be rejected", value)
		}
	}
}
