package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const oidcStateCookieName = "devops_worker_oidc_state"
const ssoUserCookieName = "devops_worker_sso_user"

type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func (a *App) effectiveSSOSettings() SSOSettings {
	settings, err := a.Store.LoadSSOSettings()
	if err != nil {
		log.Printf("load sso settings failed: %v", err)
	}
	// UI settings are authoritative. SSO is disabled by default until an admin enables it in /sso-settings.
	if strings.TrimSpace(settings.Scopes) == "" {
		settings.Scopes = "openid profile email"
	}
	if len(settings.AdminRoles) == 0 {
		settings.AdminRoles = []string{"devops-worker-admin", "admin"}
	}
	if len(settings.UserRoles) == 0 {
		settings.UserRoles = []string{"devops-worker-user", "user"}
	}
	return settings
}

func (a *App) ssoEnabled() bool {
	settings := a.effectiveSSOSettings()
	return settings.Enabled && strings.TrimSpace(settings.IssuerURL) != "" && strings.TrimSpace(settings.ClientID) != "" && strings.TrimSpace(settings.RedirectURL) != ""
}

func (a *App) oidcDiscover() (oidcDiscovery, error) {
	settings := a.effectiveSSOSettings()
	issuer := strings.TrimRight(strings.TrimSpace(settings.IssuerURL), "/")
	if issuer == "" {
		return oidcDiscovery{}, fmt.Errorf("OIDC issuer URL 未配置")
	}
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return oidcDiscovery{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var d oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return oidcDiscovery{}, err
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" {
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery 缺少 authorization_endpoint 或 token_endpoint")
	}
	return d, nil
}

func (a *App) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w)
	if !a.ssoEnabled() {
		http.Error(w, "SSO 未启用或配置不完整", http.StatusBadRequest)
		return
	}
	discovery, err := a.oidcDiscover()
	if err != nil {
		log.Printf("oidc discovery error: %v", err)
		http.Error(w, "SSO discovery failed", http.StatusBadGateway)
		return
	}
	state := randomURLToken(24)
	nonce := randomURLToken(24)
	cookieValue := state + ":" + nonce
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookieName, Value: cookieValue, Path: "/", MaxAge: 10 * 60, HttpOnly: true, SameSite: http.SameSiteLaxMode})

	scope := strings.TrimSpace(a.effectiveSSOSettings().Scopes)
	if scope == "" {
		scope = "openid profile email"
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", strings.TrimSpace(a.effectiveSSOSettings().ClientID))
	q.Set("redirect_uri", strings.TrimSpace(a.effectiveSSOSettings().RedirectURL))
	q.Set("scope", scope)
	q.Set("state", state)
	q.Set("nonce", nonce)
	http.Redirect(w, r, discovery.AuthorizationEndpoint+"?"+q.Encode(), http.StatusFound)
}

func (a *App) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w)
	if !a.ssoEnabled() {
		http.Error(w, "SSO 未启用或配置不完整", http.StatusBadRequest)
		return
	}
	if errMsg := strings.TrimSpace(r.URL.Query().Get("error")); errMsg != "" {
		http.Error(w, "SSO 登录失败: "+errMsg, http.StatusUnauthorized)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		http.Error(w, "SSO 回调缺少 code/state", http.StatusBadRequest)
		return
	}
	stateCookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || stateCookie.Value == "" {
		http.Error(w, "SSO state 已过期，请重新登录", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	parts := strings.SplitN(stateCookie.Value, ":", 2)
	if len(parts) != 2 || parts[0] != state {
		http.Error(w, "SSO state 校验失败", http.StatusUnauthorized)
		return
	}

	discovery, err := a.oidcDiscover()
	if err != nil {
		log.Printf("oidc discovery error: %v", err)
		http.Error(w, "SSO discovery failed", http.StatusBadGateway)
		return
	}
	tok, err := a.exchangeOIDCCode(discovery.TokenEndpoint, code)
	if err != nil {
		log.Printf("oidc token exchange error: %v", err)
		http.Error(w, "SSO token exchange failed", http.StatusBadGateway)
		return
	}
	claims := map[string]any{}
	mergeClaims(claims, parseJWTClaims(tok.IDToken))
	mergeClaims(claims, parseJWTClaims(tok.AccessToken))
	if discovery.UserInfoEndpoint != "" && tok.AccessToken != "" {
		if userInfo, err := fetchOIDCUserInfo(discovery.UserInfoEndpoint, tok.AccessToken); err == nil {
			mergeClaims(claims, userInfo)
		} else {
			log.Printf("oidc userinfo warning: %v", err)
		}
	}
	if a.oidcClaimsAreAdmin(claims) {
		exp := time.Now().Add(12 * time.Hour)
		http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: a.signAdminSession(exp.Unix()), Path: "/", Expires: exp, MaxAge: int(12 * time.Hour / time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: ssoUserCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if a.oidcClaimsAreUser(claims) {
		exp := time.Now().Add(12 * time.Hour)
		username := firstNonEmpty(claimString(claims, "preferred_username"), claimString(claims, "email"), claimString(claims, "name"), claimString(claims, "sub"))
		http.SetCookie(w, &http.Cookie{Name: ssoUserCookieName, Value: signSimpleSession(username, exp.Unix(), a.Cfg.AdminPassword), Path: "/", Expires: exp, MaxAge: int(12 * time.Hour / time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	log.Printf("SSO login rejected: subject=%s username=%s email=%s roles=%v", claimString(claims, "sub"), claimString(claims, "preferred_username"), claimString(claims, "email"), collectRoles(claims))
	http.Redirect(w, r, "/?sso=forbidden", http.StatusSeeOther)
}

func (a *App) exchangeOIDCCode(tokenEndpoint string, code string) (oidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", strings.TrimSpace(a.effectiveSSOSettings().RedirectURL))
	form.Set("client_id", strings.TrimSpace(a.effectiveSSOSettings().ClientID))
	if strings.TrimSpace(a.effectiveSSOSettings().ClientSecret) != "" {
		form.Set("client_secret", strings.TrimSpace(a.effectiveSSOSettings().ClientSecret))
	}
	req, err := http.NewRequest(http.MethodPost, tokenEndpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return oidcTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var tok oidcTokenResponse
	_ = json.Unmarshal(body, &tok)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oidcTokenResponse{}, fmt.Errorf("token endpoint status=%d error=%s desc=%s body=%s", resp.StatusCode, tok.Error, tok.Description, string(body))
	}
	if tok.AccessToken == "" && tok.IDToken == "" {
		return oidcTokenResponse{}, fmt.Errorf("token response missing access_token/id_token")
	}
	return tok, nil
}

func fetchOIDCUserInfo(endpoint string, accessToken string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("userinfo status=%d body=%s", resp.StatusCode, string(body))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *App) oidcClaimsAreAdmin(claims map[string]any) bool {
	settings := a.effectiveSSOSettings()
	allowedUsers := lowerSet(settings.AdminUsers)
	if len(allowedUsers) > 0 {
		ids := []string{claimString(claims, "sub"), claimString(claims, "preferred_username"), claimString(claims, "email"), claimString(claims, "name")}
		for _, id := range ids {
			if allowedUsers[strings.ToLower(strings.TrimSpace(id))] {
				return true
			}
		}
	}
	allowedRoles := lowerSet(settings.AdminRoles)
	if len(allowedRoles) > 0 {
		for _, role := range collectRoles(claims) {
			if allowedRoles[strings.ToLower(strings.TrimSpace(role))] {
				return true
			}
		}
	}
	return false
}

func (a *App) oidcClaimsAreUser(claims map[string]any) bool {
	settings := a.effectiveSSOSettings()
	allowedRoles := lowerSet(settings.UserRoles)
	if len(allowedRoles) == 0 {
		return true
	}
	for _, role := range collectRoles(claims) {
		if allowedRoles[strings.ToLower(strings.TrimSpace(role))] {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "sso-user"
}

func signSimpleSession(username string, exp int64, secret string) string {
	msg := fmt.Sprintf("%s:%d", username, exp)
	return base64.RawURLEncoding.EncodeToString([]byte(msg))
}

func randomURLToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func mergeClaims(dst map[string]any, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

func claimString(claims map[string]any, key string) string {
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}

func collectRoles(claims map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	var add func(any)
	add = func(v any) {
		switch t := v.(type) {
		case string:
			if t != "" && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		case []any:
			for _, x := range t {
				add(x)
			}
		}
	}
	add(claims["roles"])
	add(claims["groups"])
	if realm, ok := claims["realm_access"].(map[string]any); ok {
		add(realm["roles"])
	}
	if resource, ok := claims["resource_access"].(map[string]any); ok {
		for _, rv := range resource {
			if m, ok := rv.(map[string]any); ok {
				add(m["roles"])
			}
		}
	}
	return out
}

func lowerSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			out[v] = true
		}
	}
	return out
}
