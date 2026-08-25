package qoder

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const (
	qoderAppCode = "cosy"
	qoderAppSecret = "d2FyLCB3YXIgbmV2ZXIgY2hhbmdlcw==" // base64("war, war never changes")
	qoderSignSep = "&"
)

var (
	jobTokenEndpoint          = QoderCenterBase + "/algo/api/v3/user/jobToken?Encode=1"
	openAPIJobTokenEndpoint   = QoderOpenAPIBase + "/api/v1/jobToken/exchange"
	openAPIQuotaUsageEndpoint = QoderOpenAPIBase + "/api/v2/quota/usage"
)

// JobTokenSession is the COSY session returned by /algo/api/v3/user/jobToken.
type JobTokenSession struct {
	UserID             string
	Name               string
	Email              string
	SecurityOauthToken string
	RefreshToken       string
	ExpireTime         int64
	UserType           string
	Plan               string
}

func jobTokenDate() string {
	return time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
}

func jobTokenSignature(date string) string {
	sum := md5.Sum([]byte(qoderAppCode + qoderSignSep + qoderAppSecret + qoderSignSep + date))
	return hex.EncodeToString(sum[:])
}

// ExchangeJobToken swaps a PAT for a COSY session via the signed jobToken endpoint.
func (qa *QoderAuth) ExchangeJobToken(ctx context.Context, pat, machineID, machineToken, machineType string) (*JobTokenSession, error) {
	return qa.postJobToken(ctx, pat, "", "", false, machineID, machineToken, machineType)
}

// RefreshJobToken renews a PAT session, sending the current tokens when available.
func (qa *QoderAuth) RefreshJobToken(ctx context.Context, pat, securityOauthToken, refreshToken, machineID, machineToken, machineType string) (*JobTokenSession, error) {
	return qa.postJobToken(ctx, pat, securityOauthToken, refreshToken, true, machineID, machineToken, machineType)
}

func (qa *QoderAuth) postJobToken(ctx context.Context, pat, securityOauthToken, refreshToken string, needRefresh bool, machineID, machineToken, machineType string) (*JobTokenSession, error) {
	if qa == nil || qa.httpClient == nil {
		return nil, fmt.Errorf("qoder: auth client is nil")
	}
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, fmt.Errorf("qoder: empty PAT")
	}
	if machineID == "" {
		machineID = generateMachineID()
	}
	if machineToken == "" {
		machineToken = machineID
	}
	if machineType == "" {
		machineType = QoderMachineTypeMagic
	}

	inner := map[string]interface{}{
		"personalToken":      pat,
		"securityOauthToken": securityOauthToken,
		"refreshToken":       refreshToken,
		"needRefresh":        needRefresh,
		"authInfo":           map[string]interface{}{},
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("qoder jobToken: marshal inner: %w", err)
	}
	outer, err := json.Marshal(map[string]interface{}{
		"payload":       string(innerJSON),
		"encodeVersion": "1",
	})
	if err != nil {
		return nil, fmt.Errorf("qoder jobToken: marshal outer: %w", err)
	}
	encoded := EncodeRequestBody(outer)

	url := jobTokenEndpoint
	if qa.jobTokenURL != "" {
		url = qa.jobTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(encoded))
	if err != nil {
		return nil, fmt.Errorf("qoder jobToken: build request: %w", err)
	}
	date := jobTokenDate()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("appcode", qoderAppCode)
	req.Header.Set("date", date)
	req.Header.Set("signature", jobTokenSignature(date))
	req.Header.Set("login-version", QoderLoginVersion)
	req.Header.Set("cosy-version", QoderIDEVersion)
	req.Header.Set("cosy-clienttype", QoderClientType)
	req.Header.Set("cosy-machineid", machineID)
	req.Header.Set("cosy-machinetoken", machineToken)
	req.Header.Set("cosy-machinetype", machineType)
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := qa.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder jobToken: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qoder jobToken: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qoder jobToken: HTTP %d body=%s", resp.StatusCode, truncateForLog(string(body), 300))
	}
	sess, err := parseJobTokenSession(body)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func parseJobTokenSession(body []byte) (*JobTokenSession, error) {
	root := gjson.ParseBytes(body)
	src := root
	if !hasJobTokenFields(src) && src.Get("data").IsObject() {
		src = src.Get("data")
	}
	token := firstGJSONString(src, "securityOauthToken", "security_oauth_token", "token")
	if token == "" {
		return nil, fmt.Errorf("qoder jobToken: missing securityOauthToken")
	}
	userID := firstGJSONString(src, "id", "user_id", "userId", "uid")
	expire := src.Get("expireTime")
	if !expire.Exists() {
		expire = src.Get("expire_time")
	}
	expireMs := int64(0)
	switch {
	case expire.Type == gjson.Number:
		expireMs = expire.Int()
		if expireMs > 0 && expireMs < 1e11 {
			expireMs *= 1000
		}
	case expire.Type == gjson.String:
		expireMs = parseExpiresAt(expire.String(), 0)
	}
	return &JobTokenSession{
		UserID:             userID,
		Name:               firstGJSONString(src, "name"),
		Email:              firstGJSONString(src, "email"),
		SecurityOauthToken: token,
		RefreshToken:       firstGJSONString(src, "refreshToken", "refresh_token"),
		ExpireTime:         expireMs,
		UserType:           firstGJSONString(src, "userType", "user_type"),
		Plan:               firstGJSONString(src, "plan"),
	}, nil
}

func hasJobTokenFields(src gjson.Result) bool {
	return firstGJSONString(src, "securityOauthToken", "security_oauth_token", "token") != ""
}

func firstGJSONString(src gjson.Result, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(src.Get(key).String()); v != "" {
			return v
		}
	}
	return ""
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// LoginWithPAT exchanges a PAT for a persisted QoderTokenStorage.
func (qa *QoderAuth) LoginWithPAT(ctx context.Context, pat string) (*QoderTokenStorage, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, fmt.Errorf("qoder: empty PAT")
	}
	if !strings.HasPrefix(pat, "pt-") {
		return nil, fmt.Errorf("qoder: PAT must start with pt-")
	}
	machineID := generateMachineID()
	sess, err := qa.ExchangeJobToken(ctx, pat, machineID, machineID, QoderMachineTypeMagic)
	if err != nil {
		return nil, err
	}
	storage := applyJobTokenSession(&QoderTokenStorage{
		AuthMode:      AuthModePAT,
		PersonalToken: pat,
		MachineID:     machineID,
		MachineToken:  machineID,
		MachineType:   QoderMachineTypeMagic,
		Type:          "qoder",
	}, sess)
	name, email := qa.SaveUserInfo(ctx, storage.Token, storage.UserID, storage.Name, storage.Email)
	if name != "" {
		storage.Name = name
	}
	if email != "" {
		storage.Email = email
	}
	if strings.TrimSpace(storage.Email) == "" {
		if storage.UserID != "" {
			storage.Email = storage.UserID
		} else {
			storage.Email = fmt.Sprintf("pat-%d", time.Now().UnixMilli())
		}
	}
	log.Infof("qoder: PAT login succeeded for %s (%s)", storage.Name, MaskPAT(pat))
	return storage, nil
}

func applyJobTokenSession(storage *QoderTokenStorage, sess *JobTokenSession) *QoderTokenStorage {
	if storage == nil || sess == nil {
		return storage
	}
	storage.AuthMode = AuthModePAT
	storage.Token = sess.SecurityOauthToken
	if sess.RefreshToken != "" {
		storage.RefreshToken = sess.RefreshToken
	}
	if sess.UserID != "" {
		storage.UserID = sess.UserID
	}
	if sess.Name != "" {
		storage.Name = sess.Name
	}
	if sess.Email != "" {
		storage.Email = sess.Email
	}
	if sess.ExpireTime > 0 {
		storage.ExpireTime = sess.ExpireTime
	}
	storage.LastRefresh = time.Now().Format(time.RFC3339)
	storage.Type = "qoder"
	return storage
}

// RefreshPATSession rebuilds the COSY session for a PAT credential and writes
// the updated storage back to authFilePath when the path is non-empty.
func RefreshPATSession(ctx context.Context, cfg *config.Config, storage *QoderTokenStorage, authFilePath string) error {
	if storage == nil || !storage.IsPAT() {
		return fmt.Errorf("qoder: not a PAT credential")
	}
	if strings.TrimSpace(storage.PersonalToken) == "" {
		return fmt.Errorf("qoder: PAT credential is missing personal_token")
	}
	auth := NewQoderAuth(cfg)
	machineID := storage.MachineID
	if machineID == "" {
		machineID = generateMachineID()
		storage.MachineID = machineID
	}
	machineToken := storage.MachineToken
	if machineToken == "" {
		machineToken = machineID
		storage.MachineToken = machineToken
	}
	machineType := storage.MachineType
	if machineType == "" {
		machineType = QoderMachineTypeMagic
		storage.MachineType = machineType
	}
	sess, err := auth.RefreshJobToken(ctx, storage.PersonalToken, storage.Token, storage.RefreshToken, machineID, machineToken, machineType)
	if err != nil {
		log.Debugf("qoder: PAT refresh with existing session failed (%v); exchanging from scratch", err)
		sess, err = auth.ExchangeJobToken(ctx, storage.PersonalToken, machineID, machineToken, machineType)
		if err != nil {
			return err
		}
	}
	applyJobTokenSession(storage, sess)
	if authFilePath == "" {
		return nil
	}
	return storage.SaveTokenToFile(authFilePath)
}

// ExchangeOpenAPIJobToken swaps a PAT for a jt- token used as Bearer on openapi quota APIs.
func (qa *QoderAuth) ExchangeOpenAPIJobToken(ctx context.Context, pat string) (string, error) {
	if qa == nil || qa.httpClient == nil {
		return "", fmt.Errorf("qoder: auth client is nil")
	}
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return "", fmt.Errorf("qoder: empty PAT")
	}
	body, err := json.Marshal(map[string]string{"personal_token": pat})
	if err != nil {
		return "", err
	}
	url := openAPIJobTokenEndpoint
	if qa.openAPIExchangeURL != "" {
		url = qa.openAPIExchangeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "qoder/1.0.0")
	req.Header.Set("cosy-version", QoderIDEVersion)
	req.Header.Set("cosy-clienttype", QoderClientType)
	resp, err := qa.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qoder openapi jobToken/exchange: HTTP %d body=%s", resp.StatusCode, truncateForLog(string(raw), 200))
	}
	token := gjson.GetBytes(raw, "token").String()
	if token == "" {
		token = gjson.GetBytes(raw, "data.token").String()
	}
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("qoder openapi jobToken/exchange: no token in response")
	}
	return strings.TrimSpace(token), nil
}

func PATAuthFileName(email, pat string) string {
	label := strings.TrimSpace(email)
	if label == "" {
		label = "user"
	}
	label = strings.ReplaceAll(label, "/", "_")
	suffix := patSuffix(pat)
	if suffix == "" {
		return fmt.Sprintf("qoder-%s-pat.json", label)
	}
	return fmt.Sprintf("qoder-%s-pat-%s.json", label, suffix)
}

func patSuffix(pat string) string {
	pat = strings.TrimSpace(pat)
	if len(pat) < 4 {
		return ""
	}
	return pat[len(pat)-4:]
}
