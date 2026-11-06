package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/zxh326/kite/pkg/common"
)

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"oauth", "ldap", "oauth", "password", "ldap"})
	want := []string{"oauth", "ldap", "password"}

	if len(got) != len(want) {
		t.Fatalf("uniqueStrings() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uniqueStrings() got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsCredentialFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "invalid credentials", err: errInvalidCredentials, want: true},
		{name: "ldap invalid credentials", err: ErrLDAPInvalidCredentials, want: true},
		{name: "ldap disabled", err: ErrLDAPDisabled, want: true},
		{name: "ldap not configured", err: ErrLDAPNotConfigured, want: true},
		{name: "wrapped invalid credentials", err: fmt.Errorf("wrap: %w", errInvalidCredentials), want: true},
		{name: "other error", err: errors.New("other"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCredentialFailure(tt.err); got != tt.want {
				t.Fatalf("isCredentialFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSafeRedirect(t *testing.T) {
	previousBase := common.Base
	common.Base = ""
	defer func() { common.Base = previousBase }()

	valid := []string{
		"/api/oauth/authorize?client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A1234%2Fcb",
	}
	for _, value := range valid {
		if got := safeRedirect(value); got != value {
			t.Errorf("safeRedirect(%q) = %q, want it unchanged", value, got)
		}
	}

	invalid := []string{
		"",
		"https://evil.example/",
		"//evil.example/",
		"/\\evil.example/",
		"/api/oauth/authorize\r\nLocation: https://evil.example",
		"/login",
		"/api/oauth/token",
	}
	for _, value := range invalid {
		if got := safeRedirect(value); got != "" {
			t.Errorf("safeRedirect(%q) = %q, want empty", value, got)
		}
	}
}

func TestSafeRedirectWithBasePath(t *testing.T) {
	previousBase := common.Base
	common.Base = "/kite"
	defer func() { common.Base = previousBase }()

	if got := safeRedirect("/kite/api/oauth/authorize?x=1"); got != "/kite/api/oauth/authorize?x=1" {
		t.Errorf("base-prefixed authorize URL rejected: %q", got)
	}
	if got := safeRedirect("/api/oauth/authorize?x=1"); got != "" {
		t.Errorf("URL outside the base path should be rejected, got %q", got)
	}
}
