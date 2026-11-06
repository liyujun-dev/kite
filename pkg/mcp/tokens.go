package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zxh326/kite/pkg/common"
)

const (
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour

	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"
)

type tokenClaims struct {
	UserID    uint   `json:"uid"`
	ClientID  string `json:"client_id"`
	Scope     string `json:"scope"`
	TokenType string `json:"typ"`
	Family    string `json:"family,omitempty"`
	jwt.RegisteredClaims
}

// TokenIssuer signs and verifies MCP OAuth tokens. The signing key is derived from
// common.JwtSecret so MCP tokens and Kite session JWTs cannot be used interchangeably.
type TokenIssuer struct {
	key []byte
}

func NewTokenIssuer() TokenIssuer {
	mac := hmac.New(sha256.New, []byte(common.JwtSecret))
	mac.Write([]byte("kite-mcp-oauth-v1"))
	return TokenIssuer{key: mac.Sum(nil)}
}

func (ti TokenIssuer) Issue(userID uint, clientID, issuer, resource, tokenType, familyID, tokenID string, ttl time.Duration) (string, error) {
	if tokenID == "" {
		tokenID = randomID(16)
	}
	now := time.Now()
	claims := tokenClaims{
		UserID:    userID,
		ClientID:  clientID,
		Scope:     mcpScope,
		TokenType: tokenType,
		Family:    familyID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Audience:  jwt.ClaimStrings{resource},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        tokenID,
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(ti.key)
}

func (ti TokenIssuer) Validate(token, expectedType, issuer, resource string) (*tokenClaims, error) {
	var claims tokenClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return ti.key, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(resource),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if claims.TokenType != expectedType {
		return nil, fmt.Errorf("unexpected token type")
	}
	return &claims, nil
}

// signConsent signs an arbitrary payload with the MCP token key.
func (ti TokenIssuer) signConsent(payload []byte) string {
	mac := hmac.New(sha256.New, ti.key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (ti TokenIssuer) verifyConsent(blob string) ([]byte, error) {
	payloadPart, sigPart, ok := strings.Cut(blob, ".")
	if !ok {
		return nil, fmt.Errorf("invalid consent token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, fmt.Errorf("invalid consent token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return nil, fmt.Errorf("invalid consent token")
	}
	mac := hmac.New(sha256.New, ti.key)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, fmt.Errorf("invalid consent signature")
	}
	return payload, nil
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failure: %v", err))
	}
	return hex.EncodeToString(b)
}

func randomCode() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failure: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
