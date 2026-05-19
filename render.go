package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"sort"
	"strings"
	"time"
)

type PreviewDay struct {
	Day         int
	WeekdayName string
	IsWeekend   bool
}

type PreviewCell struct {
	Date        string
	Day         int
	WeekdayName string
	IsWeekend   bool
	ShiftName   string
	ShortName   string
	Class       string
}

type PreviewRow struct {
	StaffName string
	Cells     []PreviewCell
}

type PreviewMonth struct {
	Title string
	Days  []PreviewDay
	Rows  []PreviewRow
}

func RenderPreviewHTML(approvalID string, items []ScheduleItem, activeRevision int, newRevision int) (string, error) {
	months := buildPreviewMonths(items)
	data := struct {
		ApprovalID     string
		GeneratedAt    string
		ActiveRevision int
		NewRevision    int
		Items          []ScheduleItem
		Months         []PreviewMonth
	}{approvalID, time.Now().Format("2006-01-02 15:04:05"), activeRevision, newRevision, items, months}
	tpl := template.Must(template.New("preview").Funcs(template.FuncMap{"clock": formatClock}).Parse(previewTemplate))
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildPreviewMonths(items []ScheduleItem) []PreviewMonth {
	monthItems := map[string][]ScheduleItem{}
	for _, item := range items {
		if len(item.Date) >= 7 {
			monthItems[item.Date[:7]] = append(monthItems[item.Date[:7]], item)
		}
	}
	monthKeys := make([]string, 0, len(monthItems))
	for k := range monthItems {
		monthKeys = append(monthKeys, k)
	}
	sort.Strings(monthKeys)
	out := make([]PreviewMonth, 0, len(monthKeys))
	for _, ym := range monthKeys {
		t, err := time.Parse("2006-01", ym)
		if err != nil {
			continue
		}
		daysCount := daysInMonth(t.Year(), t.Month())
		days := make([]PreviewDay, daysCount)
		for i := 0; i < daysCount; i++ {
			d := time.Date(t.Year(), t.Month(), i+1, 0, 0, 0, 0, time.Local)
			days[i] = PreviewDay{Day: i + 1, WeekdayName: previewWeekdayName(d.Weekday()), IsWeekend: d.Weekday() == time.Saturday || d.Weekday() == time.Sunday}
		}
		staffNames := map[string]string{}
		by := map[string]ScheduleItem{}
		for _, item := range monthItems[ym] {
			staffNames[item.StaffID] = item.StaffName
			by[item.StaffID+"|"+item.Date] = item
		}
		staffIDs := make([]string, 0, len(staffNames))
		for id := range staffNames {
			staffIDs = append(staffIDs, id)
		}
		sort.Slice(staffIDs, func(i, j int) bool { return staffNames[staffIDs[i]] < staffNames[staffIDs[j]] })
		rows := make([]PreviewRow, 0, len(staffIDs))
		for _, staffID := range staffIDs {
			row := PreviewRow{StaffName: staffNames[staffID]}
			for day := 1; day <= daysCount; day++ {
				date := time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.Local).Format("2006-01-02")
				cell := PreviewCell{Date: date, Day: day, WeekdayName: previewWeekdayName(time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.Local).Weekday()), IsWeekend: time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.Local).Weekday() == time.Saturday || time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.Local).Weekday() == time.Sunday}
				if item, ok := by[staffID+"|"+date]; ok {
					cell.ShiftName = item.ShiftName
					cell.ShortName = item.ShiftShortName
					cell.Class = strings.ToLower(item.ShiftCode)
				}
				row.Cells = append(row.Cells, cell)
			}
			rows = append(rows, row)
		}
		out = append(out, PreviewMonth{Title: ym, Days: days, Rows: rows})
	}
	return out
}

func previewWeekdayName(w time.Weekday) string {
	switch w {
	case time.Monday:
		return "周一"
	case time.Tuesday:
		return "周二"
	case time.Wednesday:
		return "周三"
	case time.Thursday:
		return "周四"
	case time.Friday:
		return "周五"
	case time.Saturday:
		return "周六"
	case time.Sunday:
		return "周日"
	default:
		return ""
	}
}

func renderPage(wr *bytes.Buffer, name string, data PageData) error {
	funcs := template.FuncMap{
		"clock":            templateClock,
		"dateOnly":         templateDateOnly,
		"json":             templateJSON,
		"versionLabel":     templateVersionLabel,
		"compact":          compactID,
		"tgName":           templateTGName,
		"tgNames":          templateTGNames,
		"approvalStatus":   templateApprovalStatus,
		"approvalReviewer": templateApprovalReviewer,
		"shortTime":        templateShortTime,
		"shortTimeTZ":      templateShortTimeTZ,
		"canEditShift":     templateCanEditShift,
		"canEditUser":      templateCanEditUser,
		"shiftNotify":      shiftNotificationEnabled,
		"join":             templateJoin,
		"userInitial":      templateUserInitial,
	}

	tpl := template.Must(template.New("base").Funcs(funcs).Parse(baseTemplate + dashboardTemplate + usersTemplate + shiftsTemplate + scheduleTemplate + approvalsTemplate + historyTemplate + settingsTemplate))
	return tpl.ExecuteTemplate(wr, name, data)
}

func templateJoin(values []string) string {
	return strings.Join(values, ",")
}

func templateUserInitial(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "U"
	}
	for _, r := range name {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return strings.ToUpper(string(r))
		}
	}
	return "U"
}

func templateClock(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("15:04")
	}
	return s
}

func templateDateOnly(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02")
	}
	return s
}

func templateShortTime(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("01-02 15:04")
	}
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func templateShortTimeTZ(s string, tz string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		if loc, err := time.LoadLocation(strings.TrimSpace(tz)); err == nil {
			return t.In(loc).Format("01-02 15:04")
		}
		return t.Format("01-02 15:04")
	}
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func templateJSON(v any) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b)
}

func templateVersionLabel(a ActiveSchedule) string {
	if a.VersionID != "" {
		return a.VersionID
	}
	return newVersionID(a.Revision)
}

func templateTGName(id int64, users []StaffUser) string {
	if id == 0 {
		return "-"
	}
	for _, u := range users {
		if u.TelegramUserID == id {
			return u.Name
		}
	}
	return "未绑定审批人"
}

func templateTGNames(ids []int64, users []StaffUser) string {
	if len(ids) == 0 {
		return "未配置"
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, templateTGName(id, users))
	}
	return strings.Join(names, "、")
}

func templateApprovalReviewer(a Approval, users []StaffUser) string {
	if a.Status == "pending" {
		return templateTGNames(a.ApproverIDs, users)
	}
	if strings.TrimSpace(a.ReviewedByName) != "" {
		return a.ReviewedByName
	}
	if a.ReviewedBy != 0 {
		return templateTGName(a.ReviewedBy, users)
	}
	return "-"
}

func templateCanEditShift(sh Shift, isAdmin bool) bool {
	return isAdmin || sh.CreatedBy == "user"
}

func templateCanEditUser(u StaffUser, isAdmin bool) bool {
	return isAdmin || u.CreatedBy == "user"
}

func templateApprovalStatus(s string) string {
	switch s {
	case "pending":
		return "待审批"
	case "approved":
		return "已同意"
	case "rejected":
		return "已拒绝"
	default:
		return s
	}
}

const previewTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>排班审批预览</title>
<style>
:root{--bg:#f5f8fb;--card:#fff;--line:#dbe4ef;--text:#0f172a;--muted:#64748b;--accent:#2563eb;--weekend:#fff8e6;--holiday:#fff1f2}*{box-sizing:border-box}body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Noto Sans SC",sans-serif;background:linear-gradient(135deg,#eef6ff,#f8fafc);color:var(--text);margin:0;padding:18px}.wrap{max-width:1320px;margin:auto}.card{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:18px;box-shadow:0 14px 45px rgba(15,23,42,.08);margin-bottom:14px}.kicker{color:var(--accent);letter-spacing:.12em;font-size:12px;text-transform:uppercase}.muted{color:var(--muted)}.summary{display:grid;grid-template-columns:repeat(3,minmax(120px,1fr));gap:10px;margin:14px 0}.box{background:#f8fafc;border:1px solid var(--line);border-radius:14px;padding:10px}.box b{display:block;font-size:20px;margin-top:4px}.matrix-wrap{overflow:hidden;border:1px solid #e2e8f0;border-radius:14px;background:#fff}.matrix{width:100%;border-collapse:separate;border-spacing:0;table-layout:fixed}.matrix th,.matrix td{border-bottom:1px solid #e5edf6;border-right:1px solid #eef2f7;text-align:center;font-size:10px;height:26px;padding:0}.matrix th{background:#f8fafc;color:#475569}.matrix th.weekend{background:#fff8e6}.matrix th small{display:block;font-size:8px;font-weight:500;color:#94a3b8;line-height:1.1}.matrix .user-col{width:96px;text-align:left;padding:5px 7px;font-weight:700;background:#fff}.matrix th.user-col{background:#f8fafc}.cell.weekend{background:#fff8e6}.mini{display:inline-flex;align-items:center;justify-content:center;min-width:18px;height:18px;border-radius:999px;background:#dbeafe;color:#1e40af;border:1px solid #bfdbfe;font-weight:700;font-size:9px;padding:0 4px}.mini.rest{background:#f1f5f9;color:#64748b;border-color:#cbd5e1}.mini.annual_leave{background:#fef3c7;color:#92400e;border-color:#fde68a}.mini.sick_leave{background:#fee2e2;color:#991b1b;border-color:#fecaca}.empty{padding:20px;color:#64748b;text-align:center}@media(max-width:900px){body{padding:8px}.card{padding:10px}.matrix th,.matrix td{font-size:8px}.matrix .user-col{width:72px}.mini{font-size:8px;min-width:16px;height:16px;padding:0 2px}}
</style>
</head><body><div class="wrap"><div class="card"><div class="kicker">devops-worker approval preview</div><h1>排班策略生效月历预览</h1><p class="muted">审批ID：{{.ApprovalID}} ｜ 生成时间：{{.GeneratedAt}}</p><div class="summary"><div class="box">当前版本<b>{{.ActiveRevision}}</b></div><div class="box">审批后版本<b>{{.NewRevision}}</b></div><div class="box">排班记录<b>{{len .Items}}</b></div></div><p class="muted">此预览为审批通过后最终生效的整月排班矩阵，按用户逐行展示。</p></div>{{range .Months}}<div class="card"><h2>{{.Title}} 排班矩阵</h2><div class="matrix-wrap"><table class="matrix"><thead><tr><th class="user-col">用户</th>{{range .Days}}<th class="{{if .IsWeekend}}weekend{{end}}"><span>{{.Day}}</span><small>{{.WeekdayName}}</small></th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr><td class="user-col">{{.StaffName}}</td>{{range .Cells}}<td class="cell {{if .IsWeekend}}weekend{{end}}"><span {{if .ShiftName}}class="mini {{.Class}}" title="{{.Date}} {{.ShiftName}}"{{end}}>{{.ShortName}}</span></td>{{end}}</tr>{{end}}</tbody></table></div></div>{{else}}<div class="card empty">没有生成任何排班记录</div>{{end}}</div></body></html>`

const baseTemplate = `{{define "layout_start"}}<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} - devops-worker</title><style>
:root{--bg:#eef3f8;--panel:#0f172a;--card:#fff;--line:#d9e3ef;--text:#0f172a;--muted:#64748b;--accent:#2563eb;--sky:#38bdf8;--green:#d9fbe5;--green-line:#86efac;--weekend:#fff8e6;--holiday:#fff1f2;--work:#fff7ed;--ok:#16a34a;--bad:#ef4444;--select:#dbeafe;--select-line:#60a5fa}*{box-sizing:border-box}body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Noto Sans SC",sans-serif;background:linear-gradient(135deg,#ecf5ff,#f8fafc 45%,#eef2f7);margin:0;color:var(--text);min-height:100vh;overflow:hidden}a{color:#2563eb;text-decoration:none}.shell{display:grid;grid-template-columns:136px 1fr;height:100vh}.side{background:linear-gradient(180deg,#0f172a,#111827);color:#e5edf7;border-right:1px solid rgba(255,255,255,.08);padding:12px 8px}.brand{display:flex;align-items:center;gap:7px;margin:4px 5px 14px}.logo{width:24px;height:24px;border-radius:8px;background:linear-gradient(135deg,#38bdf8,#2563eb);box-shadow:0 0 20px rgba(56,189,248,.26)}.brand b{font-size:13px}.brand .hint{font-size:10px;color:#94a3b8}.nav a{display:flex;align-items:center;gap:6px;padding:8px 9px;margin:3px 0;border-radius:11px;color:#cbd5e1;font-size:12px}.nav a:hover{background:rgba(148,163,184,.14);color:#fff}.content{height:100vh;overflow:hidden;padding:12px 14px}.topline{display:flex;align-items:center;justify-content:space-between;margin-bottom:8px}.kicker{color:var(--accent);letter-spacing:.12em;font-size:10px;text-transform:uppercase}.topline h1{margin:2px 0 0;font-size:18px}.version-box{display:flex;gap:6px;align-items:center;flex-wrap:wrap;justify-content:flex-end}.tag{display:inline-flex;align-items:center;border:1px solid var(--line);background:rgba(255,255,255,.74);border-radius:999px;padding:4px 8px;color:#334155;font-size:11px}.user-tag{gap:6px;max-width:260px}.user-avatar{width:20px;height:20px;border-radius:999px;background:linear-gradient(135deg,#dbeafe,#bae6fd);display:inline-flex;align-items:center;justify-content:center;color:#1e40af;font-weight:900;font-size:10px}.user-text{display:flex;flex-direction:column;line-height:1.08;min-width:0}.user-text b{font-size:11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:172px}.user-text small{font-size:9px;color:#64748b;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:172px}.role-admin{border-color:#bfdbfe;background:#eff6ff;color:#1e40af}.role-user{border-color:#e2e8f0;background:#f8fafc;color:#475569}.tz-picker{gap:5px;padding:2px 4px 2px 8px}.tz-picker select{border:0;background:transparent;margin:0;padding:2px 4px;font-size:11px;max-width:132px}.card{background:rgba(255,255,255,.92);border:1px solid var(--line);border-radius:16px;padding:12px;box-shadow:0 12px 35px rgba(15,23,42,.07)}.panel-card{background:rgba(255,255,255,.96);border:1px solid var(--line);border-radius:16px;padding:11px;box-shadow:0 12px 38px rgba(15,23,42,.07);min-height:0}.workbench{display:grid;grid-template-columns:minmax(600px,56%) minmax(460px,44%);gap:11px;height:calc(100vh - 64px);min-height:0}.workbench.schedule-workbench{grid-template-columns:minmax(410px,34%) minmax(720px,66%)}.schedule-workbench .calendar-card{padding:9px}.schedule-workbench .calendar{gap:4px}.schedule-workbench .day{padding:5px;border-radius:11px}.schedule-workbench .daynum{font-size:12px}.schedule-workbench .chip{font-size:8px;padding:2px 3px}.schedule-workbench .calendar-sub{font-size:10px}.calendar-card{display:flex;flex-direction:column;min-height:0}.detail-card{display:flex;flex-direction:column;min-height:0;overflow:hidden}.detail-scroll{overflow:hidden;padding-right:2px;min-height:0}.calendar-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:7px;gap:8px}.calendar-title{font-size:16px;font-weight:800}.calendar-sub{font-size:11px;color:var(--muted);margin-top:2px}.calendar{display:grid;grid-template-columns:repeat(7,1fr);gap:5px;flex:1;min-height:0}.weekday{text-align:center;color:#64748b;font-size:11px;padding:3px;border-radius:8px}.calendar .weekday:nth-child(6),.calendar .weekday:nth-child(7){background:#fff8e6;color:#92400e}.day{border:1px solid var(--line);border-radius:12px;background:#fff;padding:6px;position:relative;overflow:hidden;transition:.12s;display:block;text-align:left;color:var(--text);width:100%;font:inherit;min-height:0}.day:hover{border-color:#93c5fd;box-shadow:0 8px 22px rgba(37,99,235,.09)}.day.weekend{background:var(--weekend)}.day.holiday{background:var(--holiday);border-color:#fecdd3}.day.workday{background:var(--work);border-color:#fed7aa}.day.dim{opacity:.38}.day.today{background:var(--green);border-color:var(--green-line)}.day.selected{background:var(--select);border-color:var(--select-line);box-shadow:0 0 0 2px rgba(37,99,235,.14)}.day.range-anchor{outline:2px dashed rgba(37,99,235,.35);outline-offset:-4px}.day.today.selected{background:#ccfbf1;border-color:#14b8a6;box-shadow:0 0 0 2px rgba(20,184,166,.16)}.daytop{display:flex;justify-content:space-between;gap:4px;align-items:flex-start}.daynum{font-weight:800;font-size:13px}.holiday-label{font-size:9px;line-height:1;max-width:44px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;border-radius:999px;padding:2px 4px;background:rgba(255,255,255,.78);border:1px solid rgba(148,163,184,.28);color:#9f1239}.workday .holiday-label{color:#9a3412}.day .items{margin-top:4px;display:flex;gap:3px;flex-wrap:wrap}.chip{font-size:9px;line-height:1;padding:3px 4px;border-radius:999px;background:#dbeafe;color:#1e40af;border:1px solid #bfdbfe;max-width:100%;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.muted,.hint{color:var(--muted);font-size:11px}.msg{padding:8px 10px;border-radius:11px;background:#dcfce7;border:1px solid #86efac;color:#166534;margin-bottom:8px}.err{padding:8px 10px;border-radius:11px;background:#fee2e2;border:1px solid #fca5a5;color:#991b1b;margin-bottom:8px}table{width:100%;border-collapse:collapse;background:#fff;border-radius:13px;overflow:hidden}th,td{border-bottom:1px solid #e5edf6;padding:7px;text-align:left;vertical-align:middle;font-size:12px}th{color:#475569;background:#f8fafc;font-weight:700}.btn,button{display:inline-flex;align-items:center;justify-content:center;gap:5px;border:0;border-radius:10px;background:linear-gradient(135deg,#2563eb,#38bdf8);color:#fff;padding:7px 10px;cursor:pointer;font-weight:700;font-size:12px}.btn.secondary,button.secondary{background:#eef2f7;border:1px solid #dbe4ef;color:#334155}.btn.danger,button.danger{background:linear-gradient(135deg,#dc2626,#f97316)}button:disabled{opacity:.45;cursor:not-allowed}input,select{padding:7px 8px;border:1px solid #dbe4ef;border-radius:10px;margin:2px;background:#fff;color:var(--text);outline:none;font-size:12px}input:focus,select:focus{border-color:#93c5fd;box-shadow:0 0 0 3px rgba(37,99,235,.1)}.row{display:flex;gap:7px;flex-wrap:wrap;align-items:center}.field-mini{display:inline-flex;align-items:center;gap:5px;color:#64748b;font-size:11px}.shift-form select{min-width:92px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:9px}.badge{display:inline-flex;align-items:center;gap:4px;padding:3px 7px;border-radius:999px;border:1px solid #bae6fd;background:#e0f2fe;color:#075985;font-size:11px}.pill{display:inline-flex;align-items:center;gap:4px;padding:3px 7px;border-radius:999px;font-size:11px;border:1px solid #e2e8f0;background:#f1f5f9;color:#64748b}.pill.ok{background:#dcfce7;border-color:#86efac;color:#166534}.pill.off,.pill.muted{background:#f1f5f9;border-color:#e2e8f0;color:#64748b}.meta-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:7px;margin-bottom:8px}.meta{background:#f8fafc;border:1px solid #e2e8f0;border-radius:12px;padding:7px}.meta .label{font-size:10px;color:#64748b}.meta b{display:block;margin-top:2px;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.list{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:6px;align-content:start}.list.many{grid-template-columns:repeat(auto-fit,minmax(118px,1fr));gap:5px}.item-card{border:1px solid #e2e8f0;background:#fff;border-radius:12px;padding:8px;font-size:12px}.list.many .item-card{padding:5px;font-size:10px;border-radius:10px}.list.many .row{gap:3px;margin-top:4px!important}.list.many .pill,.list.many .badge{font-size:9px;padding:2px 5px}.item-head{display:flex;justify-content:space-between;gap:6px;align-items:center}.shift-groups{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:7px;align-content:start}.shift-group{border:1px solid #e2e8f0;background:rgba(255,255,255,.96);border-radius:12px;padding:7px;min-width:0}.shift-group-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:5px}.shift-user-list{display:grid;gap:4px}.shift-user-row{display:grid;grid-template-columns:minmax(58px,1fr) minmax(58px,auto) auto auto auto;align-items:center;gap:4px;border:1px solid #edf2f7;background:#f8fafc;border-radius:10px;padding:4px 5px;font-size:10px;min-width:0}.shift-user-row b{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.phone-mini{font-size:9px;color:#475569;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.shift-user-row .pill{font-size:9px;padding:2px 5px}.empty-detail{border:1px dashed #cbd5e1;background:#f8fafc;border-radius:12px;padding:12px}.shift-morning{background:#dbeafe;color:#1e40af}.shift-middle{background:#e0f2fe;color:#075985}.shift-night{background:#ede9fe;color:#5b21b6}.shift-normal{background:#dcfce7;color:#166534}.shift-rest{background:#f1f5f9;color:#475569}.shift-annual_leave{background:#fef3c7;color:#92400e}.shift-sick_leave{background:#fee2e2;color:#991b1b}@media(max-height:760px){.shift-groups{grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:5px}.shift-group{padding:5px}.shift-user-row{font-size:9px;padding:3px 4px}.shift-user-row .pill{font-size:8px;padding:1px 4px}}.modal-backdrop{display:none;position:fixed;inset:0;background:rgba(15,23,42,.35);backdrop-filter:blur(8px);z-index:20}.modal{max-width:760px;margin:6vh auto;background:#fff;border:1px solid var(--line);border-radius:18px;padding:16px;box-shadow:0 28px 100px rgba(15,23,42,.25)}.modal-backdrop.show{display:block}.checkbox-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(118px,1fr));gap:7px;max-height:240px;overflow:auto}.checkbox-card{display:flex;align-items:center;gap:6px;border:1px solid #e2e8f0;background:#f8fafc;border-radius:12px;padding:8px}.draft-box{margin-top:8px;border:1px dashed #bfdbfe;background:#f8fbff;border-radius:13px;padding:8px}.draft-list{display:grid;gap:5px;max-height:90px;overflow:auto}.draft-item{display:flex;justify-content:space-between;gap:6px;align-items:center;padding:6px 8px;border:1px solid #e2e8f0;background:#fff;border-radius:10px;font-size:11px}.month-tools{display:grid;grid-template-columns:1fr;gap:5px;margin-bottom:6px}.month-tools .toolbar{display:flex;gap:6px;align-items:center;flex-wrap:wrap}.month-matrix-wrap{border:1px solid #e2e8f0;border-radius:13px;background:#fff;overflow:hidden;flex:1;min-height:0}.month-matrix{border-collapse:separate;border-spacing:0;width:100%;min-width:0;table-layout:fixed}.month-matrix th,.month-matrix td{padding:0;border-bottom:1px solid #e5edf6;border-right:1px solid #eef2f7;text-align:center;font-size:10px}.month-matrix th{height:30px;background:#f8fafc;position:sticky;top:0;z-index:2}.month-matrix th.weekend{background:#fff8e6}.month-matrix th .day-head{display:flex;flex-direction:column;align-items:center;line-height:1.05}.month-matrix th .day-head small{font-size:8px;font-weight:500;color:#94a3b8;margin-top:2px}.month-matrix .user-col{position:sticky;left:0;z-index:3;background:#fff;text-align:left;width:92px;min-width:92px;max-width:92px;padding:5px 6px}.month-matrix th.user-col{background:#f8fafc;z-index:4}.matrix-cell{height:28px;min-width:0;background:#fff}.matrix-cell.weekend{background:#fff8e6}.matrix-cell.holiday{background:#fff1f2}.matrix-cell.has-shift{background:#eff6ff}.matrix-cell.cell-selected{outline:2px solid #60a5fa;outline-offset:-2px;background:#dbeafe}.matrix-shift-select{width:100%;min-width:0;height:24px;margin:0;border:0;border-radius:0;background:transparent;text-align:center;text-align-last:center;font-size:10px;padding:0 1px;color:#1e40af;font-weight:700;cursor:pointer}.matrix-shift-select.rest{color:#64748b;background:#f8fafc}.matrix-shift-select.annual_leave{color:#92400e;background:#fef3c7}.matrix-shift-select.sick_leave{color:#991b1b;background:#fee2e2}.matrix-shift-select.draft{box-shadow:inset 0 0 0 2px #60a5fa;background:#dbeafe}.user-check{display:flex;align-items:center;gap:5px;white-space:nowrap}.user-picker-box,.shift-picker-box{background:#f8fafc;border:1px solid #dbe4ef;border-radius:13px;padding:8px}.picker-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:6px}.user-picker-grid{display:flex;flex-wrap:wrap;gap:5px;align-content:flex-start}.user-tile{background:#fff;border:1px solid #dbe4ef;color:#0f172a;border-radius:10px;padding:5px 7px;justify-content:flex-start;box-shadow:none;font-size:11px;min-width:82px;width:auto}.user-tile.selected{background:#dbeafe;border-color:#60a5fa;color:#1e40af;box-shadow:0 0 0 2px rgba(37,99,235,.10)}.shift-picker-grid{display:flex;flex-wrap:wrap;gap:5px;align-content:flex-start}.shift-tile{display:flex;flex-direction:column;align-items:flex-start;gap:2px;background:#fff;border:1px solid #dbe4ef;color:#0f172a;border-radius:10px;padding:5px 7px;box-shadow:none;text-align:left;min-width:138px;width:auto}.shift-tile b{font-size:11px}.shift-tile .time{font-size:9px;color:#475569}.shift-tile .sub{font-size:8px;color:#64748b}.shift-tile.selected{background:#ecfeff;border-color:#22d3ee;color:#075985;box-shadow:0 0 0 2px rgba(6,182,212,.12)}.selected-note{font-size:10px;color:#475569;background:#f8fafc;border:1px solid #e2e8f0;border-radius:10px;padding:5px 7px}.picker-row{display:grid;grid-template-columns:1fr;gap:6px}.schedule-meta-mini{margin-bottom:4px}.schedule-meta-mini summary{cursor:pointer;color:#64748b;font-size:11px;list-style:none}.schedule-meta-mini summary::-webkit-details-marker{display:none}.schedule-meta-mini summary span{display:inline-flex;border:1px solid #e2e8f0;background:#f8fafc;border-radius:999px;padding:3px 7px}.schedule-meta-mini .meta-grid{margin-top:5px;margin-bottom:0}.schedule-heading-row{display:flex;align-items:center;justify-content:space-between;gap:8px;margin:0 0 5px}.schedule-heading-row h2{margin:0;font-size:15px}@media(max-width:1100px){body{overflow:auto}.shell{grid-template-columns:1fr;height:auto}.side{height:auto}.content{height:auto;overflow:visible}.workbench{grid-template-columns:1fr;height:auto}.calendar{min-height:620px}.detail-scroll{overflow:auto}}@media(max-height:760px){.day .items .chip:nth-child(n+3){display:none}.day{padding:5px}.calendar{gap:4px}.weekday{padding:2px}.content{padding:9px}.workbench{height:calc(100vh - 58px)}.calendar-title{font-size:15px}.calendar-sub{display:none}.topline{margin-bottom:6px}.item-card{padding:5px}.chip{font-size:8px}.month-matrix th,.month-matrix td{font-size:9px}.matrix-cell{height:22px}.matrix-cell .mini{height:16px;min-width:16px;font-size:8px}}
</style><script>
function esc(v){return String(v ?? '').replace(/[&<>'"]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c];});}
function changeDisplayTimezone(tz){document.cookie='devops_worker_tz='+encodeURIComponent(tz)+'; Path=/; Max-Age=31536000; SameSite=Lax';const u=new URL(window.location.href);u.searchParams.set('tz',tz);window.location.href=u.toString();}
function itemClock(row,field,fallback){return row[field] || fallback || '-';}
function shiftGroupRank(row){const code=String(row.shift_code||'').toLowerCase();const name=String((row.shift_name||'')+(row.shift_short_name||''));if(code==='morning'||name.indexOf('早')>=0)return 10;if(code==='middle'||name.indexOf('中')>=0)return 20;if(code==='night'||name.indexOf('晚')>=0||name.indexOf('夜')>=0)return 30;if(code==='normal'||name.indexOf('正常')>=0)return 40;if(code==='rest'||name.indexOf('休')>=0)return 50;if(code==='annual_leave'||name.indexOf('年')>=0)return 60;if(code==='sick_leave'||name.indexOf('病')>=0)return 70;return 99;}
function renderDetail(dayStatus,date,targetId,emptyText){const target=document.getElementById(targetId); if(!target)return; const rows=(dayStatus[date]||[]).slice(); document.querySelectorAll('[data-current-date]').forEach(function(el){el.textContent=date;}); if(rows.length===0){target.className='shift-groups';target.innerHTML='<div class="empty-detail muted">'+emptyText+'</div>';return;} rows.sort(function(a,b){const ra=shiftGroupRank(a), rb=shiftGroupRank(b); if(ra!==rb)return ra-rb; return String(a.staff_name||'').localeCompare(String(b.staff_name||''),'zh-CN');}); const groups=[]; rows.forEach(function(r){let g=groups.find(function(x){return x.code===r.shift_code;}); if(!g){g={code:r.shift_code,name:r.shift_name||r.shift_code,rank:shiftGroupRank(r),start:r.start_clock||'',end:r.end_clock||'',items:[]}; groups.push(g);} g.items.push(r);}); groups.sort(function(a,b){return a.rank-b.rank;}); target.className='shift-groups'; target.innerHTML=groups.map(function(g){const time=(g.start||g.end)?('<span class="hint">'+esc(g.start||'-')+' - '+esc(g.end||'-')+'</span>'):'';return '<section class="shift-group"><div class="shift-group-head"><span class="badge shift-'+esc(g.code)+'">'+esc(g.name)+'</span>'+time+'<span class="hint">'+g.items.length+' 人</span></div><div class="shift-user-list">'+g.items.map(function(r){return '<div class="shift-user-row"><b>'+esc(r.staff_name)+'</b>'+(r.staff_phone?'<span class="phone-mini">☎ '+esc(r.staff_phone)+'</span>':'')+'<span class="pill '+esc(r.notify_status)+'">'+esc(r.notify_status_label)+'</span><span class="pill '+esc(r.read_status)+'">'+esc(r.read_status_label)+'</span>'+(r.telegram_user_id?'<span class="pill ok">TG</span>':'<span class="pill off">未绑</span>')+'</div>';}).join('')+'</div></section>';}).join('');}
function bindCalendar(opts){const status=opts.status||{};let selected=opts.selected||'';let rangeAnchor='';const selectedSet=new Set(opts.multi?[]:[selected]);const cal=document.getElementById(opts.calendarId);if(!cal)return;const input=opts.inputId?document.getElementById(opts.inputId):null;const hint=opts.hintId?document.getElementById(opts.hintId):null;function current(){return [...selectedSet].sort();}function datesBetween(a,b){if(!a||!b)return[];const start=a<=b?a:b;const end=a<=b?b:a;function parseDateUTC(s){const p=s.split('-').map(Number);return new Date(Date.UTC(p[0],p[1]-1,p[2]));}function fmtUTC(d){const y=d.getUTCFullYear();const m=String(d.getUTCMonth()+1).padStart(2,'0');const day=String(d.getUTCDate()).padStart(2,'0');return y+'-'+m+'-'+day;}let d=parseDateUTC(start);const e=parseDateUTC(end);const out=[];while(d.getTime()<=e.getTime()){out.push(fmtUTC(d));d.setUTCDate(d.getUTCDate()+1);}return out;}function paint(){cal.querySelectorAll('.day').forEach(function(el){const d=el.dataset.date;el.classList.toggle('selected',selectedSet.has(d));el.classList.toggle('range-anchor',rangeAnchor===d);});if(input)input.value=current().join(',');if(hint){const arr=current();let msg=arr.length?('已选择 '+arr.length+' 天：'+arr.join(', ')):'尚未选择日期';if(opts.multi&&rangeAnchor&&arr.length===1)msg+='，再次点击结束日期可自动选中区间';hint.textContent=msg;}if(opts.modalDatesId){const md=document.getElementById(opts.modalDatesId);if(md){const arr=current();md.textContent=arr.length?arr.join(', '):'-';}}if(typeof window.onScheduleDateSelectionChange==='function')window.onScheduleDateSelectionChange(current());}function show(d){document.querySelectorAll('[data-current-date]').forEach(function(el){el.textContent=d;});if(opts.detailId)renderDetail(status,d,opts.detailId,opts.emptyText||'当天没有排班');}cal.querySelectorAll('.day').forEach(function(el){el.addEventListener('click',function(ev){const d=el.dataset.date;if(!d)return;if(opts.multi){if(ev.ctrlKey||ev.metaKey){if(selectedSet.has(d)){selectedSet.delete(d)}else{selectedSet.add(d)}rangeAnchor=d;}else if(!rangeAnchor||selectedSet.size===0){selectedSet.clear();selectedSet.add(d);rangeAnchor=d;}else{selectedSet.clear();datesBetween(rangeAnchor,d).forEach(function(x){selectedSet.add(x);});rangeAnchor='';}selected=d;}else{selectedSet.clear();selectedSet.add(d);selected=d;}show(d);paint();});});window.clearSelectedDates=function(){selectedSet.clear();rangeAnchor='';paint();};window.setSelectedDates=function(arr){selectedSet.clear();(arr||[]).forEach(function(d){if(d)selectedSet.add(d);});if(arr&&arr.length)selected=arr[arr.length-1];rangeAnchor='';show(selected);paint();};window.openScheduleModal=function(){if(selectedSet.size===0){alert('请先在日历中选择日期');return;}paint();const m=document.getElementById('scheduleModal');if(m)m.classList.add('show');};window.closeScheduleModal=function(){const m=document.getElementById('scheduleModal');if(m)m.classList.remove('show');};window.getSelectedDates=function(){return current();};paint();show(selected);}
function initScheduleDraft(opts){let draft=[];let modalDraft=[];const key='devops-worker-draft-'+opts.year+'-'+opts.month;try{draft=normalizeDraft(JSON.parse(localStorage.getItem(key)||'[]')||[])}catch(e){draft=[]}function normalizeDraft(list){const latest={};(list||[]).forEach(function(ch){const shift=String(ch.shift_code||'').trim();if(!shift)return;(ch.dates||[]).forEach(function(d){d=String(d||'').trim();if(!d)return;(ch.staff_ids||[]).forEach(function(u){u=String(u||'').trim();if(!u)return;latest[d+'|'+u]=shift;});});});return Object.keys(latest).sort().map(function(k){const p=k.split('|');return{dates:[p[0]],staff_ids:[p[1]],shift_code:latest[k]};});}function groupDraft(list){const groups={};normalizeDraft(list).forEach(function(ch){const k=ch.shift_code+'|'+ch.staff_ids[0];if(!groups[k])groups[k]={shift_code:ch.shift_code,staff_id:ch.staff_ids[0],dates:[]};groups[k].dates.push(ch.dates[0]);});return Object.keys(groups).sort().map(function(k){groups[k].dates.sort();return groups[k];});}function formatDates(ds){ds=(ds||[]).slice().sort();if(ds.length===0)return '';if(ds.length===1)return ds[0];if(ds.length>8)return ds[0]+' ~ '+ds[ds.length-1]+'（'+ds.length+'天）';return ds.join(', ');}function save(){draft=normalizeDraft(draft);localStorage.setItem(key,JSON.stringify(draft));renderDraft();if(typeof window.refreshSchedulePreviewMatrix==='function')window.refreshSchedulePreviewMatrix(draft);if(typeof window.refreshScheduleCalendar==='function')window.refreshScheduleCalendar(draft);}function renderDraft(){const box=document.getElementById('draftList');const empty=document.getElementById('draftEmpty');const submit=document.getElementById('submitDraftBtn');if(!box)return;draft=normalizeDraft(draft);const groups=groupDraft(draft);if(groups.length===0){box.innerHTML='';if(empty)empty.style.display='block';if(submit)submit.disabled=true;return;}if(empty)empty.style.display='none';if(submit)submit.disabled=false;box.innerHTML=groups.map(function(g,i){return '<div class="draft-item"><span>'+esc(formatDates(g.dates))+' ｜ '+esc(opts.userMap[g.staff_id]||g.staff_id)+' ｜ '+esc(opts.shiftMap[g.shift_code]||g.shift_code)+'</span><button type="button" class="secondary" onclick="removeDraftGroup('+i+')">移除</button></div>';}).join('');}function renderModalDraft(){const box=document.getElementById('modalDraftList');const empty=document.getElementById('modalDraftEmpty');if(!box)return;modalDraft=normalizeDraft(modalDraft);const groups=groupDraft(modalDraft);if(groups.length===0){box.innerHTML='';if(empty)empty.style.display='block';return;}if(empty)empty.style.display='none';box.innerHTML=groups.map(function(g,i){return '<div class="draft-item"><span>'+esc(formatDates(g.dates))+' ｜ '+esc(opts.userMap[g.staff_id]||g.staff_id)+' ｜ '+esc(opts.shiftMap[g.shift_code]||g.shift_code)+'</span><button type="button" class="secondary" onclick="removeModalDraftGroup('+i+')">移除</button></div>';}).join('');}function collectModalChange(){const dates=window.getSelectedDates?window.getSelectedDates():[];const shiftEl=document.getElementById('modalShift');const shift=shiftEl?shiftEl.value:'';const checked=[...document.querySelectorAll('#scheduleModal input[name="staff_ids"]:checked')].map(function(el){return el.value;});if(dates.length===0){alert('请先选择日期');return null;}if(checked.length===0){alert('请选择用户');return null;}return{dates:dates,staff_ids:checked,shift_code:shift};}window.addDraftRules=function(changes){draft=normalizeDraft(draft.concat(changes||[]));save();};window.upsertDraftChange=function(date,staffID,shiftCode){date=String(date||'').trim();staffID=String(staffID||'').trim();shiftCode=String(shiftCode||'').trim();if(!date||!staffID)return;draft=normalizeDraft(draft).filter(function(ch){return !(ch.dates[0]===date&&ch.staff_ids[0]===staffID);});if(shiftCode){draft.push({dates:[date],staff_ids:[staffID],shift_code:shiftCode});}save();};window.removeDraftChange=function(date,staffID){window.upsertDraftChange(date,staffID,'');};window.getCurrentDraft=function(){return normalizeDraft(draft);};window.addModalDraft=function(){const ch=collectModalChange();if(!ch)return;modalDraft=normalizeDraft(modalDraft.concat([ch]));renderModalDraft();};window.removeModalDraftGroup=function(i){const groups=groupDraft(modalDraft);const g=groups[i];if(!g)return;modalDraft=normalizeDraft(modalDraft).filter(function(ch){return !(ch.shift_code===g.shift_code && ch.staff_ids[0]===g.staff_id && g.dates.indexOf(ch.dates[0])>=0);});renderModalDraft();};window.commitModalDraft=function(){if(modalDraft.length===0){const ch=collectModalChange();if(!ch)return;modalDraft=normalizeDraft([ch]);}draft=normalizeDraft(draft.concat(modalDraft));modalDraft=[];save();renderModalDraft();document.querySelectorAll('#scheduleModal input[name="staff_ids"]').forEach(function(el){el.checked=false;});window.closeScheduleModal();};window.addDraftChange=function(){window.commitModalDraft();};window.removeDraftGroup=function(i){const groups=groupDraft(draft);const g=groups[i];if(!g)return;draft=normalizeDraft(draft).filter(function(ch){return !(ch.shift_code===g.shift_code && ch.staff_ids[0]===g.staff_id && g.dates.indexOf(ch.dates[0])>=0);});save();};window.clearDraft=function(){if(confirm('清空当前月份所有未提交草稿？')){draft=[];save();}};window.submitDraftApproval=function(){draft=normalizeDraft(draft);if(draft.length===0){alert('请先在右侧排班矩阵编辑草稿。矩阵下拉修改会自动加入草稿，无需再点击批量写入。');return;}document.getElementById('draftRulesInput').value=JSON.stringify(draft);try{localStorage.removeItem(key);}catch(e){}draft=[];renderDraft();document.getElementById('draftSubmitForm').submit();};renderDraft();renderModalDraft();}
function initSchedulePreviewEditor(opts){
const users=(opts.users||[]).filter(function(u){return (u.enabled!==false)&&(u.Enabled!==false);});
const shifts=(opts.shifts||[]).filter(function(s){return (s.enabled!==false)&&(s.Enabled!==false);});
const days=(opts.days||[]).filter(function(d){return d.is_current_month||d.IsCurrentMonth});
const status=opts.status||{};
let checkedUsers=new Set(users.map(function(u){return u.id||u.ID;}));
let selectedShift=shifts.length?(shifts[0].code||shifts[0].Code):'';
function shiftCode(sh){return sh.code||sh.Code}
function shiftName(sh){return sh.name||sh.Name||shiftCode(sh)}
function shiftShort(sh){return sh.short_name||sh.ShortName||shiftName(sh)}
function shiftStart(sh){return sh.start||sh.Start||''}
function shiftEnd(sh){return sh.end||sh.End||''}
function shiftTimezone(sh){return sh.timezone||sh.Timezone||''}
function shiftNotify(sh){if(sh.notify_enabled!==undefined&&sh.notify_enabled!==null)return !!sh.notify_enabled;if(sh.NotifyEnabled!==undefined&&sh.NotifyEnabled!==null)return !!sh.NotifyEnabled;return !(String(shiftName(sh)+shiftShort(sh)).match(/休|年|病/));}
function shiftInfo(code){return shifts.find(function(s){return shiftCode(s)===code})||{code:code,name:code,short_name:code,start:'',end:''};}
function isMorning(code){const sh=shiftInfo(code);return code==='morning'||String(shiftName(sh)+shiftShort(sh)).indexOf('早')>=0;}
function isNormal(code){const sh=shiftInfo(code);return code==='normal'||String(shiftName(sh)+shiftShort(sh)).indexOf('正常')>=0;}
function isAnnual(code){const sh=shiftInfo(code);return code==='annual_leave'||String(shiftName(sh)+shiftShort(sh)).indexOf('年')>=0;}
function restCode(){const sh=shifts.find(function(s){return shiftCode(s)==='rest'||String(shiftName(s)+shiftShort(s)).indexOf('休')>=0;});return sh?shiftCode(sh):'rest';}
function dayDate(d){return d.date||d.Date}
function dayNum(d){return d.day||d.Day}
function parseDay(d){const parts=String(dayDate(d)||'').split('-').map(Number);return new Date(Date.UTC(parts[0],(parts[1]||1)-1,parts[2]||1));}
function weekdayName(d){const t=parseDay(d);return ['周日','周一','周二','周三','周四','周五','周六'][t.getUTCDay()]||'';}
function isWeekend(d){return d.is_weekend||d.IsWeekend}
function holidayType(d){return d.holiday_type||d.HolidayType||''}
function isSat(d){return parseDay(d).getUTCDay()===6}
function isSun(d){return parseDay(d).getUTCDay()===0}
function currentDraft(){return window.getCurrentDraft?window.getCurrentDraft():[]}
function explicitMap(draft){const m={};(draft||[]).forEach(function(ch){(ch.dates||[]).forEach(function(d){(ch.staff_ids||[]).forEach(function(u){if(ch.shift_code)m[d+'|'+u]=ch.shift_code;});});});return m;}
function projectedDraft(draft){
 const explicit=explicitMap(draft);const out=(draft||[]).slice();const morningUsers={},normalUsers={},annualStarts={};
 Object.keys(explicit).forEach(function(k){const user=k.split('|')[1];const date=k.split('|')[0];const code=explicit[k];if(isMorning(code))morningUsers[user]=code;if(isNormal(code))normalUsers[user]=code;if(isAnnual(code)){if(!annualStarts[user])annualStarts[user]={};annualStarts[user][date]=code;}});
 function addMonthly(user,code,kind){days.forEach(function(d){const date=dayDate(d);const key=date+'|'+user;if(explicit[key])return;if(kind==='morning'){out.push({dates:[date],staff_ids:[user],shift_code:(isSun(d)||isSat(d))?restCode():code});}else if(kind==='normal'){out.push({dates:[date],staff_ids:[user],shift_code:isSun(d)?restCode():code});}});}
 Object.keys(morningUsers).forEach(function(u){addMonthly(u,morningUsers[u],'morning');});
 Object.keys(normalUsers).forEach(function(u){if(!morningUsers[u])addMonthly(u,normalUsers[u],'normal');});
 Object.keys(annualStarts).forEach(function(u){Object.keys(annualStarts[u]).sort().forEach(function(start){const idx=days.findIndex(function(d){return dayDate(d)===start;});if(idx<0)return;for(let i=0;i<15 && idx+i<days.length;i++){const date=dayDate(days[idx+i]);const key=date+'|'+u;if(!explicit[key])out.push({dates:[date],staff_ids:[u],shift_code:annualStarts[u][start]});}});});
 return out;
}
function statusMapWithDraft(draft){const by={};Object.keys(status).forEach(function(date){(status[date]||[]).forEach(function(r){by[date+'|'+r.staff_id]={code:r.shift_code,name:r.shift_name,short:r.shift_short_name||r.shift_name,draft:false};});});projectedDraft(draft||[]).forEach(function(ch){const info=shiftInfo(ch.shift_code);(ch.dates||[]).forEach(function(d){(ch.staff_ids||[]).forEach(function(u){by[d+'|'+u]={code:ch.shift_code,name:shiftName(info),short:shiftShort(info),draft:true};});});});return by;}
function renderCalendar(by){const cal=document.getElementById('scheduleCalendar');if(!cal)return;cal.querySelectorAll('.day').forEach(function(el){const date=el.dataset.date;const items=el.querySelector('.items');if(!items)return;let chips=[];users.forEach(function(u){const id=u.id||u.ID;const name=u.name||u.Name;const cell=by[date+'|'+id];if(cell)chips.push('<span class="chip">'+esc(name)+' '+esc(cell.short||cell.name||cell.code)+'</span>');});items.innerHTML=chips.join('');});}
function renderUserPicker(){const box=document.getElementById('previewUserPicker');if(!box)return;let html='<div class="picker-head"><b>选择用户</b><span class="hint">已选 '+checkedUsers.size+' / '+users.length+'</span></div><div class="user-picker-grid">';users.forEach(function(u){const id=u.id||u.ID;const name=u.name||u.Name;html+='<button type="button" class="user-tile '+(checkedUsers.has(id)?'selected':'')+'" data-user="'+esc(id)+'"><b>'+esc(name)+'</b></button>';});html+='</div>';box.innerHTML=html;box.querySelectorAll('.user-tile').forEach(function(btn){btn.addEventListener('click',function(){const id=btn.dataset.user;if(checkedUsers.has(id))checkedUsers.delete(id);else checkedUsers.add(id);render(currentDraft());});});}
function renderShiftPicker(){const box=document.getElementById('previewShiftPicker');if(!box)return;if(!selectedShift&&shifts.length)selectedShift=shiftCode(shifts[0]);let html='<div class="picker-head"><b>选择班次</b><span class="hint">单选</span></div><div class="shift-picker-grid">';shifts.forEach(function(sh){const code=shiftCode(sh);const name=shiftName(sh);const time=(shiftStart(sh)||'-')+' - '+(shiftEnd(sh)||'-');const tz=shiftTimezone(sh);const notify=shiftNotify(sh)?'启用通知':'无需通知';html+='<button type="button" class="shift-tile '+(selectedShift===code?'selected':'')+'" data-shift="'+esc(code)+'"><b>'+esc(name)+'</b><span class="time">'+esc(time)+'</span><span class="sub">'+esc(tz?tz+' ｜ '+notify:notify)+'</span></button>';});html+='</div>';box.innerHTML=html;box.querySelectorAll('.shift-tile').forEach(function(btn){btn.addEventListener('click',function(){selectedShift=btn.dataset.shift;render(currentDraft());});});}
function render(draft){const box=document.getElementById('monthPreviewMatrix');if(!box)return;const sel=window.getSelectedDates?window.getSelectedDates():[];const by=statusMapWithDraft(draft||[]);const selectedDates=new Set(sel);const note=document.getElementById('previewSelectedDates');if(note)note.textContent=sel.length?sel.join(', '):'尚未选择日期';renderUserPicker();renderShiftPicker();const viewUsers=users.filter(function(u){return checkedUsers.has(u.id||u.ID);});if(viewUsers.length===0){box.innerHTML='<div class="empty">请先在上方用户窗口选择一个或多个用户</div>';renderCalendar(by);return;}let html='<table class="month-matrix"><thead><tr><th class="user-col">用户</th>';days.forEach(function(d){html+='<th class="'+(isWeekend(d)?'weekend':'')+'"><span class="day-head"><b>'+dayNum(d)+'</b><small>'+weekdayName(d)+'</small></span></th>';});html+='</tr></thead><tbody>';viewUsers.forEach(function(u){const id=u.id||u.ID;const name=u.name||u.Name;html+='<tr><td class="user-col"><b>'+esc(name)+'</b></td>';days.forEach(function(d){const date=dayDate(d);const cell=by[date+'|'+id];let cls='matrix-cell';if(isWeekend(d))cls+=' weekend';if(holidayType(d)==='holiday')cls+=' holiday';if(cell)cls+=' has-shift '+esc(cell.code||'');if(selectedDates.has(date))cls+=' cell-selected';let select='<select class="matrix-shift-select '+esc(cell&&cell.code?cell.code:'')+' '+(cell&&cell.draft?'draft':'')+'" data-date="'+esc(date)+'" data-user="'+esc(id)+'" title="'+esc(date+' '+name)+'"><option value="">-</option>';shifts.forEach(function(sh){const code=shiftCode(sh);select+='<option value="'+esc(code)+'" '+(cell&&cell.code===code?'selected':'')+'>'+esc(shiftShort(sh))+'</option>';});select+='</select>';html+='<td class="'+cls+'" data-date="'+esc(date)+'" data-user="'+esc(id)+'">'+select+'</td>';});html+='</tr>';});html+='</tbody></table>';box.innerHTML=html;box.querySelectorAll('.matrix-shift-select').forEach(function(selEl){selEl.addEventListener('change',function(){if(window.upsertDraftChange)window.upsertDraftChange(selEl.dataset.date,selEl.dataset.user,selEl.value);});});renderCalendar(by);}
window.refreshSchedulePreviewMatrix=function(draft){render(draft||currentDraft());};
window.refreshScheduleCalendar=function(draft){renderCalendar(statusMapWithDraft(draft||currentDraft()));};
window.onScheduleDateSelectionChange=function(){render(currentDraft());};
window.selectAllPreviewUsers=function(){users.forEach(function(u){checkedUsers.add(u.id||u.ID);});render(currentDraft());};
window.clearPreviewUsers=function(){checkedUsers.clear();render(currentDraft());};
window.confirmPreviewDraft=function(){const dates=window.getSelectedDates?window.getSelectedDates():[];const selected=[...checkedUsers];const shift=selectedShift||'';const overwriteEl=document.getElementById('previewOverwriteDraft');const overwrite=overwriteEl?overwriteEl.checked:false;if(selected.length===0){alert('请先在用户窗口选择一个或多个用户');return;}if(!shift){alert('请选择班次');return;}const manual={};currentDraft().forEach(function(ch){(ch.dates||[]).forEach(function(d){(ch.staff_ids||[]).forEach(function(u){manual[d+'|'+u]=ch.shift_code;});});});const changes=[];function pushChange(d,u,code){if(!overwrite&&manual[d+'|'+u])return;changes.push({dates:[d],staff_ids:[u],shift_code:code});}
 selected.forEach(function(u){if(isMorning(shift)){days.forEach(function(d){pushChange(dayDate(d),u,(isSun(d)||isSat(d))?restCode():shift);});}else if(isNormal(shift)){days.forEach(function(d){pushChange(dayDate(d),u,isSun(d)?restCode():shift);});}else if(isAnnual(shift)){const baseDates=dates.length?dates:[dayDate(days[0])];baseDates.forEach(function(start){const idx=days.findIndex(function(d){return dayDate(d)===start;});if(idx<0)return;for(let i=0;i<15 && idx+i<days.length;i++)pushChange(dayDate(days[idx+i]),u,shift);});}else{if(dates.length===0){alert('请先在左侧日历选择一个或多个日期');return;}dates.forEach(function(d){pushChange(d,u,shift);});}});
 if(changes.length===0){alert('所选范围内都已有手动编辑草稿。需要覆盖时请勾选“覆盖已编辑草稿”。');return;}if(window.addDraftRules)window.addDraftRules(changes);};
render(currentDraft());
}
</script></head><body><div class="shell"><aside class="side"><div class="brand"><div class="logo"></div><div><b>devops-worker</b><div class="hint">schedule</div></div></div><nav class="nav"><a href="/">首页</a><a href="/schedule">排班设置</a><a href="/shifts">班次设置</a><a href="/users">用户</a><a href="/approvals">审批</a><a href="/history">历史</a>{{if .IsAdmin}}<a href="/sso-settings">SSO配置</a>{{end}}</nav></aside><main class="content"><div class="topline"><div><div class="kicker">devops-worker</div><h1>{{.Title}}</h1></div><div class="version-box"><span class="tag">{{.NowDate}}</span><label class="tag tz-picker">时区<select onchange="changeDisplayTimezone(this.value)">{{range .TimezoneOptions}}<option value="{{.Name}}" {{if eq .Name $.Config.Timezone}}selected{{end}}>{{.Name}}</option>{{end}}</select></label><span class="tag user-tag {{if .IsAdmin}}role-admin{{else}}role-user{{end}}"><span class="user-avatar">{{userInitial .CurrentUserName}}</span><span class="user-text"><b>{{if .CurrentUserName}}{{.CurrentUserName}}{{else}}{{if .IsAdmin}}admin{{else}}普通用户{{end}}{{end}}</b><small>{{if .IsAdmin}}超级管理员{{else}}普通用户{{end}}{{if .CurrentUserEmail}} · {{.CurrentUserEmail}}{{end}}</small></span></span>{{if or .IsAdmin .IsAuthenticated}}<a class="tag" href="/logout">退出</a>{{else}}<a class="tag" href="/login">管理员登录</a>{{end}}</div></div>{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}{{if .Error}}<div class="err">{{.Error}}</div>{{end}}{{end}}
{{define "layout_end"}}</main></div></body></html>{{end}}`

const dayCell = `<div class="daytop"><div class="daynum">{{.Day}}</div>{{if .HolidayName}}<div class="holiday-label">{{.HolidayName}}</div>{{end}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div>`

const dashboardTemplate = `{{define "dashboard"}}{{template "layout_start" .}}<div class="workbench"><section class="panel-card calendar-card"><div class="calendar-head"><a class="btn secondary" href="/?year={{.CalendarPrevYear}}&month={{.CalendarPrevMonth}}&date={{.SelectedDate}}">上月</a><div><div class="calendar-title">{{.CalendarYear}} 年 {{.CalendarMonth}} 月</div><div class="calendar-sub">点击日期查看右侧详情，不刷新页面</div></div><a class="btn secondary" href="/?year={{.CalendarNextYear}}&month={{.CalendarNextMonth}}&date={{.SelectedDate}}">下月</a></div><div class="calendar" id="mainCalendar"><div class="weekday">周一</div><div class="weekday">周二</div><div class="weekday">周三</div><div class="weekday">周四</div><div class="weekday">周五</div><div class="weekday">周六</div><div class="weekday">周日</div>{{range .CalendarDays}}<button type="button" class="day {{if not .IsCurrentMonth}}dim{{end}} {{if .IsWeekend}}weekend{{end}} {{if eq .HolidayType "holiday"}}holiday{{end}} {{if eq .HolidayType "work"}}workday{{end}} {{if .IsToday}}today{{end}} {{if .IsSelected}}selected{{end}}" data-date="{{.Date}}"><div class="daytop"><div class="daynum">{{.Day}}</div>{{if .HolidayName}}<div class="holiday-label">{{.HolidayName}}</div>{{end}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div></button>{{end}}</div></section><aside class="panel-card detail-card"><details class="meta-fold"><summary>版本信息 / 审批 / 记录数</summary><div class="meta-grid"><div class="meta"><div class="label">版本</div><b>{{versionLabel .Active}}</b></div><div class="meta"><div class="label">审批人</div><b>{{tgName .Active.ApprovedBy .Users}}</b></div><div class="meta"><div class="label">生效时间</div><b>{{if .Active.EffectiveAt}}{{shortTimeTZ .Active.EffectiveAt .Config.Timezone}}{{else}}-{{end}}</b></div><div class="meta"><div class="label">记录数</div><b>{{len .Active.Items}}</b></div></div></details><h2 style="margin:4px 0 8px;font-size:16px"><span data-current-date>{{.SelectedDate}}</span> 排班明细</h2><div class="detail-scroll"><div class="list" id="dayDetail"></div><p class="hint">已读状态来自值班人员点击 Telegram 提醒中的“我已读”。</p></div></aside></div><script>bindCalendar({calendarId:'mainCalendar',detailId:'dayDetail',status:{{json .DayStatus}},selected:'{{.SelectedDate}}',multi:false,emptyText:'当天没有排班'});</script>{{template "layout_end" .}}{{end}}`

const usersTemplate = `{{define "users"}}{{template "layout_start" .}}<div class="card"><div class="row" style="justify-content:space-between;align-items:flex-start"><div><h2>新增用户</h2><p class="hint">普通用户可以新增用户，并修改自己新增的用户；既有/admin 用户只能查看。SSO 登录用户会按完整字符串严格匹配 sub / email / preferred_username / name，大小写自动兼容，不做关键字、包含或邮箱前缀模糊匹配。</p></div>{{if .IsAdmin}}<a class="btn secondary" href="/users/export">一键导出 CSV</a>{{end}}</div><form method="post" action="/users/create" class="row"><input name="name" placeholder="用户名/别名" required><input name="email" placeholder="邮箱/SSO 邮箱，可为空"><input name="phone" placeholder="电话号码，可为空"><input name="telegram_user_id" placeholder="Telegram User ID，可为空"><button type="submit">新增用户</button></form></div>{{if .IsAdmin}}<div class="card" style="margin-top:10px"><h2>批量导入用户</h2><form method="post" action="/users/import" enctype="multipart/form-data" class="grid" style="grid-template-columns:minmax(260px,360px) 1fr auto;align-items:end"><div><label class="hint">CSV 文件</label><input type="file" name="file" accept=".csv,text/csv" style="width:100%"></div><div><label class="hint">或粘贴 CSV 内容</label><textarea name="csv_text" placeholder="id,name,email,phone,telegram_user_id,enabled,created_by,sso_sub,sso_username,sso_email" style="width:100%;min-height:46px;border:1px solid #dbe4ef;border-radius:10px;padding:8px"></textarea></div><button type="submit">批量导入</button></form><p class="hint">导入采用完整字符串匹配：优先 ID，其次 SSO Sub、邮箱、SSO 邮箱、SSO 用户名、用户名；大小写不敏感。不会使用关键字、包含、邮箱前缀等模糊匹配，避免相似账号被错误合并。不会删除 CSV 中缺失的现有用户。</p></div>{{end}}<div class="card" style="overflow:auto;max-height:calc(100vh - 260px);margin-top:10px"><table><thead><tr><th>用户名</th><th>邮箱</th><th>电话</th><th>Telegram User ID</th><th>SSO关联</th><th>启用</th><th>来源</th><th>操作</th></tr></thead><tbody>{{range .Users}}{{$can := canEditUser . $.IsAdmin}}<tr>{{if $can}}<form method="post" action="/users/update"><input type="hidden" name="id" value="{{.ID}}"><td><input name="name" value="{{.Name}}" required></td><td><input name="email" value="{{.Email}}" placeholder="邮箱"></td><td><input name="phone" value="{{.Phone}}" placeholder="电话号码"></td><td><input name="telegram_user_id" value="{{.TelegramUserID}}"></td><td>{{if .SSOSub}}<span class="tag ok">{{if .SSOUsername}}{{.SSOUsername}}{{else}}SSO{{end}}</span>{{else}}<span class="hint">未关联</span>{{end}}</td><td><select name="enabled"><option value="true" {{if .Enabled}}selected{{end}}>启用</option><option value="false" {{if not .Enabled}}selected{{end}}>禁用</option></select></td><td><span class="tag">{{if .CreatedBy}}{{.CreatedBy}}{{else}}admin{{end}}</span></td><td><button type="submit">保存</button></form>{{if $.IsAdmin}}<form method="post" action="/users/delete" style="display:inline"><input type="hidden" name="id" value="{{.ID}}"><button class="danger" type="submit">{{if .Enabled}}禁用并清理{{else}}彻底删除{{end}}</button></form>{{end}}</td>{{else}}<td><b>{{.Name}}</b></td><td>{{if .Email}}{{.Email}}{{else}}-{{end}}</td><td>{{if .Phone}}{{.Phone}}{{else}}-{{end}}</td><td>{{.TelegramUserID}}</td><td>{{if .SSOSub}}<span class="tag ok">{{if .SSOUsername}}{{.SSOUsername}}{{else}}SSO{{end}}</span>{{else}}<span class="hint">未关联</span>{{end}}</td><td>{{if .Enabled}}启用{{else}}禁用{{end}}</td><td><span class="tag">{{if .CreatedBy}}{{.CreatedBy}}{{else}}admin{{end}}</span></td><td><span class="hint">只读</span></td>{{end}}</tr>{{else}}<tr><td colspan="8">暂无用户</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const shiftsTemplate = `{{define "shifts"}}{{template "layout_start" .}}<div class="card"><h2>新增班次</h2><form method="post" action="/shifts/create" class="row shift-form"><span class="tag">编码自动生成</span><input name="name" placeholder="名称，如 客服班" required><input name="short_name" placeholder="简称，如 客" required><label class="field-mini">开始<select name="start" required>{{range .TimeOptions}}<option value="{{.}}" {{if eq . "09:00"}}selected{{end}}>{{.}}</option>{{end}}</select></label><label class="field-mini">结束<select name="end" required>{{range .TimeOptions}}<option value="{{.}}" {{if eq . "18:00"}}selected{{end}}>{{.}}</option>{{end}}</select></label><label class="field-mini">时区<select name="timezone" required>{{range .TimezoneOptions}}<option value="{{.Name}}" {{if eq .Name "Asia/Dubai"}}selected{{end}}>{{.Label}}</option>{{end}}</select></label><label class="field-mini">通知<select name="notify_enabled"><option value="true" selected>启用通知</option><option value="false">无需通知</option></select></label><button type="submit">新增班次</button></form><p class="hint">编码由系统自动生成，避免手动重复；是否跨天自动根据开始/结束时间判断；可为自定义班次关闭通知。休息、年假、病假默认无需通知。普通用户可新增并修改自己新增的班次；默认/admin 班次只读。admin 第一次删除会先禁用并异步清理未来未通知排班，禁用后再次删除才会彻底移除配置。</p></div><div class="card" style="overflow:auto;max-height:calc(100vh - 215px);margin-top:10px"><table><thead><tr><th>编码</th><th>名称</th><th>简称</th><th>开始</th><th>结束</th><th>时区</th><th>通知</th><th>跨天</th><th>状态</th><th>来源</th><th>操作</th></tr></thead><tbody>{{range .Shifts}}{{$shift := .}}{{$can := canEditShift . $.IsAdmin}}<tr>{{if $can}}<form method="post" action="/shifts/update"><input type="hidden" name="code" value="{{$shift.Code}}"><td><code>{{$shift.Code}}</code></td><td><input name="name" value="{{$shift.Name}}" required></td><td><input name="short_name" value="{{$shift.ShortName}}" required></td><td><select name="start" required>{{range $.TimeOptions}}<option value="{{.}}" {{if eq . $shift.Start}}selected{{end}}>{{.}}</option>{{end}}</select></td><td><select name="end" required>{{range $.TimeOptions}}<option value="{{.}}" {{if eq . $shift.End}}selected{{end}}>{{.}}</option>{{end}}</select></td><td><select name="timezone" required>{{range $.TimezoneOptions}}<option value="{{.Name}}" {{if eq .Name $shift.Timezone}}selected{{end}}>{{.Label}}</option>{{end}}</select></td><td><select name="notify_enabled"><option value="true" {{if shiftNotify $shift}}selected{{end}}>启用</option><option value="false" {{if not (shiftNotify $shift)}}selected{{end}}>无需通知</option></select></td><td>{{if $shift.CrossDay}}<span class="pill ok">自动跨天</span>{{else}}<span class="pill off">当天</span>{{end}}</td><td><select name="enabled"><option value="true" {{if $shift.Enabled}}selected{{end}}>启用</option><option value="false" {{if not $shift.Enabled}}selected{{end}}>禁用</option></select></td><td><span class="tag">{{if $shift.CreatedBy}}{{$shift.CreatedBy}}{{else}}system{{end}}</span></td><td><button type="submit">保存</button></form>{{if $.IsAdmin}}<form method="post" action="/shifts/delete" style="display:inline"><input type="hidden" name="code" value="{{$shift.Code}}"><button class="danger" type="submit">{{if $shift.Enabled}}禁用并清理{{else}}彻底删除{{end}}</button></form>{{end}}</td>{{else}}<td><code>{{$shift.Code}}</code></td><td>{{$shift.Name}}</td><td>{{$shift.ShortName}}</td><td>{{$shift.Start}}</td><td>{{$shift.End}}</td><td>{{$shift.Timezone}}</td><td>{{if shiftNotify $shift}}启用通知{{else}}无需通知{{end}}</td><td>{{if $shift.CrossDay}}自动跨天{{else}}当天{{end}}</td><td>{{if $shift.Enabled}}启用{{else}}禁用{{end}}</td><td><span class="tag">{{if $shift.CreatedBy}}{{$shift.CreatedBy}}{{else}}system{{end}}</span></td><td><span class="hint">只读</span></td>{{end}}</tr>{{else}}<tr><td colspan="11">暂无班次</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const scheduleTemplate = `{{define "schedule"}}{{template "layout_start" .}}<div class="workbench schedule-workbench"><section class="panel-card calendar-card"><div class="calendar-head"><a class="btn secondary" href="/schedule?year={{.CalendarPrevYear}}&month={{.CalendarPrevMonth}}&date={{.SelectedDate}}">上月</a><div><div class="calendar-title">{{.CalendarYear}} 年 {{.CalendarMonth}} 月排班设置</div><div class="calendar-sub">左侧选日期，右侧选择用户/班次并在矩阵中直接编辑</div></div><a class="btn secondary" href="/schedule?year={{.CalendarNextYear}}&month={{.CalendarNextMonth}}&date={{.SelectedDate}}">下月</a></div><div class="calendar" id="scheduleCalendar"><div class="weekday">周一</div><div class="weekday">周二</div><div class="weekday">周三</div><div class="weekday">周四</div><div class="weekday">周五</div><div class="weekday">周六</div><div class="weekday">周日</div>{{range .CalendarDays}}<button type="button" class="day {{if not .IsCurrentMonth}}dim{{end}} {{if .IsWeekend}}weekend{{end}} {{if eq .HolidayType "holiday"}}holiday{{end}} {{if eq .HolidayType "work"}}workday{{end}} {{if .IsToday}}today{{end}}" data-date="{{.Date}}" {{if not .IsCurrentMonth}}disabled{{end}}><div class="daytop"><div class="daynum">{{.Day}}</div>{{if .HolidayName}}<div class="holiday-label">{{.HolidayName}}</div>{{end}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div></button>{{end}}</div><div class="row" style="margin-top:7px"><button type="button" class="btn secondary" onclick="clearSelectedDates()">清空选择</button><span class="hint" id="selectedHint">尚未选择日期</span></div><div class="draft-box"><div class="row" style="justify-content:space-between"><b>本月待提交草稿</b><div class="row"><button type="button" class="secondary" onclick="clearDraft()">清空草稿</button><button type="button" id="submitDraftBtn" onclick="submitDraftApproval()">提交草稿审批</button></div></div><p class="hint" id="draftEmpty">暂无草稿。直接在右侧矩阵单元格下拉修改即可实时加入草稿。</p><div class="draft-list" id="draftList"></div><form id="draftSubmitForm" method="post" action="/schedule/submit"><input type="hidden" name="year" value="{{.CalendarYear}}"><input type="hidden" name="month" value="{{.CalendarMonth}}"><input type="hidden" name="created_by" value="{{.Role}}"><input type="hidden" name="draft_rules" id="draftRulesInput"></form></div></section><aside class="panel-card detail-card"><details class="schedule-meta-mini"><summary><span>版本 / 记录信息</span></summary><div class="meta-grid"><div class="meta"><div class="label">当前版本</div><b>{{versionLabel .Active}}</b></div><div class="meta"><div class="label">排班记录</div><b>{{len .Active.Items}}</b></div></div></details><div class="schedule-heading-row"><h2>本月排班预览 / 直接编辑</h2><span class="hint">选择后实时进入草稿</span></div><div class="month-tools"><div class="picker-row"><div id="previewUserPicker" class="user-picker-box"></div><div id="previewShiftPicker" class="shift-picker-box"></div></div><div class="selected-note">已选日期：<span id="previewSelectedDates">尚未选择日期</span></div><div class="toolbar"><label class="inline-check"><input type="checkbox" id="previewOverwriteDraft"> 覆盖已编辑草稿</label><button type="button" class="secondary" onclick="selectAllPreviewUsers()">全选用户</button><button type="button" class="secondary" onclick="clearPreviewUsers()">清空用户</button><button type="button" onclick="confirmPreviewDraft()">应用到预览</button></div><div class="hint">班次默认单选。早班：工作日早班、周六周日休息；正常班：周一至周六正常班、周日休息；年假：起始日起连续 15 天。下方单元格可逐日微调，手动设置优先。</div></div><div class="month-matrix-wrap" id="monthPreviewMatrix"></div></aside></div><script>bindCalendar({calendarId:'scheduleCalendar',hintId:'selectedHint',status:{{json .DayStatus}},selected:'{{.SelectedDate}}',multi:true,emptyText:'当天没有排班'});initScheduleDraft({year:{{.CalendarYear}},month:{{.CalendarMonth}},userMap:{ {{range .Users}}'{{.ID}}':'{{.Name}}',{{end}} },shiftMap:{ {{range .Shifts}}'{{.Code}}':'{{.Name}}',{{end}} }});initSchedulePreviewEditor({users:{{json .Users}},shifts:{{json .Shifts}},days:{{json .CalendarDays}},status:{{json .DayStatus}}});</script>{{template "layout_end" .}}{{end}}`

const approvalsTemplate = `{{define "approvals"}}{{template "layout_start" .}}<div class="card" style="overflow:auto;max-height:calc(100vh - 96px)"><div class="row" style="justify-content:space-between;align-items:flex-start"><div><h2>审批记录</h2><p class="hint">Telegram 审批仍然可用；Web UI 审批仅允许 admin 超级管理员操作，普通用户只能查看。</p></div>{{if .IsAdmin}}<span class="pill ok">admin 可审批</span>{{else}}<span class="pill off">普通用户只读</span>{{end}}</div><table><thead><tr><th>审批</th><th>状态</th><th>事务</th><th>创建</th><th>审批人/处理人</th><th>结果</th><th>预览</th><th>操作</th></tr></thead><tbody>{{range .Approvals}}<tr><td><span class="tag">{{compact .ID}}</span></td><td><span class="badge">{{approvalStatus .Status}}</span></td><td><span class="tag">{{compact .TransactionID}}</span></td><td>{{.CreatedBy}}<div class="hint">{{shortTimeTZ .CreatedAt $.Config.Timezone}}</div></td><td>{{approvalReviewer . $.Users}}</td><td>{{if .StatusMessage}}<span class="hint">{{.StatusMessage}}</span>{{else}}-{{end}}</td><td><a class="btn secondary" href="/{{.PreviewHTML}}" target="_blank">HTML</a></td><td>{{if and $.IsAdmin (eq .Status "pending")}}<form method="post" action="/approvals/approve" style="display:inline" onsubmit="return confirm('确认通过该审批并立即生效？')"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">通过生效</button></form><form method="post" action="/approvals/reject" style="display:inline;margin-left:6px" onsubmit="return confirm('确认拒绝该审批？')"><input type="hidden" name="id" value="{{.ID}}"><button class="danger" type="submit">拒绝</button></form>{{else}}<span class="hint">{{if eq .Status "pending"}}等待审批{{else}}已结束{{end}}</span>{{end}}</td></tr>{{else}}<tr><td colspan="8">暂无审批记录</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const settingsTemplate = `{{define "sso_settings"}}{{template "layout_start" .}}<div class="card" style="overflow:auto;max-height:calc(100vh - 96px)"><div class="row" style="justify-content:space-between;align-items:flex-start"><div><h2>SSO 配置</h2><p class="hint">SSO 默认禁用。超级管理员可在这里配置 Keycloak / OIDC 并启用或禁用；保存后会立即对后续登录生效，不需要重启服务。</p></div>{{if .SSOSettings.Enabled}}<span class="pill ok">SSO 已启用</span>{{else}}<span class="pill off">SSO 已禁用</span>{{end}}</div><form method="post" action="/sso-settings/update" class="grid" style="grid-template-columns:repeat(auto-fit,minmax(280px,1fr));align-items:start"><section class="panel-card"><h3 style="margin:0 0 8px;font-size:15px">Keycloak / OIDC</h3><label class="field-mini"><input type="checkbox" name="enabled" value="true" {{if .SSOSettings.Enabled}}checked{{end}}> 启用 SSO</label><div><label class="hint">Issuer URL</label><input name="issuer_url" value="{{.SSOSettings.IssuerURL}}" placeholder="https://keycloak.example.com/realms/devops" style="width:100%"></div><div><label class="hint">Client ID</label><input name="client_id" value="{{.SSOSettings.ClientID}}" placeholder="devops-worker" style="width:100%"></div><div><label class="hint">Client Secret</label><input name="client_secret" type="password" placeholder="留空则保留当前密钥" style="width:100%"><input type="hidden" name="keep_client_secret" value="true"></div><div><label class="hint">Redirect URL</label><input name="redirect_url" value="{{.SSOSettings.RedirectURL}}" placeholder="https://dwork.abc.om/sso/callback" style="width:100%"></div><div><label class="hint">Scopes</label><input name="scopes" value="{{.SSOSettings.Scopes}}" placeholder="openid profile email" style="width:100%"></div></section><section class="panel-card"><h3 style="margin:0 0 8px;font-size:15px">角色权限映射</h3><div><label class="hint">超级管理员用户（sub / username / email / name，逗号分隔）</label><textarea name="admin_users" style="width:100%;min-height:76px;border:1px solid #dbe4ef;border-radius:10px;padding:8px">{{join .SSOSettings.AdminUsers}}</textarea></div><div><label class="hint">超级管理员角色（逗号分隔）</label><input name="admin_roles" value="{{join .SSOSettings.AdminRoles}}" placeholder="devops-worker-admin,admin" style="width:100%"></div><div><label class="hint">普通用户角色（逗号分隔）</label><input name="user_roles" value="{{join .SSOSettings.UserRoles}}" placeholder="devops-worker-user,user" style="width:100%"></div><div class="empty-detail"><b>权限说明</b><p class="hint">普通用户：查看首页、排班设置、历史、审批记录；无删除权限，不能修改 admin/system 创建的用户或班次。超级管理员：admin 权限，可审批生效、修改 SSO、删除/禁用用户和班次。</p><p class="hint">保存配置后立即生效。启用 SSO 后，登录页会显示 Keycloak SSO 登录入口；禁用后隐藏。</p></div><div class="row" style="margin-top:10px"><button type="submit">保存并立即生效</button><a class="btn secondary" href="/login">测试登录入口</a></div></section></form></div>{{template "layout_end" .}}{{end}}`

const historyTemplate = `{{define "history"}}{{template "layout_start" .}}<div class="workbench"><section class="panel-card calendar-card"><div class="calendar-head"><a class="btn secondary" href="/history?year={{.CalendarPrevYear}}&month={{.CalendarPrevMonth}}&date={{.SelectedDate}}">上月</a><div><div class="calendar-title">{{.CalendarYear}} 年 {{.CalendarMonth}} 月历史</div><div class="calendar-sub">点击日期查看右侧历史排班</div></div><a class="btn secondary" href="/history?year={{.CalendarNextYear}}&month={{.CalendarNextMonth}}&date={{.SelectedDate}}">下月</a></div><div class="calendar" id="historyCalendar"><div class="weekday">周一</div><div class="weekday">周二</div><div class="weekday">周三</div><div class="weekday">周四</div><div class="weekday">周五</div><div class="weekday">周六</div><div class="weekday">周日</div>{{range .CalendarDays}}<button type="button" class="day {{if not .IsCurrentMonth}}dim{{end}} {{if .IsWeekend}}weekend{{end}} {{if eq .HolidayType "holiday"}}holiday{{end}} {{if eq .HolidayType "work"}}workday{{end}} {{if .IsToday}}today{{end}} {{if .IsSelected}}selected{{end}}" data-date="{{.Date}}"><div class="daytop"><div class="daynum">{{.Day}}</div>{{if .HolidayName}}<div class="holiday-label">{{.HolidayName}}</div>{{end}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div></button>{{end}}</div></section><aside class="panel-card detail-card"><h2 style="margin:4px 0 8px;font-size:16px"><span data-current-date>{{.SelectedDate}}</span> 历史排班</h2><form method="get" action="/history" class="row" style="margin-bottom:8px"><input type="date" name="date" value="{{.HistoryDate}}"><button type="submit">跳转</button></form><div class="detail-scroll"><div class="list" id="historyDayDetail"></div></div></aside></div><script>bindCalendar({calendarId:'historyCalendar',detailId:'historyDayDetail',status:{{json .DayStatus}},selected:'{{.SelectedDate}}',multi:false,emptyText:'当天没有历史排班'});</script>{{template "layout_end" .}}{{end}}`
