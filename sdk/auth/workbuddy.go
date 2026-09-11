package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// WorkBuddyAuthenticator implements the browser OAuth polling flow for WorkBuddy.
type WorkBuddyAuthenticator struct{}

// NewWorkBuddyAuthenticator constructs a new WorkBuddy authenticator.
func NewWorkBuddyAuthenticator() Authenticator {
	return &WorkBuddyAuthenticator{}
}

// Provider returns the provider key for workbuddy.
func (WorkBuddyAuthenticator) Provider() string {
	return "workbuddy"
}

var workBuddyRefreshLead = 24 * time.Hour

// RefreshLead returns how soon before expiry a refresh should be attempted.
func (WorkBuddyAuthenticator) RefreshLead() *time.Duration {
	return &workBuddyRefreshLead
}

// Login initiates the browser OAuth flow for WorkBuddy.
func (a WorkBuddyAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("workbuddy: configuration is required")
	}
	if opts == nil {
		opts = &LoginOptions{}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	region := ""
	if opts.Metadata != nil {
		region = opts.Metadata["region"]
	}
	site, err := workbuddy.ParseSite(region)
	if err != nil {
		return nil, err
	}

	authSvc := workbuddy.NewWorkBuddyAuthForSite(cfg, site)

	authState, err := authSvc.FetchAuthState(ctx)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: failed to fetch auth state: %w", err)
	}

	fmt.Printf("\nPlease open the following URL in your browser to login (%s):\n\n  %s\n\n", site.Name, authState.AuthURL)
	fmt.Println("Waiting for authorization...")

	if !opts.NoBrowser {
		if browser.IsAvailable() {
			if errOpen := browser.OpenURL(authState.AuthURL); errOpen != nil {
				log.Debugf("workbuddy: failed to open browser: %v", errOpen)
			}
		}
	}

	storage, err := authSvc.PollForToken(ctx, authState.State)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: %s: %w", workbuddy.GetUserFriendlyMessage(err), err)
	}

	fmt.Printf("\nSuccessfully logged in! (User ID: %s)\n", storage.UserID)

	authID := fmt.Sprintf("workbuddy-%s.json", storage.UserID)
	label := storage.UserID
	if label == "" {
		label = "workbuddy-user"
	}

	return &coreauth.Auth{
		ID:       authID,
		Provider: a.Provider(),
		FileName: authID,
		Label:    label,
		Storage:  storage,
		Metadata: map[string]any{
			"access_token":  storage.AccessToken,
			"refresh_token": storage.RefreshToken,
			"user_id":       storage.UserID,
			"domain":        storage.Domain,
			"expires_in":    storage.ExpiresIn,
			"region":        site.Name,
		},
	}, nil
}

// MigrateLegacyWorkBuddyAuth rewrites type:"codebuddy" records whose domain
// belongs to WorkBuddy so they bind to the WorkBuddy executor and persist as
// WorkBuddy credentials. Returns true when the record was changed.
func MigrateLegacyWorkBuddyAuth(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	providerCodeBuddy := strings.EqualFold(strings.TrimSpace(auth.Provider), "codebuddy")
	typeCodeBuddy := false
	if auth.Metadata != nil {
		rawType, _ := auth.Metadata["type"].(string)
		typeCodeBuddy = strings.EqualFold(strings.TrimSpace(rawType), "codebuddy")
	}
	if !providerCodeBuddy && !typeCodeBuddy {
		return false
	}
	if !workbuddy.IsWorkBuddyDomain(legacyWorkBuddyDomain(auth)) {
		return false
	}
	auth.Provider = "workbuddy"
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata["type"] = "workbuddy"
	if cb, ok := auth.Storage.(*codebuddy.CodeBuddyTokenStorage); ok && cb != nil {
		auth.Storage = workBuddyStorageFromCodeBuddy(cb)
	}
	log.Infof("workbuddy: migrated legacy CodeBuddy credential %s", auth.ID)
	return true
}

func legacyWorkBuddyDomain(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		if domain, _ := auth.Metadata["domain"].(string); strings.TrimSpace(domain) != "" {
			return domain
		}
	}
	switch storage := auth.Storage.(type) {
	case *codebuddy.CodeBuddyTokenStorage:
		if storage != nil {
			return storage.Domain
		}
	case *workbuddy.WorkBuddyTokenStorage:
		if storage != nil {
			return storage.Domain
		}
	}
	return ""
}

func workBuddyStorageFromCodeBuddy(cb *codebuddy.CodeBuddyTokenStorage) *workbuddy.WorkBuddyTokenStorage {
	if cb == nil {
		return nil
	}
	return &workbuddy.WorkBuddyTokenStorage{
		AccessToken:      cb.AccessToken,
		RefreshToken:     cb.RefreshToken,
		ExpiresIn:        cb.ExpiresIn,
		RefreshExpiresIn: cb.RefreshExpiresIn,
		TokenType:        cb.TokenType,
		Domain:           cb.Domain,
		UserID:           cb.UserID,
		Type:             "workbuddy",
	}
}
