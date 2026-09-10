package auth

import (
	"context"
	"fmt"
	"time"

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
