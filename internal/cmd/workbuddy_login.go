package cmd

import (
	"context"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoWorkBuddyLogin triggers the browser OAuth polling flow for WorkBuddy and saves tokens.
func DoWorkBuddyLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser: options.NoBrowser,
		Metadata:  map[string]string{},
	}
	if region := options.WorkBuddyRegion; region != "" {
		authOpts.Metadata["region"] = region
	}

	record, savedPath, err := manager.Login(context.Background(), "workbuddy", cfg, authOpts)
	if err != nil {
		log.Errorf("WorkBuddy authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("WorkBuddy authentication successful!")
}
