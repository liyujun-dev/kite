package auth

import (
	"crypto/subtle"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/zxh326/kite/pkg/common"
	"github.com/zxh326/kite/pkg/model"
	"github.com/zxh326/kite/pkg/rbac"
)

// ValidateAPIKeyToken validates a Kite API key in the form kite<id>-<key> (or
// <id>-<key>) and returns the corresponding user with roles populated.
func ValidateAPIKeyToken(token string) (*model.User, error) {
	id, key, ok := strings.Cut(strings.TrimPrefix(token, "kite"), "-")
	if !ok || id == "" || key == "" {
		return nil, fmt.Errorf("invalid API key")
	}
	dbID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid API key")
	}
	apikey, err := model.GetUserByIDCached(dbID)
	if err != nil {
		return nil, fmt.Errorf("invalid API key")
	}
	if apikey.Provider != common.APIKeyProvider || len(apikey.APIKey) == 0 {
		return nil, fmt.Errorf("invalid API key")
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(apikey.APIKey)) != 1 {
		return nil, fmt.Errorf("invalid API key")
	}
	if !apikey.Enabled {
		return nil, fmt.Errorf("user disabled")
	}
	apikey.Roles = rbac.GetUserRoles(*apikey)
	return apikey, nil
}

// safeRedirect returns raw only when it is a local Kite OAuth authorize URL.
// Restricting the path also prevents backslash normalization open redirects
// (for example "/\evil.com" becoming "//evil.com" in a browser).
func safeRedirect(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.ContainsAny(raw, "\\\r\n\x00") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(raw, common.Base+"/api/oauth/authorize") {
		return ""
	}
	return raw
}
