package openai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/pkg/browser"
)

const (
	clientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	issuer   = "https://auth.openai.com"

	browserPort = 1455

	deviceUserCodeURL = issuer + "/api/accounts/deviceauth/usercode"
	deviceTokenURL    = issuer + "/api/accounts/deviceauth/token"
	tokenURL          = issuer + "/oauth/token"
	deviceCallbackURI = issuer + "/deviceauth/callback"
	redirectURI       = "http://localhost:1455/auth/callback"

	browserTimeout = 5 * time.Minute
)

var ErrBrowserUnavailable = errors.New("openai browser oauth unavailable")

var openURL = browser.OpenURL

type pkceCodes struct {
	verifier  string
	challenge string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type idTokenClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	Organizations    []struct {
		ID string `json:"id"`
	} `json:"organizations"`
	Auth struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

type deviceCodeResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
}

type BrowserSession struct {
	URL   string
	wait  func(context.Context) (*oauth.Token, error)
	close func()
	one   sync.Once
}

func (s *BrowserSession) Wait(ctx context.Context) (*oauth.Token, error) {
	return s.wait(ctx)
}

func (s *BrowserSession) Close() {
	s.one.Do(func() {
		if s.close != nil {
			s.close()
		}
	})
}

func StartBrowserAuth(ctx context.Context) (*BrowserSession, error) {
	ctx, cancel := context.WithTimeout(ctx, browserTimeout)

	pkce, err := generatePKCE()
	if err != nil {
		cancel()
		return nil, err
	}
	state, err := randomBase64URL(32)
	if err != nil {
		cancel()
		return nil, err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", browserPort))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start browser oauth listener: %w", ErrBrowserUnavailable)
	}

	resultCh := make(chan result, 1)
	srv := &http.Server{Handler: browserHandler(pkce, state, resultCh)}
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case resultCh <- result{err: serveErr}:
			default:
			}
		}
	}()

	closeFn := func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = listener.Close()
		cancel()
	}

	return &BrowserSession{
		URL: buildAuthorizeURL(state, pkce),
		wait: func(waitCtx context.Context) (*oauth.Token, error) {
			defer closeFn()
			select {
			case <-waitCtx.Done():
				return nil, waitCtx.Err()
			case <-ctx.Done():
				return nil, ctx.Err()
			case res := <-resultCh:
				if res.err != nil {
					return nil, res.err
				}
				return res.token, nil
			}
		},
		close: closeFn,
	}, nil
}

func BrowserAuth(ctx context.Context) (*oauth.Token, error) {
	session, err := StartBrowserAuth(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if err := openURL(session.URL); err != nil {
		return nil, fmt.Errorf("open browser oauth URL: %w", ErrBrowserUnavailable)
	}
	return session.Wait(ctx)
}

func DeviceAuth(ctx context.Context) (*oauth.Token, error) {
	dc, err := requestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}

	fmt.Println("Open the following URL and follow the instructions to authenticate with OpenAI:")
	fmt.Println()
	fmt.Println(issuer + "/codex/device")
	fmt.Println()
	fmt.Println("Code:", dc.UserCode)
	fmt.Println()

	interval := time.Duration(max(1, parseDeviceInterval(dc.Interval))) * time.Second
	deadline := time.NewTimer(browserTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("device authorization timed out")
		case <-ticker.C:
			data, err := pollDeviceToken(ctx, dc.DeviceAuthID, dc.UserCode)
			if err != nil {
				if errors.Is(err, errPending) {
					continue
				}
				if errors.Is(err, errSlowDown) {
					interval += 5 * time.Second
					ticker.Reset(interval)
					continue
				}
				return nil, err
			}
			return exchangeCode(ctx, data.AuthorizationCode, deviceCallbackURI, data.CodeVerifier)
		}
	}
}

func RefreshToken(ctx context.Context, refreshToken string) (*oauth.Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "crush")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai token refresh failed: %s - %s", resp.Status, string(body))
	}

	var tokens tokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}
	return tokenFromResponse(tokens), nil
}

func buildAuthorizeURL(state string, pkce pkceCodes) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", "openid profile email offline_access")
	params.Set("code_challenge", pkce.challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	params.Set("originator", "crush")
	params.Set("state", state)
	return issuer + "/oauth/authorize?" + params.Encode()
}

type result struct {
	token *oauth.Token
	err   error
}

func browserHandler(pkce pkceCodes, expectedState string, resultCh chan<- result) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errText := q.Get("error"); errText != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = errText
			}
			writeBrowserHTML(w, 200, errorHTML(desc))
			sendResult(resultCh, result{err: errors.New(desc)})
			return
		}

		code := q.Get("code")
		if code == "" {
			writeBrowserHTML(w, 400, errorHTML("missing authorization code"))
			sendResult(resultCh, result{err: errors.New("missing authorization code")})
			return
		}
		state := q.Get("state")
		if state != expectedState {
			writeBrowserHTML(w, 400, errorHTML("invalid state"))
			sendResult(resultCh, result{err: errors.New("invalid state")})
			return
		}

		tokens, err := exchangeCode(context.Background(), code, redirectURI, pkce.verifier)
		if err != nil {
			writeBrowserHTML(w, 500, errorHTML(err.Error()))
			sendResult(resultCh, result{err: err})
			return
		}

		writeBrowserHTML(w, 200, successHTML())
		sendResult(resultCh, result{token: tokens})
	})
	return mux
}

func sendResult(ch chan<- result, res result) {
	select {
	case ch <- res:
	default:
	}
}

func exchangeCode(ctx context.Context, code, redirectURI, verifier string) (*oauth.Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "crush")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai token exchange failed: %s - %s", resp.Status, string(body))
	}

	var tokens tokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}
	return tokenFromResponse(tokens), nil
}

func requestDeviceCode(ctx context.Context) (*deviceCodeResponse, error) {
	reqBody := map[string]string{"client_id": clientID}
	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceUserCodeURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "crush")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai device auth request failed: %s - %s", resp.Status, string(body))
	}

	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, err
	}
	if dc.DeviceAuthID == "" || dc.UserCode == "" {
		return nil, errors.New("invalid device authorization response")
	}
	return &dc, nil
}

var (
	errPending  = errors.New("pending")
	errSlowDown = errors.New("slow_down")
)

type authorizationData struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

func pollDeviceToken(ctx context.Context, deviceAuthID, userCode string) (*authorizationData, error) {
	reqBody := map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	}
	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceTokenURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "crush")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		var data authorizationData
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}
		if data.AuthorizationCode == "" || data.CodeVerifier == "" {
			return nil, errors.New("invalid device authorization token response")
		}
		return &data, nil
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, errPending
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, errSlowDown
	}
	return nil, fmt.Errorf("openai device auth poll failed: %s - %s", resp.Status, string(body))
}

func tokenFromResponse(tokens tokenResponse) *oauth.Token {
	token := &oauth.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresIn:    tokens.ExpiresIn,
	}
	token.SetExpiresAt()
	if token.AccountID == "" {
		token.AccountID = extractAccountID(tokens.IDToken)
	}
	if token.AccountID == "" {
		token.AccountID = extractAccountID(tokens.AccessToken)
	}
	return token
}

func extractAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if claims.ChatGPTAccountID != "" {
		return claims.ChatGPTAccountID
	}
	if claims.Auth.ChatGPTAccountID != "" {
		return claims.Auth.ChatGPTAccountID
	}
	if len(claims.Organizations) > 0 && claims.Organizations[0].ID != "" {
		return claims.Organizations[0].ID
	}
	return ""
}

func generatePKCE() (pkceCodes, error) {
	verifier, err := randomPKCEString(64)
	if err != nil {
		return pkceCodes{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return pkceCodes{verifier: verifier, challenge: challenge}, nil
}

func randomPKCEString(n int) (string, error) {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = chars[int(b)%len(chars)]
	}
	return string(buf), nil
}

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func parseDeviceInterval(raw string) int {
	if raw == "" {
		return 5
	}
	interval, err := time.ParseDuration(raw + "s")
	if err == nil {
		return int(interval / time.Second)
	}
	return 5
}

func writeBrowserHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func successHTML() string {
	return "<!doctype html><html><head><title>Crush - Authorization Successful</title></head><body><h1>Authorization Successful</h1><p>You can close this window and return to Crush.</p></body></html>"
}

func errorHTML(msg string) string {
	return "<!doctype html><html><head><title>Crush - Authorization Failed</title></head><body><h1>Authorization Failed</h1><p>" + htmlEscape(msg) + "</p></body></html>"
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
