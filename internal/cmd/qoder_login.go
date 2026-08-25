package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoQoderLogin handles the Qoder device flow using the shared authentication manager.
// It initiates the device-based authentication process for Qoder services and saves
// the authentication tokens to the configured auth directory.
//
// Parameters:
//   - cfg: The application configuration
//   - options: Login options including browser behavior and prompts
func DoQoderLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	manager := newAuthManager()

	promptFn := options.Prompt
	if promptFn == nil && !options.QoderOAuth && strings.TrimSpace(options.QoderPAT) == "" {
		promptFn = func(prompt string) (string, error) {
			fmt.Println()
			fmt.Println(prompt)
			var value string
			_, err := fmt.Scanln(&value)
			return value, err
		}
	}

	metadata := map[string]string{}
	if options != nil && options.QoderOAuth {
		metadata["oauth"] = "1"
	}
	if options != nil && strings.TrimSpace(options.QoderPAT) != "" {
		metadata["pat"] = strings.TrimSpace(options.QoderPAT)
	}

	authOpts := &sdkAuth.LoginOptions{
		NoBrowser:    options.NoBrowser,
		CallbackPort: options.CallbackPort,
		Metadata:     metadata,
		Prompt:       promptFn,
	}

	_, savedPath, err := manager.Login(context.Background(), "qoder", cfg, authOpts)
	if err != nil {
		if emailErr, ok := errors.AsType[*sdkAuth.EmailRequiredError](err); ok {
			log.Error(emailErr.Error())
			return
		}
		fmt.Printf("Qoder authentication failed: %v\n", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}

	fmt.Println("Qoder authentication successful!")
}
