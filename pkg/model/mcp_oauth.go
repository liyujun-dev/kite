package model

import (
	"crypto/subtle"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrMCPRefreshTokenReused is returned when a refresh token is presented more than once.
var ErrMCPRefreshTokenReused = errors.New("mcp refresh token reused")

// JSONStringSlice stores a string slice as a JSON array in a text column.
// SliceString cannot be used for URLs because it joins values with commas.
type JSONStringSlice []string

func (s *JSONStringSlice) Scan(value interface{}) error {
	if value == nil {
		*s = nil
		return nil
	}

	var data []byte
	switch v := value.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("cannot scan %T into JSONStringSlice", value)
	}

	if len(data) == 0 {
		*s = nil
		return nil
	}
	return json.Unmarshal(data, s)
}

func (s JSONStringSlice) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

// MCPOAuthClient is an OAuth client registered with Kite through dynamic client registration.
type MCPOAuthClient struct {
	Model
	ClientID                string          `json:"client_id" gorm:"type:varchar(128);uniqueIndex;not null"`
	ClientSecretHash        string          `json:"-" gorm:"type:varchar(128)"`
	Name                    string          `json:"client_name" gorm:"type:varchar(255)"`
	RedirectURIs            JSONStringSlice `json:"redirect_uris" gorm:"type:text"`
	GrantTypes              JSONStringSlice `json:"grant_types" gorm:"type:text"`
	ResponseTypes           JSONStringSlice `json:"response_types" gorm:"type:text"`
	Scope                   string          `json:"scope" gorm:"type:varchar(255)"`
	TokenEndpointAuthMethod string          `json:"token_endpoint_auth_method" gorm:"type:varchar(32)"`
	CreatedBy               uint            `json:"-"`
}

// MCPOAuthCode is a single-use authorization code issued by the authorize endpoint.
type MCPOAuthCode struct {
	Model
	CodeHash            string    `json:"-" gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientID            string    `json:"client_id" gorm:"type:varchar(128);index;not null"`
	UserID              uint      `json:"user_id" gorm:"index;not null"`
	RedirectURI         string    `json:"redirect_uri" gorm:"type:text"`
	Scope               string    `json:"scope" gorm:"type:varchar(255)"`
	CodeChallenge       string    `json:"-" gorm:"type:varchar(255)"`
	CodeChallengeMethod string    `json:"-" gorm:"type:varchar(16)"`
	ExpiresAt           time.Time `json:"-" gorm:"index"`
	Used                bool      `json:"-" gorm:"type:boolean;default:false"`
}

func CreateMCPOAuthClient(client *MCPOAuthClient) error {
	return DB.Create(client).Error
}

func GetMCPOAuthClient(clientID string) (*MCPOAuthClient, error) {
	var client MCPOAuthClient
	if err := DB.Where("client_id = ?", clientID).First(&client).Error; err != nil {
		return nil, err
	}
	return &client, nil
}

func CreateMCPOAuthCode(code *MCPOAuthCode) error {
	return DB.Create(code).Error
}

// ConsumeMCPOAuthCode atomically marks an unused, unexpired code as used and returns it.
func ConsumeMCPOAuthCode(codeHash string) (*MCPOAuthCode, error) {
	var code MCPOAuthCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("code_hash = ? AND used = ? AND expires_at > ?", codeHash, false, time.Now()).First(&code).Error; err != nil {
			return err
		}
		res := tx.Model(&MCPOAuthCode{}).Where("id = ? AND used = ?", code.ID, false).Update("used", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &code, nil
}

func DeleteExpiredMCPOAuthCodes() error {
	return DB.Where("expires_at <= ?", time.Now()).Delete(&MCPOAuthCode{}).Error
}

// MCPOAuthRefreshToken tracks a refresh token so rotation and reuse can be detected.
type MCPOAuthRefreshToken struct {
	Model
	TokenID   string    `json:"-" gorm:"type:varchar(64);uniqueIndex;not null"`
	TokenHash string    `json:"-" gorm:"type:varchar(64);not null"`
	FamilyID  string    `json:"-" gorm:"type:varchar(64);index;not null"`
	ClientID  string    `json:"client_id" gorm:"type:varchar(128);index;not null"`
	UserID    uint      `json:"user_id" gorm:"index;not null"`
	Scope     string    `json:"scope" gorm:"type:varchar(255)"`
	ExpiresAt time.Time `json:"-" gorm:"index"`
	Used      bool      `json:"-" gorm:"type:boolean;default:false"`
	Revoked   bool      `json:"-" gorm:"type:boolean;default:false"`
}

func CreateMCPOAuthRefreshToken(token *MCPOAuthRefreshToken) error {
	return DB.Create(token).Error
}

// ConsumeMCPOAuthRefreshToken atomically marks an unused refresh token as used.
// Presenting an already-used or revoked token revokes the whole token family.
func ConsumeMCPOAuthRefreshToken(tokenID, tokenHash string) (*MCPOAuthRefreshToken, error) {
	var token MCPOAuthRefreshToken
	reused := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("token_id = ?", tokenID).First(&token).Error; err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(token.TokenHash)) != 1 {
			return gorm.ErrRecordNotFound
		}
		if token.Used || token.Revoked || time.Now().After(token.ExpiresAt) {
			reused = true
			return nil
		}
		res := tx.Model(&MCPOAuthRefreshToken{}).Where("id = ? AND used = ? AND revoked = ?", token.ID, false, false).Update("used", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			reused = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if reused {
		// Revoke outside the transaction: returning an error from the transaction
		// callback would roll the revocation back.
		_ = RevokeMCPOAuthRefreshTokenFamily(token.FamilyID)
		return nil, ErrMCPRefreshTokenReused
	}
	return &token, nil
}

func RevokeMCPOAuthRefreshTokenFamily(familyID string) error {
	return DB.Model(&MCPOAuthRefreshToken{}).Where("family_id = ?", familyID).Update("revoked", true).Error
}

func DeleteExpiredMCPOAuthTokens() error {
	if err := DeleteExpiredMCPOAuthCodes(); err != nil {
		return err
	}
	return DB.Where("expires_at <= ?", time.Now()).Delete(&MCPOAuthRefreshToken{}).Error
}
