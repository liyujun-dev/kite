package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	kiteauth "github.com/zxh326/kite/pkg/auth"
	"github.com/zxh326/kite/pkg/cluster"
	"github.com/zxh326/kite/pkg/common"
	"github.com/zxh326/kite/pkg/model"
	"github.com/zxh326/kite/pkg/rbac"
)

const (
	mcpScope = "mcp"

	userTokenInfoKey = "kite.user"
)

// Handler serves the MCP streamable HTTP endpoint and the OAuth endpoints that
// MCP clients use to obtain bearer tokens.
type Handler struct {
	cm      *cluster.ClusterManager
	tokens  TokenIssuer
	manager *kiteauth.OAuthManager
	server  *sdkmcp.Server

	streamable http.Handler
}

func NewHandler(cm *cluster.ClusterManager) *Handler {
	h := &Handler{
		cm:      cm,
		tokens:  NewTokenIssuer(),
		manager: kiteauth.NewOAuthManager(),
	}
	h.server = buildServer(h)
	h.streamable = sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return h.server },
		&sdkmcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
		},
	)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	opts := &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL:    h.resourceMetadataURL(r),
		Scopes:                 []string{mcpScope},
		AllowMissingExpiration: true,
	}
	sdkauth.RequireBearerToken(h.verifier(), opts)(h.streamable).ServeHTTP(w, r)
}

func (h *Handler) verifier() sdkauth.TokenVerifier {
	return func(_ context.Context, token string, req *http.Request) (*sdkauth.TokenInfo, error) {
		user, expiration, err := h.authenticate(token, req)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", sdkauth.ErrInvalidToken, err)
		}
		return &sdkauth.TokenInfo{
			Scopes:     []string{mcpScope},
			Expiration: expiration,
			UserID:     fmt.Sprintf("%d", user.ID),
			Extra:      map[string]any{userTokenInfoKey: *user},
		}, nil
	}
}

func (h *Handler) authenticate(token string, r *http.Request) (*model.User, time.Time, error) {
	if common.AnonymousUserEnabled {
		user := anonymousUser()
		return &user, time.Time{}, nil
	}

	if strings.HasPrefix(token, "kite") {
		user, err := kiteauth.ValidateAPIKeyToken(token)
		if err != nil {
			return nil, time.Time{}, err
		}
		return user, time.Time{}, nil
	}

	claims, err := h.tokens.Validate(token, tokenTypeAccess, h.issuerURL(r), h.resourceURL(r))
	if err != nil {
		return nil, time.Time{}, err
	}
	user, err := model.GetUserByID(uint64(claims.UserID))
	if err != nil || !user.Enabled {
		return nil, time.Time{}, fmt.Errorf("user not found or disabled")
	}
	user.Roles = rbac.GetUserRoles(*user)
	return user, claims.ExpiresAt.Time, nil
}

func anonymousUser() model.User {
	if user := model.GetAnonymousUser(); user != nil {
		user.Roles = model.AnonymousUser.Roles
		return *user
	}
	return model.AnonymousUser
}

func (h *Handler) issuerURL(r *http.Request) string {
	if common.MCPIssuer != "" {
		return common.MCPIssuer
	}
	return strings.TrimRight(kiteauth.GetRequestHostFromRequest(r), "/") + common.Base
}

func (h *Handler) resourceURL(r *http.Request) string {
	if common.MCPResource != "" {
		return common.MCPResource
	}
	return h.issuerURL(r) + "/mcp"
}

func (h *Handler) resourceMetadataURL(r *http.Request) string {
	return h.issuerURL(r) + "/.well-known/oauth-protected-resource"
}

func userFromRequest(req *sdkmcp.CallToolRequest) (model.User, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return model.User{}, fmt.Errorf("request is not authenticated")
	}
	user, ok := req.Extra.TokenInfo.Extra[userTokenInfoKey].(model.User)
	if !ok {
		return model.User{}, fmt.Errorf("request is not authenticated")
	}
	return user, nil
}
