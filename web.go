package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type App struct {
	Cfg   Config
	Store *Storage
	Loc   *time.Location
	TG    *TelegramService
}

type contextKey string

const roleContextKey contextKey = "role"
const identityContextKey contextKey = "identity"
const adminCookieName = "devops_worker_admin"

type AuthIdentity struct {
	Name     string `json:"name"`
	Email    string `json:"email,omitempty"`
	Role     string `json:"role"`
	Source   string `json:"source"`
	StaffID  string `json:"staff_id,omitempty"`
	Username string `json:"username,omitempty"`
	Exp      int64  `json:"exp"`
}

func (a *App) signIdentitySession(identity AuthIdentity) string {
	if identity.Exp == 0 {
		identity.Exp = time.Now().Add(12 * time.Hour).Unix()
	}
	if strings.TrimSpace(identity.Name) == "" {
		identity.Name = identity.Role
	}
	payload, _ := json.Marshal(identity)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(a.Cfg.AdminPassword))
	_, _ = mac.Write([]byte(payloadB64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadB64 + "." + sig
}

func (a *App) parseIdentitySession(v string) (AuthIdentity, bool) {
	if strings.TrimSpace(v) == "" {
		return AuthIdentity{}, false
	}
	// Current signed JSON format: base64(json).hmac
	if strings.Contains(v, ".") {
		parts := strings.SplitN(v, ".", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return AuthIdentity{}, false
		}
		mac := hmac.New(sha256.New, []byte(a.Cfg.AdminPassword))
		_, _ = mac.Write([]byte(parts[0]))
		expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
			return AuthIdentity{}, false
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			return AuthIdentity{}, false
		}
		var identity AuthIdentity
		if err := json.Unmarshal(payload, &identity); err != nil {
			return AuthIdentity{}, false
		}
		if identity.Exp != 0 && time.Now().Unix() > identity.Exp {
			return AuthIdentity{}, false
		}
		if strings.TrimSpace(identity.Name) == "" {
			identity.Name = identity.Role
		}
		return identity, true
	}
	// Backward compatibility with the old simple SSO user cookie: base64("username:exp").
	decoded, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return AuthIdentity{}, false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return AuthIdentity{}, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return AuthIdentity{}, false
	}
	return AuthIdentity{Name: parts[0], Role: "user", Source: "sso", Exp: exp}, true
}

func (a *App) requestIdentity(r *http.Request) (AuthIdentity, bool) {
	if c, err := r.Cookie(ssoUserCookieName); err == nil && c.Value != "" {
		if identity, ok := a.parseIdentitySession(c.Value); ok {
			return identity, true
		}
	}
	if a.isAdmin(r) {
		return AuthIdentity{Name: a.Cfg.AdminUsername, Role: "admin", Source: "local"}, true
	}
	return AuthIdentity{Name: "普通用户", Role: "user", Source: "anonymous"}, false
}

func setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (a *App) routes() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	appMux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	appMux.HandleFunc("/login", a.handleLogin)
	appMux.HandleFunc("/logout", a.handleLogout)
	appMux.HandleFunc("/sso/login", a.handleSSOLogin)
	appMux.HandleFunc("/sso/callback", a.handleSSOCallback)

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleDashboard)
	mux.HandleFunc("/users", a.handleUsers)
	mux.HandleFunc("/users/create", a.handleUserCreate)
	mux.HandleFunc("/users/update", a.handleUserUpdate)
	mux.HandleFunc("/users/delete", a.handleUserDelete)
	mux.HandleFunc("/users/export", a.handleUsersExport)
	mux.HandleFunc("/users/import", a.handleUsersImport)
	mux.HandleFunc("/groups/create", a.handleGroupCreate)
	mux.HandleFunc("/groups/update", a.handleGroupUpdate)
	mux.HandleFunc("/groups/delete", a.handleGroupDelete)
	mux.HandleFunc("/shifts", a.handleShifts)
	mux.HandleFunc("/shifts/create", a.handleShiftCreate)
	mux.HandleFunc("/shifts/update", a.handleShiftUpdate)
	mux.HandleFunc("/shifts/delete", a.handleShiftDelete)
	mux.HandleFunc("/schedule", a.handleSchedule)
	mux.HandleFunc("/schedule/export", a.handleScheduleExport)
	mux.HandleFunc("/schedule/submit", a.handleScheduleSubmit)
	mux.HandleFunc("/approvals", a.handleApprovals)
	mux.HandleFunc("/approvals/approve", a.handleApprovalApprove)
	mux.HandleFunc("/approvals/reject", a.handleApprovalReject)
	mux.HandleFunc("/history", a.handleHistory)
	mux.HandleFunc("/settings", a.handleSettingsRedirect)
	mux.HandleFunc("/sso-settings", a.handleSSOSettings)
	mux.HandleFunc("/sso-settings/update", a.handleSSOSettingsUpdate)
	mux.Handle("/previews/", http.StripPrefix("/previews/", http.FileServer(http.Dir(filepath.Join(a.Cfg.DataDir, "previews")))))
	appMux.Handle("/", a.roleMiddleware(mux))
	return appMux
}

func (a *App) roleMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setNoStoreHeaders(w)
		identity, authenticated := a.requestIdentity(r)
		if a.ssoEnabled() && !authenticated {
			// SSO 开启后不再允许匿名浏览业务页面，统一进入登录入口。
			http.Redirect(w, r, "/login?next="+urlQueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		role := "user"
		if a.isAdmin(r) || identity.Role == "admin" {
			role = "admin"
			identity.Role = "admin"
		} else {
			identity.Role = "user"
		}
		ctx := context.WithValue(r.Context(), roleContextKey, role)
		ctx = context.WithValue(ctx, identityContextKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func urlQueryEscape(v string) string { return url.QueryEscape(v) }

func (a *App) role(r *http.Request) string {
	if v, ok := r.Context().Value(roleContextKey).(string); ok && v != "" {
		return v
	}
	if a.isAdmin(r) {
		return "admin"
	}
	return "user"
}

func (a *App) identity(r *http.Request) (AuthIdentity, bool) {
	if v, ok := r.Context().Value(identityContextKey).(AuthIdentity); ok {
		return v, v.Source != "anonymous"
	}
	return a.requestIdentity(r)
}

func (a *App) isAdmin(r *http.Request) bool {
	c, err := r.Cookie(adminCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return a.verifyAdminSession(c.Value)
}

func (a *App) signAdminSession(exp int64) string {
	msg := fmt.Sprintf("%s:%d", a.Cfg.AdminUsername, exp)
	mac := hmac.New(sha256.New, []byte(a.Cfg.AdminPassword))
	_, _ = mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%d:%s", exp, sig)
}

func (a *App) verifyAdminSession(v string) bool {
	parts := strings.SplitN(v, ":", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	expected := a.signAdminSession(exp)
	return hmac.Equal([]byte(v), []byte(expected))
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w)
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		if a.ssoEnabled() && r.URL.Query().Get("local") != "1" {
			http.Redirect(w, r, "/sso/login", http.StatusSeeOther)
			return
		}
		if r.FormValue("username") == a.Cfg.AdminUsername && r.FormValue("password") == a.Cfg.AdminPassword {
			exp := time.Now().Add(12 * time.Hour)
			http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: a.signAdminSession(exp.Unix()), Path: "/", Expires: exp, MaxAge: int(12 * time.Hour / time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
			identity := AuthIdentity{Name: a.Cfg.AdminUsername, Role: "admin", Source: "local", Exp: exp.Unix()}
			http.SetCookie(w, &http.Cookie{Name: ssoUserCookieName, Value: a.signIdentitySession(identity), Path: "/", Expires: exp, MaxAge: int(12 * time.Hour / time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("admin login failed"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if a.ssoEnabled() {
		page := `<html><head><meta charset="utf-8"><title>SSO Login</title><style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial;background:#eef6ff;display:grid;place-items:center;height:100vh;margin:0}.card{background:white;border:1px solid #dbe4ef;border-radius:18px;padding:24px;box-shadow:0 20px 50px rgba(15,23,42,.12);width:360px}h2{margin:0 0 10px}p{color:#64748b;font-size:13px}.sso{display:block;width:100%;box-sizing:border-box;padding:11px;margin:12px 0;border-radius:10px;border:1px solid #dbe4ef;text-align:center;background:linear-gradient(135deg,#0f172a,#2563eb);color:#fff;font-weight:700;text-decoration:none}.plain{display:block;text-align:center;font-size:12px;margin-top:10px;color:#64748b}</style></head><body><div class="card"><h2>devops-worker 登录</h2><p>当前已启用 SSO，请通过统一身份认证后访问系统。</p><a class="sso" href="/sso/login">使用 Keycloak SSO 登录</a><a class="plain" href="/login?local=1">本地管理员备用入口</a></div></body></html>`
		if r.URL.Query().Get("local") != "1" {
			_, _ = w.Write([]byte(page))
			return
		}
	}
	ssoButton := ""
	if a.ssoEnabled() {
		ssoButton = `<a class="sso" href="/sso/login">使用 Keycloak SSO 登录</a><div class="or">本地管理员备用登录</div>`
	}
	page := fmt.Sprintf(`<html><head><meta charset="utf-8"><title>Admin Login</title><style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial;background:#eef6ff;display:grid;place-items:center;height:100vh;margin:0}.card{background:white;border:1px solid #dbe4ef;border-radius:18px;padding:24px;box-shadow:0 20px 50px rgba(15,23,42,.12);width:360px}h2{margin:0 0 10px}p{color:#64748b;font-size:13px}input,button,.sso{width:100%%;box-sizing:border-box;padding:10px;margin:8px 0;border-radius:10px;border:1px solid #dbe4ef}button,.sso{display:block;text-align:center;background:#2563eb;color:#fff;font-weight:700;cursor:pointer;text-decoration:none}.sso{background:linear-gradient(135deg,#0f172a,#2563eb)}.or{text-align:center;color:#94a3b8;font-size:12px;margin:8px 0}.plain{display:block;text-align:center;font-size:13px;margin-top:8px}</style></head><body><form class="card" method="post"><h2>devops-worker 管理员登录</h2><p>请选择 SSO 或本地管理员登录。</p>%s<input name="username" placeholder="管理员账号" required><input name="password" type="password" placeholder="管理员密码" required><button type="submit">登录</button>{{ANON_LINK}}</form></body></html>`, ssoButton)
	if a.ssoEnabled() {
		page = strings.ReplaceAll(page, "{{ANON_LINK}}", "")
	} else {
		page = strings.ReplaceAll(page, "{{ANON_LINK}}", `<a class="plain" href="/">以普通用户进入</a>`)
	}
	_, _ = w.Write([]byte(page))
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w)
	http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: ssoUserCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if a.ssoEnabled() {
		http.Redirect(w, r, "/login?logout=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?logout=1", http.StatusSeeOther)
}

func (a *App) render(w http.ResponseWriter, templateName string, data PageData) {
	var buf bytes.Buffer
	if err := renderPage(&buf, templateName, data); err != nil {
		log.Printf("render error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setNoStoreHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (a *App) requestTimezone(r *http.Request) string {
	// Web display timezone is a user-facing preference. Default to Dubai so
	// rendered dates do not silently inherit the server timezone.
	tz := strings.TrimSpace(r.URL.Query().Get("tz"))
	if tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if c, err := r.Cookie("devops_worker_tz"); err == nil {
		if v := strings.TrimSpace(c.Value); v != "" {
			if _, err := time.LoadLocation(v); err == nil {
				return v
			}
		}
	}
	return DefaultShiftTimezone
}

func (a *App) requestLocation(r *http.Request) *time.Location {
	tz := a.requestTimezone(r)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation(DefaultShiftTimezone)
	}
	return loc
}

func (a *App) basePage(r *http.Request, title string) PageData {
	loc := a.requestLocation(r)
	tz := a.requestTimezone(r)
	now := time.Now().In(loc)
	cfg := a.Cfg
	cfg.Timezone = tz
	role := a.role(r)
	identity, authenticated := a.identity(r)
	groups, _ := a.Store.LoadUserGroups()
	return PageData{
		Title:             title,
		Role:              role,
		IsAdmin:           role == "admin",
		IsAuthenticated:   authenticated,
		CurrentUserName:   identity.Name,
		CurrentUserEmail:  identity.Email,
		CurrentUserSource: identity.Source,
		Groups:            groups,
		Config:            cfg,
		NowYear:           now.Year(),
		NowMonth:          int(now.Month()),
		NowDate:           now.Format("2006-01-02"),
		Months:            []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		WeekNums:          []int{1, 2, 3, 4, 5},
		TimeOptions:       buildTimeOptions(),
		TimezoneOptions:   buildTimezoneOptions(),
	}
}

func buildTimeOptions() []string {
	// Minute-level precision. 24:00 is kept as an explicit end-of-day option.
	options := make([]string, 0, 24*60+1)
	for h := 0; h < 24; h++ {
		for m := 0; m < 60; m++ {
			options = append(options, fmt.Sprintf("%02d:%02d", h, m))
		}
	}
	options = append(options, "24:00")
	return options
}

func buildTimezoneOptions() []TimezoneOption {
	return []TimezoneOption{
		{Name: "Asia/Dubai", Label: "迪拜 / 阿联酋 (Asia/Dubai)"},
		{Name: "Asia/Shanghai", Label: "中国上海 / 北京时间 (Asia/Shanghai)"},
		{Name: "Asia/Singapore", Label: "新加坡 (Asia/Singapore)"},
		{Name: "Asia/Tokyo", Label: "东京 (Asia/Tokyo)"},
		{Name: "UTC", Label: "UTC"},
		{Name: "Europe/Berlin", Label: "柏林 (Europe/Berlin)"},
		{Name: "America/New_York", Label: "纽约 (America/New_York)"},
	}
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := a.basePage(r, "首页")
	active, err := a.Store.LoadActive()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Active = active
	}
	data.Users, _ = a.Store.LoadUsers()
	loc := a.requestLocation(r)
	year, month, selected := a.resolveCalendarRequestWithLoc(r, data.NowYear, data.NowMonth, data.NowDate, loc)
	a.fillCalendarWithLoc(&data, year, month, selected, active.Items, loc)
	a.fillDayStatusesWithLoc(&data, active.Items, loc)
	items := filterItemsByDate(active.Items, selected)
	data.SelectedDayItems = a.Store.BuildScheduleItemStatuses(items, loc)
	a.render(w, "dashboard", data)
}

func (a *App) handleUsers(w http.ResponseWriter, r *http.Request) {
	data := a.basePage(r, "用户管理")
	if msg := strings.TrimSpace(r.URL.Query().Get("msg")); msg != "" {
		data.Message = msg
	}
	users, err := a.Store.LoadUsers()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Users = users
	}
	data.Groups, _ = a.Store.LoadUserGroups()
	a.render(w, "users", data)
}

func (a *App) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	groupID := strings.TrimSpace(r.FormValue("group_id"))
	if groupID == "" {
		groupID = DefaultUserGroupID
	}
	tgID := parseFormInt64(r.FormValue("telegram_user_id"))
	users, _ := a.Store.LoadUsers()
	now := time.Now().Format(time.RFC3339)
	users = append(users, StaffUser{ID: newID("user"), Name: name, Email: email, Phone: phone, GroupID: groupID, TelegramUserID: tgID, Enabled: true, CreatedBy: a.role(r), CreatedAt: now, UpdatedAt: now})
	if err := a.Store.SaveUsers(users); err != nil {
		log.Printf("save user error: %v", err)
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := r.FormValue("id")
	users, _ := a.Store.LoadUsers()
	disabledNow := false
	for i := range users {
		if users[i].ID == id {
			if !a.isAdmin(r) && users[i].CreatedBy != "user" {
				a.renderError(w, "用户管理", "普通用户只能修改自己新建的用户，不能修改既有/admin 用户")
				return
			}
			wasEnabled := users[i].Enabled
			users[i].Name = strings.TrimSpace(r.FormValue("name"))
			users[i].Email = strings.TrimSpace(r.FormValue("email"))
			users[i].Phone = strings.TrimSpace(r.FormValue("phone"))
			users[i].GroupID = strings.TrimSpace(r.FormValue("group_id"))
			if users[i].GroupID == "" {
				users[i].GroupID = DefaultUserGroupID
			}
			users[i].TelegramUserID = parseFormInt64(r.FormValue("telegram_user_id"))
			users[i].Enabled = r.FormValue("enabled") == "true"
			users[i].UpdatedAt = time.Now().Format(time.RFC3339)
			disabledNow = wasEnabled && !users[i].Enabled
		}
	}
	if err := a.Store.SaveUsers(users); err != nil {
		log.Printf("update user error: %v", err)
	}
	if disabledNow {
		a.asyncCleanupFutureSchedules(id, "", "user_disabled_cleanup")
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) asyncCleanupFutureSchedules(userID string, shiftCode string, reason string) {
	go func() {
		summary, err := a.Store.CleanupFutureItemsByUserOrShift(userID, shiftCode, a.Loc, reason)
		if err != nil {
			log.Printf("async cleanup future schedules failed: user=%s shift=%s reason=%s err=%v", userID, shiftCode, reason, err)
			return
		}
		if summary.ChangedItems > 0 {
			log.Printf("async cleanup future schedules done: user=%s shift=%s reason=%s changed=%d revision=%d version=%s", userID, shiftCode, reason, summary.ChangedItems, summary.NewRevision, summary.VersionID)
			if a.TG != nil {
				a.TG.WakeNotificationQueue()
			}
		}
	}()
}

func (a *App) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "普通用户无删除权限", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := r.FormValue("id")
	users, _ := a.Store.LoadUsers()
	out := make([]StaffUser, 0, len(users))
	changed := false
	permanentlyDeleted := false
	for _, u := range users {
		if u.ID != id {
			out = append(out, u)
			continue
		}
		changed = true
		if u.Enabled {
			u.Enabled = false
			u.UpdatedAt = time.Now().Format(time.RFC3339)
			out = append(out, u)
		} else {
			permanentlyDeleted = true
		}
	}
	if changed {
		if err := a.Store.SaveUsers(out); err != nil {
			log.Printf("disable/delete user error: %v", err)
		}
		if permanentlyDeleted {
			a.asyncCleanupFutureSchedules(id, "", "user_delete_cleanup")
		} else {
			a.asyncCleanupFutureSchedules(id, "", "user_disable_cleanup")
		}
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以管理分组", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	groups, _ := a.Store.LoadUserGroups()
	now := time.Now().Format(time.RFC3339)
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		id = sanitizeGroupID(name)
	}
	if id == "" || groupIDExists(groups, id) {
		id = newID("grp")
	}
	groups = append(groups, UserGroup{ID: id, Name: name, Enabled: true, CreatedBy: "admin", CreatedAt: now, UpdatedAt: now})
	if err := a.Store.SaveUserGroups(groups); err != nil {
		log.Printf("save group error: %v", err)
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) handleGroupUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以管理分组", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := strings.TrimSpace(r.FormValue("id"))
	groups, _ := a.Store.LoadUserGroups()
	for i := range groups {
		if groups[i].ID == id {
			groups[i].Name = strings.TrimSpace(r.FormValue("name"))
			if groups[i].Name == "" {
				groups[i].Name = groups[i].ID
			}
			groups[i].Enabled = r.FormValue("enabled") == "true"
			groups[i].UpdatedAt = time.Now().Format(time.RFC3339)
		}
	}
	if err := a.Store.SaveUserGroups(groups); err != nil {
		log.Printf("update group error: %v", err)
	}
	go func() {
		if summary, err := a.Store.SyncActiveItemsWithLatestShifts(a.Loc); err != nil {
			log.Printf("sync after group update failed: %v", err)
		} else if summary.ChangedItems > 0 && a.TG != nil {
			a.TG.WakeNotificationQueue()
		}
	}()
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以管理分组", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := strings.TrimSpace(r.FormValue("id"))
	if id == DefaultUserGroupID {
		http.Redirect(w, r, "/users?msg="+url.QueryEscape("默认 devops 分组不能彻底删除，只能禁用/启用"), http.StatusSeeOther)
		return
	}
	groups, _ := a.Store.LoadUserGroups()
	out := make([]UserGroup, 0, len(groups))
	for _, g := range groups {
		if g.ID != id {
			out = append(out, g)
			continue
		}
		if g.Enabled {
			g.Enabled = false
			g.UpdatedAt = time.Now().Format(time.RFC3339)
			out = append(out, g)
		}
	}
	if err := a.Store.SaveUserGroups(out); err != nil {
		log.Printf("delete group error: %v", err)
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func groupIDExists(groups []UserGroup, id string) bool {
	for _, g := range groups {
		if strings.EqualFold(g.ID, id) {
			return true
		}
	}
	return false
}

func sanitizeGroupID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '.' || r == '/' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (a *App) handleUsersExport(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以导出用户", http.StatusForbidden)
		return
	}
	users, err := a.Store.LoadUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="devops-worker-users.csv"`)
	_, _ = w.Write([]byte("\ufeff"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "name", "email", "phone", "group_id", "telegram_user_id", "enabled", "created_by", "sso_provider", "sso_sub", "sso_username", "sso_email", "last_sso_login_at", "created_at", "updated_at"})
	for _, u := range users {
		_ = cw.Write([]string{
			u.ID,
			u.Name,
			u.Email,
			u.Phone,
			u.GroupID,
			strconv.FormatInt(u.TelegramUserID, 10),
			strconv.FormatBool(u.Enabled),
			u.CreatedBy,
			u.SSOProvider,
			u.SSOSub,
			u.SSOUsername,
			u.SSOEmail,
			u.LastSSOLoginAt,
			u.CreatedAt,
			u.UpdatedAt,
		})
	}
	cw.Flush()
}

func (a *App) handleUsersImport(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以批量导入用户", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		a.renderError(w, "用户批量导入", "解析上传内容失败: "+err.Error())
		return
	}
	var reader io.Reader
	file, _, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		reader = file
	} else if text := strings.TrimSpace(r.FormValue("csv_text")); text != "" {
		reader = strings.NewReader(text)
	} else {
		a.renderError(w, "用户批量导入", "请上传 CSV 文件或粘贴 CSV 内容")
		return
	}
	imported, updated, err := a.importUsersCSV(reader)
	if err != nil {
		a.renderError(w, "用户批量导入", err.Error())
		return
	}
	if summary, err := a.Store.SyncActiveItemsWithLatestShifts(a.Loc); err != nil {
		log.Printf("sync schedule after user import failed: %v", err)
	} else if summary.ChangedItems > 0 && a.TG != nil {
		a.TG.WakeNotificationQueue()
	}
	http.Redirect(w, r, fmt.Sprintf("/users?msg=%s", url.QueryEscape(fmt.Sprintf("批量导入完成：新增 %d 个，更新 %d 个", imported, updated))), http.StatusSeeOther)
}

func (a *App) importUsersCSV(reader io.Reader) (int, int, error) {
	cr := csv.NewReader(reader)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		return 0, 0, fmt.Errorf("读取 CSV 失败: %w", err)
	}
	if len(records) < 2 {
		return 0, 0, fmt.Errorf("CSV 至少需要表头和一行用户数据")
	}
	header := map[string]int{}
	for i, h := range records[0] {
		header[normalizeUserImportHeader(h)] = i
	}
	get := func(row []string, key string) string {
		idx, ok := header[key]
		if !ok || idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}
	if _, ok := header["name"]; !ok {
		return 0, 0, fmt.Errorf("CSV 缺少必需表头 name/用户名")
	}

	users, err := a.Store.LoadUsers()
	if err != nil {
		return 0, 0, err
	}
	now := time.Now().Format(time.RFC3339)
	created := 0
	updated := 0
	for lineNo, row := range records[1:] {
		if rowEmpty(row) {
			continue
		}
		name := get(row, "name")
		if name == "" {
			return created, updated, fmt.Errorf("第 %d 行缺少用户名 name", lineNo+2)
		}
		candidate := StaffUser{
			ID:             get(row, "id"),
			Name:           name,
			Email:          get(row, "email"),
			Phone:          get(row, "phone"),
			TelegramUserID: parseFormInt64(get(row, "telegram_user_id")),
			Enabled:        parseBoolDefault(get(row, "enabled"), true),
			CreatedBy:      firstNonEmpty(get(row, "created_by"), "import"),
			SSOProvider:    get(row, "sso_provider"),
			SSOSub:         get(row, "sso_sub"),
			SSOUsername:    get(row, "sso_username"),
			SSOEmail:       get(row, "sso_email"),
			LastSSOLoginAt: get(row, "last_sso_login_at"),
			CreatedAt:      firstNonEmpty(get(row, "created_at"), now),
			UpdatedAt:      now,
		}
		idx := findUserForImport(users, candidate)
		if idx >= 0 {
			// CSV 导入采用完整字符串匹配后更新，不做包含/前缀/模糊匹配。
			if candidate.ID == "" {
				candidate.ID = users[idx].ID
			}
			if candidate.CreatedAt == "" {
				candidate.CreatedAt = users[idx].CreatedAt
			}
			if candidate.CreatedBy == "" {
				candidate.CreatedBy = users[idx].CreatedBy
			}
			users[idx] = candidate
			updated++
		} else {
			if candidate.ID == "" {
				candidate.ID = newID("user")
			}
			users = append(users, candidate)
			created++
		}
	}
	if err := a.Store.SaveUsers(users); err != nil {
		return created, updated, err
	}
	return created, updated, nil
}

func normalizeUserImportHeader(h string) string {
	h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
	h = strings.ToLower(h)
	h = strings.ReplaceAll(h, "-", "_")
	h = strings.ReplaceAll(h, " ", "_")
	switch h {
	case "用户名", "姓名", "名称", "name", "user_name", "username":
		return "name"
	case "邮箱", "邮件", "email", "mail":
		return "email"
	case "电话", "手机号", "手机", "phone", "mobile", "telephone":
		return "phone"
	case "分组", "用户组", "group", "group_id", "user_group":
		return "group_id"
	case "telegram", "telegram_id", "telegram_user_id", "tg", "tg_id":
		return "telegram_user_id"
	case "启用", "状态", "enabled":
		return "enabled"
	case "来源", "created_by", "source":
		return "created_by"
	case "sso_provider", "sso_issuer":
		return "sso_provider"
	case "sub", "sso_sub":
		return "sso_sub"
	case "sso_username", "preferred_username":
		return "sso_username"
	case "sso_email":
		return "sso_email"
	case "last_sso_login_at":
		return "last_sso_login_at"
	case "created_at":
		return "created_at"
	case "updated_at":
		return "updated_at"
	case "id", "用户id", "用户_id":
		return "id"
	default:
		return h
	}
}

func rowEmpty(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func parseBoolDefault(v string, def bool) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return def
	}
	switch v {
	case "true", "1", "yes", "y", "on", "启用", "是":
		return true
	case "false", "0", "no", "n", "off", "禁用", "否":
		return false
	default:
		return def
	}
}

func findUserForImport(users []StaffUser, cand StaffUser) int {
	if strings.TrimSpace(cand.ID) != "" {
		for i := range users {
			if sameString(users[i].ID, cand.ID) {
				return i
			}
		}
	}
	if strings.TrimSpace(cand.SSOSub) != "" {
		for i := range users {
			if sameString(users[i].SSOSub, cand.SSOSub) {
				return i
			}
		}
	}
	if strings.TrimSpace(cand.Email) != "" {
		for i := range users {
			if sameString(users[i].Email, cand.Email) || sameString(users[i].SSOEmail, cand.Email) {
				return i
			}
		}
	}
	if strings.TrimSpace(cand.SSOEmail) != "" {
		for i := range users {
			if sameString(users[i].Email, cand.SSOEmail) || sameString(users[i].SSOEmail, cand.SSOEmail) {
				return i
			}
		}
	}
	if strings.TrimSpace(cand.SSOUsername) != "" {
		for i := range users {
			if sameString(users[i].SSOUsername, cand.SSOUsername) || sameString(users[i].Name, cand.SSOUsername) {
				return i
			}
		}
	}
	if strings.TrimSpace(cand.Name) != "" {
		for i := range users {
			if sameString(users[i].Name, cand.Name) {
				return i
			}
		}
	}
	return -1
}

func (a *App) handleShifts(w http.ResponseWriter, r *http.Request) {
	data := a.basePage(r, "班次设置")
	shifts, err := a.Store.LoadShifts()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Shifts = shifts
	}
	a.render(w, "shifts", data)
}

func formBoolPtr(r *http.Request, name string, defaultValue bool) *bool {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		return boolPtr(defaultValue)
	}
	return boolPtr(v == "true" || v == "on" || v == "1" || strings.EqualFold(v, "yes"))
}

func (a *App) handleShiftCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/shifts", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	shifts, _ := a.Store.LoadShifts()
	shift, err := normalizeShift(Shift{
		Code:          newShiftCode(shifts),
		Name:          r.FormValue("name"),
		ShortName:     r.FormValue("short_name"),
		Start:         r.FormValue("start"),
		End:           r.FormValue("end"),
		Timezone:      r.FormValue("timezone"),
		Enabled:       true,
		NotifyEnabled: formBoolPtr(r, "notify_enabled", true),
		CreatedBy:     a.role(r),
	})
	if err != nil {
		a.renderError(w, "班次设置", err.Error())
		return
	}
	shifts = append(shifts, shift)
	if err := a.Store.SaveShifts(shifts); err != nil {
		log.Printf("save shift error: %v", err)
	}
	http.Redirect(w, r, "/shifts", http.StatusSeeOther)
}

func (a *App) handleShiftUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/shifts", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	code := r.FormValue("code")
	shifts, _ := a.Store.LoadShifts()
	var updated Shift
	found := false
	for i := range shifts {
		if shifts[i].Code == code {
			old := shifts[i]
			if !a.isAdmin(r) && old.CreatedBy != "user" {
				a.renderError(w, "班次设置", "普通用户只能修改自己新建的班次，默认/admin 班次只能查看")
				return
			}
			candidate := Shift{
				Code:          old.Code,
				Name:          r.FormValue("name"),
				ShortName:     r.FormValue("short_name"),
				Start:         r.FormValue("start"),
				End:           r.FormValue("end"),
				Timezone:      r.FormValue("timezone"),
				Enabled:       r.FormValue("enabled") != "false",
				NotifyEnabled: formBoolPtr(r, "notify_enabled", shiftNotificationEnabled(old)),
				CreatedBy:     old.CreatedBy,
			}
			sh, err := normalizeShift(candidate)
			if err != nil {
				a.renderError(w, "班次设置", err.Error())
				return
			}
			shifts[i] = sh
			updated = sh
			found = true
			break
		}
	}
	if !found {
		a.renderError(w, "班次设置", "班次不存在")
		return
	}
	if err := a.Store.SaveShifts(shifts); err != nil {
		log.Printf("update shift error: %v", err)
	}
	if !updated.Enabled {
		a.asyncCleanupFutureSchedules("", updated.Code, "shift_disabled_cleanup")
	} else if summary, err := a.Store.UpdateFutureItemsForShift(updated, a.Loc); err != nil {
		log.Printf("update future schedule items failed: %v", err)
	} else if summary.ChangedItems > 0 {
		log.Printf("shift %s updated %d future schedule items, revision=%d version=%s", updated.Code, summary.ChangedItems, summary.NewRevision, summary.VersionID)
		if a.TG != nil {
			a.TG.WakeNotificationQueue()
		}
	}
	http.Redirect(w, r, "/shifts", http.StatusSeeOther)
}

func (a *App) handleShiftDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "普通用户无删除权限", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/shifts", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	code := r.FormValue("code")
	shifts, _ := a.Store.LoadShifts()
	out := make([]Shift, 0, len(shifts))
	changed := false
	permanentlyDeleted := false
	for _, sh := range shifts {
		if sh.Code != code {
			out = append(out, sh)
			continue
		}
		changed = true
		if sh.Enabled {
			sh.Enabled = false
			out = append(out, sh)
		} else {
			permanentlyDeleted = true
		}
	}
	if changed {
		if err := a.Store.SaveShifts(out); err != nil {
			log.Printf("disable/delete shift error: %v", err)
		}
		if permanentlyDeleted {
			a.asyncCleanupFutureSchedules("", code, "shift_delete_cleanup")
		} else {
			a.asyncCleanupFutureSchedules("", code, "shift_disable_cleanup")
		}
	}
	http.Redirect(w, r, "/shifts", http.StatusSeeOther)
}

func (a *App) handleSchedule(w http.ResponseWriter, r *http.Request) {
	data := a.basePage(r, "排班设置")
	users, _ := a.Store.LoadUsers()
	shifts, _ := a.Store.LoadShifts()
	active, _ := a.Store.LoadActive()
	data.Users = enabledUsers(users)
	data.Shifts = enabledShifts(shifts)
	data.Active = active
	loc := a.requestLocation(r)
	year, month, selected := a.resolveCalendarRequestWithLoc(r, data.NowYear, data.NowMonth, data.NowDate, loc)
	a.fillCalendarWithLoc(&data, year, month, selected, active.Items, loc)
	a.fillDayStatusesWithLoc(&data, active.Items, loc)
	data.SelectedDayItems = a.Store.BuildScheduleItemStatuses(filterItemsByDate(active.Items, selected), loc)
	data.ScheduleVersions, _ = a.Store.ListScheduleMonthVersions(year, month, loc)
	if len(data.ScheduleVersions) > 0 {
		data.SelectedVersionKey = data.ScheduleVersions[0].Key
	}
	a.render(w, "schedule", data)
}

func (a *App) handleScheduleExport(w http.ResponseWriter, r *http.Request) {
	if a.role(r) != "admin" {
		http.Error(w, "只有超级管理员可以导出包含手机号的排班表", http.StatusForbidden)
		return
	}
	loc := a.requestLocation(r)
	now := time.Now().In(loc)
	year := parseQueryInt(r, "year", now.Year())
	month := parseQueryInt(r, "month", int(now.Month()))
	if month < 1 || month > 12 {
		http.Error(w, "月份参数无效", http.StatusBadRequest)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "xlsx"
	}
	if format != "xlsx" && format != "csv" {
		http.Error(w, "format 只支持 xlsx 或 csv", http.StatusBadRequest)
		return
	}
	version, items, err := a.Store.ResolveScheduleMonthVersion(year, month, r.URL.Query().Get("version"), loc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	users, _ := a.Store.LoadUsers()
	exportData := buildScheduleExportData(year, month, version, items, users, loc)
	filename := fmt.Sprintf("devops-worker-schedule-%04d-%02d-rev%d.%s", year, month, version.Revision, format)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		if err := writeScheduleCSV(w, exportData); err != nil {
			log.Printf("write schedule csv export failed: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	if err := writeScheduleXLSX(w, exportData); err != nil {
		log.Printf("write schedule xlsx export failed: %v", err)
	}
}

func (a *App) handleScheduleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/schedule", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	year, _ := strconv.Atoi(r.FormValue("year"))
	month, _ := strconv.Atoi(r.FormValue("month"))
	createdBy := strings.TrimSpace(r.FormValue("created_by"))
	if createdBy == "" {
		createdBy = a.role(r)
	}
	users, err := a.Store.LoadUsers()
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	shifts, err := a.Store.LoadShifts()
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	loc := a.requestLocation(r)
	rules, err := a.parseScheduleSubmitRulesWithLoc(r, year, month, shifts, loc)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	newItems, err := BuildScheduleItems(rules, users, shifts, loc)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	active, _ := a.Store.LoadActive()
	previewItems := MergeScheduleItems(active.Items, newItems)
	html, err := RenderPreviewHTML("待生成", previewItems, active.Revision, active.Revision+1)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	approval, err := a.Store.CreateApproval(createdBy, rules, previewItems, html, a.Cfg.ApproverUserIDs)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	html, _ = RenderPreviewHTML(approval.ID, previewItems, approval.BaseRevision, approval.NewRevision)
	_ = writeFileAtomic(filepath.Join(a.Cfg.DataDir, approval.PreviewHTML), []byte(html))
	if a.TG != nil {
		if err := a.TG.SendApproval(approval); err != nil {
			log.Printf("telegram approval send error: %v", err)
		}
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

func (a *App) parseScheduleSubmitRulesWithLoc(r *http.Request, year int, month int, shifts []Shift, loc *time.Location) ([]ScheduleRule, error) {
	draftRaw := strings.TrimSpace(r.FormValue("draft_rules"))
	var rules []ScheduleRule
	if draftRaw != "" {
		var changes []ScheduleDraftChange
		if err := json.Unmarshal([]byte(draftRaw), &changes); err != nil {
			return nil, fmt.Errorf("草稿内容格式错误: %w", err)
		}
		changes = ApplyAutoRestDefaults(changes, year, month, shifts, loc)
		rules = append(rules, DraftChangesToRules(changes, year, month)...)
	} else {
		dates := splitCSV(r.FormValue("selected_dates"))
		staffIDs := r.Form["staff_ids"]
		shiftCode := strings.TrimSpace(r.FormValue("shift_code"))
		if len(dates) > 0 && len(staffIDs) > 0 && shiftCode != "" {
			changes := ApplyAutoRestDefaults([]ScheduleDraftChange{{Dates: dates, StaffIDs: staffIDs, ShiftCode: shiftCode}}, year, month, shifts, loc)
			rules = append(rules, DraftChangesToRules(changes, year, month)...)
		}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("请先至少加入一条排班草稿，再统一提交审批")
	}
	return rules, nil
}

func (a *App) renderError(w http.ResponseWriter, title, errMsg string) {
	loc, _ := time.LoadLocation(DefaultShiftTimezone)
	now := time.Now().In(loc)
	cfg := a.Cfg
	cfg.Timezone = DefaultShiftTimezone
	data := PageData{Title: title, Config: cfg, NowYear: now.Year(), NowMonth: int(now.Month()), NowDate: now.Format("2006-01-02"), TimeOptions: buildTimeOptions(), TimezoneOptions: buildTimezoneOptions()}
	data.Error = errMsg
	a.render(w, "dashboard", data)
}

func (a *App) handleApprovals(w http.ResponseWriter, r *http.Request) {
	data := a.basePage(r, "审批记录")
	data.Users, _ = a.Store.LoadUsers()
	approvals, err := a.Store.ListApprovals()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Approvals = approvals
	}
	a.render(w, "approvals", data)
}

func (a *App) handleApprovalApprove(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以通过 Web UI 审批生效", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/approvals", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Redirect(w, r, "/approvals", http.StatusSeeOther)
		return
	}
	approval, err := a.Store.ApproveByAdmin(id, a.Cfg.AdminUsername, a.Loc)
	if err != nil {
		a.renderError(w, "审批记录", "Web UI 审批通过失败: "+err.Error())
		return
	}
	if a.TG != nil {
		a.TG.editApprovalMessages(approval)
		a.TG.WakeNotificationQueue()
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

func (a *App) handleApprovalReject(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以通过 Web UI 拒绝审批", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/approvals", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Redirect(w, r, "/approvals", http.StatusSeeOther)
		return
	}
	approval, err := a.Store.RejectByAdmin(id, a.Cfg.AdminUsername, "web ui admin rejected")
	if err != nil {
		a.renderError(w, "审批记录", "Web UI 审批拒绝失败: "+err.Error())
		return
	}
	if a.TG != nil {
		a.TG.editApprovalMessages(approval)
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

func (a *App) handleSettingsRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/sso-settings", http.StatusSeeOther)
}

func (a *App) handleSSOSettings(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data := a.basePage(r, "SSO 配置")
	data.SSOSettings = a.effectiveSSOSettings()
	a.render(w, "sso_settings", data)
}

func (a *App) handleSSOSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		http.Error(w, "只有超级管理员可以修改 SSO 配置", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/sso-settings", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	current := a.effectiveSSOSettings()
	secret := strings.TrimSpace(r.FormValue("client_secret"))
	if secret == "" && r.FormValue("keep_client_secret") == "true" {
		secret = current.ClientSecret
	}
	settings := SSOSettings{
		Enabled:      r.FormValue("enabled") == "true",
		IssuerURL:    strings.TrimSpace(r.FormValue("issuer_url")),
		ClientID:     strings.TrimSpace(r.FormValue("client_id")),
		ClientSecret: secret,
		RedirectURL:  strings.TrimSpace(r.FormValue("redirect_url")),
		Scopes:       strings.TrimSpace(r.FormValue("scopes")),
		AdminUsers:   parseStringList(r.FormValue("admin_users")),
		AdminRoles:   parseStringList(r.FormValue("admin_roles")),
		UserRoles:    parseStringList(r.FormValue("user_roles")),
	}
	if strings.TrimSpace(settings.Scopes) == "" {
		settings.Scopes = "openid profile email"
	}
	if len(settings.UserRoles) == 0 {
		settings.UserRoles = []string{"devops-worker-user", "user"}
	}
	if err := a.Store.SaveSSOSettings(settings); err != nil {
		a.renderError(w, "SSO 配置", "保存 SSO 设置失败: "+err.Error())
		return
	}
	http.Redirect(w, r, "/sso-settings?saved=1", http.StatusSeeOther)
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	data := a.basePage(r, "历史查询")
	loc := a.requestLocation(r)
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().In(loc).Format("2006-01-02")
	}
	selectedTime, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		selectedTime = time.Now().In(loc)
		date = selectedTime.Format("2006-01-02")
	}
	year := parseQueryInt(r, "year", selectedTime.Year())
	month := parseQueryInt(r, "month", int(selectedTime.Month()))
	items, err := a.Store.LoadHistoryMonth(year, month)
	if err != nil {
		data.Error = err.Error()
	}
	dayItems, err := a.Store.LoadHistoryDay(date)
	if err != nil {
		data.Error = err.Error()
	}
	data.HistoryDate = date
	data.History = a.Store.BuildScheduleItemStatuses(dayItems, loc)
	a.fillCalendarWithLoc(&data, year, month, date, items, loc)
	a.fillDayStatusesWithLoc(&data, items, loc)
	data.SelectedDayItems = data.History
	data.ScheduleVersions, _ = a.Store.ListScheduleMonthVersions(year, month, loc)
	if len(data.ScheduleVersions) > 0 {
		data.SelectedVersionKey = data.ScheduleVersions[0].Key
	}
	a.render(w, "history", data)
}

func (a *App) resolveCalendarRequestWithLoc(r *http.Request, defaultYear, defaultMonth int, defaultDate string, loc *time.Location) (int, int, string) {
	year := parseQueryInt(r, "year", defaultYear)
	month := parseQueryInt(r, "month", defaultMonth)
	if month < 1 || month > 12 {
		month = defaultMonth
	}
	selected := r.URL.Query().Get("date")
	if selected == "" {
		selected = defaultDate
	}
	if t, err := time.ParseInLocation("2006-01-02", selected, loc); err == nil {
		if r.URL.Query().Get("year") == "" && r.URL.Query().Get("month") == "" {
			year, month = t.Year(), int(t.Month())
		}
	}
	return year, month, selected
}

func (a *App) fillCalendarWithLoc(data *PageData, year int, month int, selected string, items []ScheduleItem, loc *time.Location) {
	data.CalendarYear = year
	data.CalendarMonth = month
	data.SelectedDate = selected
	prev := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc).AddDate(0, -1, 0)
	next := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	data.CalendarPrevYear, data.CalendarPrevMonth = prev.Year(), int(prev.Month())
	data.CalendarNextYear, data.CalendarNextMonth = next.Year(), int(next.Month())
	data.CalendarDays = buildCalendarDays(year, month, selected, data.NowDate, items, loc)
}

func (a *App) fillDayStatusesWithLoc(data *PageData, items []ScheduleItem, loc *time.Location) {
	statuses := a.Store.BuildScheduleItemStatuses(items, loc)
	byDate := map[string][]ScheduleItemStatus{}
	for _, item := range statuses {
		byDate[item.Date] = append(byDate[item.Date], item)
	}
	data.DayStatus = byDate
}

func buildCalendarDays(year int, month int, selected string, today string, items []ScheduleItem, loc *time.Location) []CalendarDay {
	byDate := map[string][]ScheduleItem{}
	for _, item := range items {
		byDate[item.Date] = append(byDate[item.Date], item)
	}
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	startOffset := (int(first.Weekday()) + 6) % 7
	start := first.AddDate(0, 0, -startOffset)
	last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, loc)
	endOffset := 6 - ((int(last.Weekday()) + 6) % 7)
	end := last.AddDate(0, 0, endOffset)
	var days []CalendarDay
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format("2006-01-02")
		dayItems := append([]ScheduleItem(nil), byDate[date]...)
		sortScheduleItems(dayItems)
		isWeekend := d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
		calInfo := ChinaCalendar(date)
		days = append(days, CalendarDay{Date: date, Day: d.Day(), IsCurrentMonth: int(d.Month()) == month, IsToday: date == today, IsSelected: date == selected, IsWeekend: isWeekend, HolidayName: calInfo.Name, HolidayType: calInfo.Type, Items: dayItems})
	}
	return days
}

func filterItemsByDate(items []ScheduleItem, date string) []ScheduleItem {
	var out []ScheduleItem
	for _, item := range items {
		if item.Date == date {
			out = append(out, item)
		}
	}
	sortScheduleItems(out)
	return out
}

func parseQueryInt(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return v
}

func parseFormInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseIntSlice(vals []string) []int {
	var out []int
	for _, v := range vals {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func splitCSV(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range strings.Split(s, ",") {
		v := strings.TrimSpace(raw)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func enabledUsers(users []StaffUser) []StaffUser {
	out := make([]StaffUser, 0, len(users))
	for _, u := range users {
		if u.Enabled {
			out = append(out, u)
		}
	}
	return out
}

func enabledShifts(shifts []Shift) []Shift {
	out := make([]Shift, 0, len(shifts))
	for _, sh := range shifts {
		if sh.Enabled {
			out = append(out, sh)
		}
	}
	return out
}

func mustURL(path string) string {
	return fmt.Sprintf("%s", path)
}
