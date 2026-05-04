package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
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

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleDashboard)
	mux.HandleFunc("/users", a.handleUsers)
	mux.HandleFunc("/users/create", a.handleUserCreate)
	mux.HandleFunc("/users/update", a.handleUserUpdate)
	mux.HandleFunc("/users/delete", a.handleUserDelete)
	mux.HandleFunc("/schedule", a.handleSchedule)
	mux.HandleFunc("/schedule/submit", a.handleScheduleSubmit)
	mux.HandleFunc("/approvals", a.handleApprovals)
	mux.HandleFunc("/history", a.handleHistory)
	mux.Handle("/previews/", http.StripPrefix("/previews/", http.FileServer(http.Dir(filepath.Join(a.Cfg.DataDir, "previews")))))
	return a.basicAuth(mux)
}

func (a *App) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != a.Cfg.AdminUsername || pass != a.Cfg.AdminPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="devops-worker"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) render(w http.ResponseWriter, templateName string, data PageData) {
	var buf bytes.Buffer
	if err := renderPage(&buf, templateName, data); err != nil {
		log.Printf("render error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (a *App) basePage(title string) PageData {
	now := time.Now().In(a.Loc)
	return PageData{
		Title:    title,
		Config:   a.Cfg,
		NowYear:  now.Year(),
		NowMonth: int(now.Month()),
		NowDate:  now.Format("2006-01-02"),
		Months:   []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		WeekNums: []int{1, 2, 3, 4, 5},
	}
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := a.basePage("首页")
	active, err := a.Store.LoadActive()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Active = active
	}
	today := time.Now().In(a.Loc).Format("2006-01-02")
	items, err := a.Store.LoadDay(today)
	if err == nil {
		data.TodayItems = items
	}
	a.render(w, "dashboard", data)
}

func (a *App) handleUsers(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("用户管理")
	users, err := a.Store.LoadUsers()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Users = users
	}
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
	tgID := parseFormInt64(r.FormValue("telegram_user_id"))
	users, _ := a.Store.LoadUsers()
	now := time.Now().Format(time.RFC3339)
	users = append(users, StaffUser{ID: newID("user"), Name: name, TelegramUserID: tgID, Enabled: true, CreatedAt: now, UpdatedAt: now})
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
	for i := range users {
		if users[i].ID == id {
			users[i].Name = strings.TrimSpace(r.FormValue("name"))
			users[i].TelegramUserID = parseFormInt64(r.FormValue("telegram_user_id"))
			users[i].Enabled = r.FormValue("enabled") == "true"
			users[i].UpdatedAt = time.Now().Format(time.RFC3339)
		}
	}
	if err := a.Store.SaveUsers(users); err != nil {
		log.Printf("update user error: %v", err)
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	id := r.FormValue("id")
	users, _ := a.Store.LoadUsers()
	out := make([]StaffUser, 0, len(users))
	for _, u := range users {
		if u.ID != id {
			out = append(out, u)
		}
	}
	if err := a.Store.SaveUsers(out); err != nil {
		log.Printf("delete user error: %v", err)
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *App) handleSchedule(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("排班设置")
	users, _ := a.Store.LoadUsers()
	shifts, _ := a.Store.LoadShifts()
	data.Users = users
	data.Shifts = shifts
	a.render(w, "schedule", data)
}

func (a *App) handleScheduleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/schedule", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	year, _ := strconv.Atoi(r.FormValue("year"))
	month, _ := strconv.Atoi(r.FormValue("month"))
	weekNums := parseIntSlice(r.Form["week_nums"])
	weekdays := parseIntSlice(r.Form["weekdays"])
	staffIDs := r.Form["staff_ids"]
	shiftCode := strings.TrimSpace(r.FormValue("shift_code"))
	createdBy := strings.TrimSpace(r.FormValue("created_by"))
	if createdBy == "" {
		createdBy = "web"
	}
	if len(weekNums) == 0 || len(weekdays) == 0 || len(staffIDs) == 0 || shiftCode == "" {
		a.renderError(w, "排班提交失败", "请至少选择周次、星期、用户和班次")
		return
	}
	rule := ScheduleRule{ID: newID("rule"), Year: year, Month: month, WeekNums: weekNums, Weekdays: weekdays, StaffIDs: staffIDs, ShiftCode: shiftCode, Enabled: true}
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
	items, err := BuildScheduleItems([]ScheduleRule{rule}, users, shifts, a.Loc)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	active, _ := a.Store.LoadActive()
	html, err := RenderPreviewHTML("待生成", items, active.Revision, active.Revision+1)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	approval, err := a.Store.CreateApproval(createdBy, []ScheduleRule{rule}, items, html, a.Cfg.ApproverUserIDs)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
	}
	// 用最终审批 ID 重新渲染一次预览，方便附件显示准确 ID。
	html, _ = RenderPreviewHTML(approval.ID, items, approval.BaseRevision, approval.NewRevision)
	_ = writeFileAtomic(filepath.Join(a.Cfg.DataDir, approval.PreviewHTML), []byte(html))
	if a.TG != nil {
		if err := a.TG.SendApproval(approval); err != nil {
			log.Printf("telegram approval send error: %v", err)
		}
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

func (a *App) renderError(w http.ResponseWriter, title, errMsg string) {
	data := a.basePage(title)
	data.Error = errMsg
	a.render(w, "dashboard", data)
}

func (a *App) handleApprovals(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("审批记录")
	approvals, err := a.Store.ListApprovals()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Approvals = approvals
	}
	a.render(w, "approvals", data)
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("历史查询")
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().In(a.Loc).Format("2006-01-02")
	}
	items, err := a.Store.LoadHistoryDay(date)
	if err != nil {
		data.Error = err.Error()
	}
	data.HistoryDate = date
	data.History = items
	a.render(w, "history", data)
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

func mustURL(path string) string {
	return fmt.Sprintf("%s", path)
}
