package workbuddy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
)

const (
	workBuddyStatePath   = "/v2/plugin/auth/state"
	workBuddyTokenPath   = "/v2/plugin/auth/token"
	workBuddyRefreshPath = "/v2/plugin/auth/token/refresh"
	pollInterval         = 5 * time.Second
	maxPollDuration      = 5 * time.Minute
	codeLoginPending     = 11217
	codeSuccess          = 0
)

type WorkBuddyAuth struct {
	httpClient *http.Client
	cfg        *config.Config
	baseURL    string
	site       Site
}

func NewWorkBuddyAuth(cfg *config.Config) *WorkBuddyAuth {
	return NewWorkBuddyAuthWithProxyURL(cfg, "")
}

func NewWorkBuddyAuthForSite(cfg *config.Config, site Site) *WorkBuddyAuth {
	return newWorkBuddyAuth(cfg, "", site)
}

// NewWorkBuddyAuthWithProxyURL creates a WorkBuddy auth helper with an explicit proxy URL.
// proxyURL takes precedence over cfg.ProxyURL when non-empty.
func NewWorkBuddyAuthWithProxyURL(cfg *config.Config, proxyURL string) *WorkBuddyAuth {
	return newWorkBuddyAuth(cfg, proxyURL, SiteCN())
}

func newWorkBuddyAuth(cfg *config.Config, proxyURL string, site Site) *WorkBuddyAuth {
	if site.APIBaseURL == "" {
		site = SiteCN()
	}
	effectiveProxyURL := strings.TrimSpace(proxyURL)
	var sdkCfg config.SDKConfig
	if cfg != nil {
		sdkCfg = cfg.SDKConfig
		if effectiveProxyURL == "" {
			effectiveProxyURL = strings.TrimSpace(cfg.ProxyURL)
		}
	}
	sdkCfg.ProxyURL = effectiveProxyURL
	httpClient := util.SetProxy(&sdkCfg, &http.Client{Timeout: 30 * time.Second})
	return &WorkBuddyAuth{httpClient: httpClient, cfg: cfg, baseURL: site.APIBaseURL, site: site}
}

// AuthState holds the state and auth URL returned by the auth state API.
type AuthState struct {
	State   string
	AuthURL string
}

// FetchAuthState calls POST /v2/plugin/auth/state?platform=... to get the state and login URL.
// International uses platform=workbuddy-ai; China uses platform=workbuddy.
func (a *WorkBuddyAuth) FetchAuthState(ctx context.Context) (*AuthState, error) {
	stateURL := fmt.Sprintf("%s%s?platform=%s", a.baseURL, workBuddyStatePath, url.QueryEscape(a.platform()))
	body := []byte("{}")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stateURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("workbuddy: failed to create auth state request: %w", err)
	}

	a.applyAnonymousAuthHeaders(req, true)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: auth state request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy auth state: close body error: %v", errClose)
		}
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: failed to read auth state response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workbuddy: auth state request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			State   string `json:"state"`
			AuthURL string `json:"authUrl"`
		} `json:"data"`
	}
	if err = json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("workbuddy: failed to parse auth state response: %w", err)
	}
	if result.Code != codeSuccess {
		return nil, fmt.Errorf("workbuddy: auth state request failed with code %d: %s", result.Code, result.Msg)
	}
	if result.Data == nil || result.Data.State == "" || result.Data.AuthURL == "" {
		return nil, fmt.Errorf("workbuddy: auth state response missing state or authUrl")
	}

	return &AuthState{
		State:   result.Data.State,
		AuthURL: result.Data.AuthURL,
	}, nil
}

type pollResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	RequestID string `json:"requestId"`
	Data      *struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		TokenType    string `json:"tokenType"`
		Domain       string `json:"domain"`
	} `json:"data"`
}

// doPollRequest performs a single polling request, safely reading and closing the response body
func (a *WorkBuddyAuth) doPollRequest(ctx context.Context, pollURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrTokenFetchFailed, err)
	}
	a.applyPollHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy poll: close body error: %v", errClose)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("workbuddy poll: failed to read response body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// PollForToken polls until the user completes browser authorization and returns auth data.
func (a *WorkBuddyAuth) PollForToken(ctx context.Context, state string) (*WorkBuddyTokenStorage, error) {
	deadline := time.Now().Add(maxPollDuration)
	pollURL := fmt.Sprintf("%s%s?state=%s", a.baseURL, workBuddyTokenPath, url.QueryEscape(state))

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}

		body, statusCode, err := a.doPollRequest(ctx, pollURL)
		if err != nil {
			log.Debugf("workbuddy poll: request error: %v", err)
			continue
		}

		if statusCode != http.StatusOK {
			log.Debugf("workbuddy poll: unexpected status %d", statusCode)
			continue
		}

		var result pollResponse
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}

		switch result.Code {
		case codeSuccess:
			if result.Data == nil {
				return nil, fmt.Errorf("%w: empty data in response", ErrTokenFetchFailed)
			}
			userID, _ := a.DecodeUserID(result.Data.AccessToken)
			domain := strings.TrimSpace(result.Data.Domain)
			if domain == "" {
				domain = a.tokenDomainFallback()
			}
			return &WorkBuddyTokenStorage{
				AccessToken:  result.Data.AccessToken,
				RefreshToken: result.Data.RefreshToken,
				ExpiresIn:    result.Data.ExpiresIn,
				TokenType:    result.Data.TokenType,
				Domain:       domain,
				UserID:       userID,
				Type:         "workbuddy",
			}, nil
		case codeLoginPending:
			// continue polling
		default:
			// TODO: when the WorkBuddy API error code for user denial is known,
			// return ErrAccessDenied here instead of ErrTokenFetchFailed.
			return nil, fmt.Errorf("%w: server returned code %d: %s", ErrTokenFetchFailed, result.Code, result.Msg)
		}
	}
	return nil, ErrPollingTimeout
}

// DecodeUserID decodes the sub field from a JWT access token as the user ID.
func (a *WorkBuddyAuth) DecodeUserID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return "", ErrJWTDecodeFailed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrJWTDecodeFailed, err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("%w: %v", ErrJWTDecodeFailed, err)
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("%w: sub claim is empty", ErrJWTDecodeFailed)
	}
	return claims.Sub, nil
}

// RefreshToken exchanges a refresh token for a new access token.
// It calls POST /v2/plugin/auth/token/refresh with the required headers.
func (a *WorkBuddyAuth) RefreshToken(ctx context.Context, accessToken, refreshToken, userID, domain string) (*WorkBuddyTokenStorage, error) {
	if domain == "" {
		domain = DefaultDomain
	}
	refreshURL := fmt.Sprintf("%s%s", a.resolvedBaseURL(domain), workBuddyRefreshPath)
	body := []byte("{}")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("workbuddy: failed to create refresh request: %w", err)
	}

	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Domain", domain)
	req.Header.Set("X-Refresh-Token", refreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	req.Header.Set("X-Request-ID", newWorkBuddyRequestID())
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("User-Agent", UserAgentForAuth(domain))

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: refresh request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy refresh: close body error: %v", errClose)
		}
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: failed to read refresh response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("workbuddy: refresh token rejected (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workbuddy: refresh failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			AccessToken      string `json:"accessToken"`
			RefreshToken     string `json:"refreshToken"`
			ExpiresIn        int64  `json:"expiresIn"`
			RefreshExpiresIn int64  `json:"refreshExpiresIn"`
			TokenType        string `json:"tokenType"`
			Domain           string `json:"domain"`
		} `json:"data"`
	}
	if err = json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("workbuddy: failed to parse refresh response: %w", err)
	}
	if result.Code != codeSuccess {
		return nil, fmt.Errorf("workbuddy: refresh failed with code %d: %s", result.Code, result.Msg)
	}
	if result.Data == nil {
		return nil, fmt.Errorf("workbuddy: empty data in refresh response")
	}

	newUserID, _ := a.DecodeUserID(result.Data.AccessToken)
	if newUserID == "" {
		newUserID = userID
	}
	tokenDomain := result.Data.Domain
	if tokenDomain == "" {
		tokenDomain = domain
	}

	return &WorkBuddyTokenStorage{
		AccessToken:      result.Data.AccessToken,
		RefreshToken:     result.Data.RefreshToken,
		ExpiresIn:        result.Data.ExpiresIn,
		RefreshExpiresIn: result.Data.RefreshExpiresIn,
		TokenType:        result.Data.TokenType,
		Domain:           tokenDomain,
		UserID:           newUserID,
		Type:             "workbuddy",
	}, nil
}

func (a *WorkBuddyAuth) applyPollHeaders(req *http.Request) {
	a.applyAnonymousAuthHeaders(req, false)
}

func (a *WorkBuddyAuth) applyAnonymousAuthHeaders(req *http.Request, includeDomain bool) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", a.userAgent())
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-No-Authorization", "true")
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-Request-ID", newWorkBuddyRequestID())
	if includeDomain {
		req.Header.Set("X-Domain", a.headerDomain())
	}
}

func (a *WorkBuddyAuth) platform() string {
	if a != nil && strings.TrimSpace(a.site.Platform) != "" {
		return a.site.Platform
	}
	return PlatformCN
}

func (a *WorkBuddyAuth) userAgent() string {
	if a != nil && strings.TrimSpace(a.site.UserAgent) != "" {
		return a.site.UserAgent
	}
	return UserAgentAuthCN
}

func (a *WorkBuddyAuth) headerDomain() string {
	if a != nil && strings.TrimSpace(a.site.HeaderDomain) != "" {
		return a.site.HeaderDomain
	}
	return "www.workbuddy.cn"
}

func (a *WorkBuddyAuth) tokenDomainFallback() string {
	if a != nil && strings.TrimSpace(a.site.TokenDomain) != "" {
		return a.site.TokenDomain
	}
	return DefaultDomain
}

func (a *WorkBuddyAuth) resolvedBaseURL(domain string) string {
	if a != nil && a.baseURL != "" && a.baseURL != BaseURLCN && a.baseURL != BaseURLGlobal {
		return a.baseURL
	}
	return APIBaseURLForDomain(domain)
}

func newWorkBuddyRequestID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
