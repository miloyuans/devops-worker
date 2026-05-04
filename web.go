package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleDashboard)
	mux.HandleFunc("/users", a.handleUsers)
	mux.HandleFunc("/users/create", a.handleUserCreate)
	mux.HandleFunc("/users/update", a.handleUserUpdate)
	mux.HandleFunc("/users/delete", a.handleUserDelete)
	mux.HandleFunc("/shifts", a.handleShifts)
	mux.HandleFunc("/shifts/create", a.handleShiftCreate)
	mux.HandleFunc("/shifts/update", a.handleShiftUpdate)
	mux.HandleFunc("/shifts/delete", a.handleShiftDelete)
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
		Title:           title,
		Config:          a.Cfg,
		NowYear:         now.Year(),
		NowMonth:        int(now.Month()),
		NowDate:         now.Format("2006-01-02"),
		Months:          []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		WeekNums:        []int{1, 2, 3, 4, 5},
		TimeOptions:     buildTimeOptions(),
		TimezoneOptions: buildTimezoneOptions(),
	}
}

func buildTimeOptions() []string {
	options := make([]string, 0, 24*12+2)
	for h := 0; h < 24; h++ {
		for m := 0; m < 60; m += 5 {
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
	data := a.basePage("首页")
	active, err := a.Store.LoadActive()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Active = active
	}
	data.Users, _ = a.Store.LoadUsers()
	year, month, selected := a.resolveCalendarRequest(r, data.NowYear, data.NowMonth, data.NowDate)
	a.fillCalendar(&data, year, month, selected, active.Items)
	a.fillDayStatuses(&data, active.Items)
	items := filterItemsByDate(active.Items, selected)
	data.SelectedDayItems = a.Store.BuildScheduleItemStatuses(items, a.Loc)
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

func (a *App) handleShifts(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("班次设置")
	shifts, err := a.Store.LoadShifts()
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Shifts = shifts
	}
	a.render(w, "shifts", data)
}

func (a *App) handleShiftCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/shifts", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	shift, err := normalizeShift(Shift{
		Code:      r.FormValue("code"),
		Name:      r.FormValue("name"),
		ShortName: r.FormValue("short_name"),
		Start:     r.FormValue("start"),
		End:       r.FormValue("end"),
		Timezone:  r.FormValue("timezone"),
		Enabled:   true,
	})
	if err != nil {
		a.renderError(w, "班次设置", err.Error())
		return
	}
	shifts, _ := a.Store.LoadShifts()
	for _, sh := range shifts {
		if sh.Code == shift.Code {
			a.renderError(w, "班次设置", "班次编码已存在")
			return
		}
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
			candidate := Shift{
				Code:      old.Code,
				Name:      r.FormValue("name"),
				ShortName: r.FormValue("short_name"),
				Start:     r.FormValue("start"),
				End:       r.FormValue("end"),
				Timezone:  r.FormValue("timezone"),
				Enabled:   r.FormValue("enabled") != "false",
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
	if summary, err := a.Store.UpdateFutureItemsForShift(updated, a.Loc); err != nil {
		log.Printf("update future schedule items failed: %v", err)
	} else if summary.ChangedItems > 0 {
		log.Printf("shift %s updated %d future schedule items, revision=%d version=%s", updated.Code, summary.ChangedItems, summary.NewRevision, summary.VersionID)
	}
	http.Redirect(w, r, "/shifts", http.StatusSeeOther)
}

func (a *App) handleShiftDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/shifts", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	code := r.FormValue("code")
	shifts, _ := a.Store.LoadShifts()
	out := make([]Shift, 0, len(shifts))
	for _, sh := range shifts {
		if sh.Code != code {
			out = append(out, sh)
		}
	}
	if err := a.Store.SaveShifts(out); err != nil {
		log.Printf("delete shift error: %v", err)
	}
	http.Redirect(w, r, "/shifts", http.StatusSeeOther)
}

func (a *App) handleSchedule(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("排班设置")
	users, _ := a.Store.LoadUsers()
	shifts, _ := a.Store.LoadShifts()
	active, _ := a.Store.LoadActive()
	data.Users = users
	data.Shifts = shifts
	data.Active = active
	year, month, selected := a.resolveCalendarRequest(r, data.NowYear, data.NowMonth, data.NowDate)
	a.fillCalendar(&data, year, month, selected, active.Items)
	a.fillDayStatuses(&data, active.Items)
	data.SelectedDayItems = a.Store.BuildScheduleItemStatuses(filterItemsByDate(active.Items, selected), a.Loc)
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
	createdBy := strings.TrimSpace(r.FormValue("created_by"))
	if createdBy == "" {
		createdBy = "web"
	}
	rules, err := a.parseScheduleSubmitRules(r, year, month)
	if err != nil {
		a.renderError(w, "排班提交失败", err.Error())
		return
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
	newItems, err := BuildScheduleItems(rules, users, shifts, a.Loc)
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

func (a *App) parseScheduleSubmitRules(r *http.Request, year int, month int) ([]ScheduleRule, error) {
	draftRaw := strings.TrimSpace(r.FormValue("draft_rules"))
	var rules []ScheduleRule
	if draftRaw != "" {
		var changes []ScheduleDraftChange
		if err := json.Unmarshal([]byte(draftRaw), &changes); err != nil {
			return nil, fmt.Errorf("草稿内容格式错误: %w", err)
		}
		for _, ch := range changes {
			if len(ch.Dates) == 0 || len(ch.StaffIDs) == 0 || strings.TrimSpace(ch.ShiftCode) == "" {
				continue
			}
			rules = append(rules, ScheduleRule{ID: newID("rule"), Year: year, Month: month, Dates: ch.Dates, StaffIDs: ch.StaffIDs, ShiftCode: strings.TrimSpace(ch.ShiftCode), Enabled: true})
		}
	} else {
		dates := splitCSV(r.FormValue("selected_dates"))
		staffIDs := r.Form["staff_ids"]
		shiftCode := strings.TrimSpace(r.FormValue("shift_code"))
		if len(dates) > 0 && len(staffIDs) > 0 && shiftCode != "" {
			rules = append(rules, ScheduleRule{ID: newID("rule"), Year: year, Month: month, Dates: dates, StaffIDs: staffIDs, ShiftCode: shiftCode, Enabled: true})
		}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("请先至少加入一条排班草稿，再统一提交审批")
	}
	return rules, nil
}

func (a *App) renderError(w http.ResponseWriter, title, errMsg string) {
	data := a.basePage(title)
	data.Error = errMsg
	a.render(w, "dashboard", data)
}

func (a *App) handleApprovals(w http.ResponseWriter, r *http.Request) {
	data := a.basePage("审批记录")
	data.Users, _ = a.Store.LoadUsers()
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
	selectedTime, err := time.ParseInLocation("2006-01-02", date, a.Loc)
	if err != nil {
		selectedTime = time.Now().In(a.Loc)
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
	data.History = a.Store.BuildScheduleItemStatuses(dayItems, a.Loc)
	a.fillCalendar(&data, year, month, date, items)
	a.fillDayStatuses(&data, items)
	data.SelectedDayItems = data.History
	a.render(w, "history", data)
}

func (a *App) resolveCalendarRequest(r *http.Request, defaultYear, defaultMonth int, defaultDate string) (int, int, string) {
	year := parseQueryInt(r, "year", defaultYear)
	month := parseQueryInt(r, "month", defaultMonth)
	if month < 1 || month > 12 {
		month = defaultMonth
	}
	selected := r.URL.Query().Get("date")
	if selected == "" {
		selected = defaultDate
	}
	if t, err := time.ParseInLocation("2006-01-02", selected, a.Loc); err == nil {
		if r.URL.Query().Get("year") == "" && r.URL.Query().Get("month") == "" {
			year, month = t.Year(), int(t.Month())
		}
	}
	return year, month, selected
}

func (a *App) fillCalendar(data *PageData, year int, month int, selected string, items []ScheduleItem) {
	data.CalendarYear = year
	data.CalendarMonth = month
	data.SelectedDate = selected
	prev := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, a.Loc).AddDate(0, -1, 0)
	next := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, a.Loc).AddDate(0, 1, 0)
	data.CalendarPrevYear, data.CalendarPrevMonth = prev.Year(), int(prev.Month())
	data.CalendarNextYear, data.CalendarNextMonth = next.Year(), int(next.Month())
	data.CalendarDays = buildCalendarDays(year, month, selected, data.NowDate, items, a.Loc)
}

func (a *App) fillDayStatuses(data *PageData, items []ScheduleItem) {
	statuses := a.Store.BuildScheduleItemStatuses(items, a.Loc)
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

func mustURL(path string) string {
	return fmt.Sprintf("%s", path)
}
