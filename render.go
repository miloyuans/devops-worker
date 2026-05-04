package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"time"
)

func RenderPreviewHTML(approvalID string, items []ScheduleItem, activeRevision int, newRevision int) (string, error) {
	data := struct {
		ApprovalID     string
		GeneratedAt    string
		ActiveRevision int
		NewRevision    int
		Items          []ScheduleItem
	}{approvalID, time.Now().Format("2006-01-02 15:04:05"), activeRevision, newRevision, items}
	tpl := template.Must(template.New("preview").Funcs(template.FuncMap{"clock": formatClock}).Parse(previewTemplate))
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderPage(wr *bytes.Buffer, name string, data PageData) error {
	funcs := template.FuncMap{
		"clock":          templateClock,
		"dateOnly":       templateDateOnly,
		"json":           templateJSON,
		"versionLabel":   templateVersionLabel,
		"compact":        compactID,
		"tgName":         templateTGName,
		"tgNames":        templateTGNames,
		"approvalStatus": templateApprovalStatus,
		"shortTime":      templateShortTime,
	}
	tpl := template.Must(template.New("base").Funcs(funcs).Parse(baseTemplate + dashboardTemplate + usersTemplate + shiftsTemplate + scheduleTemplate + approvalsTemplate + historyTemplate))
	return tpl.ExecuteTemplate(wr, name, data)
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
<html lang="zh-CN"><head><meta charset="utf-8"><title>排班审批预览</title>
<style>
:root{--bg:#f5f8fb;--card:#fff;--line:#dbe4ef;--text:#0f172a;--muted:#64748b;--accent:#2563eb;--ok:#16a34a}*{box-sizing:border-box}body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Noto Sans SC",sans-serif;background:linear-gradient(135deg,#eef6ff,#f8fafc);color:var(--text);margin:0;padding:24px}.card{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:22px;box-shadow:0 14px 45px rgba(15,23,42,.08);max-width:1120px;margin:auto}.kicker{color:var(--accent);letter-spacing:.12em;font-size:12px;text-transform:uppercase}.muted{color:var(--muted)}.summary{display:grid;grid-template-columns:repeat(3,minmax(120px,1fr));gap:10px;margin:16px 0}.box{background:#f8fafc;border:1px solid var(--line);border-radius:14px;padding:12px}.box b{display:block;font-size:22px;margin-top:4px}table{width:100%;border-collapse:collapse;margin-top:14px;background:#fff;border-radius:14px;overflow:hidden}th,td{border-bottom:1px solid #e5edf6;padding:9px;text-align:left;font-size:13px}th{color:#334155;background:#f1f5f9}.badge{display:inline-block;padding:3px 8px;border-radius:999px;background:#e0f2fe;color:#075985}</style>
</head><body><div class="card"><div class="kicker">devops-worker approval preview</div><h1>排班策略生效预览</h1><p class="muted">审批ID：{{.ApprovalID}} ｜ 生成时间：{{.GeneratedAt}}</p><div class="summary"><div class="box">当前版本<b>{{.ActiveRevision}}</b></div><div class="box">审批后版本<b>{{.NewRevision}}</b></div><div class="box">排班记录<b>{{len .Items}}</b></div></div><table><thead><tr><th>日期</th><th>员工</th><th>班次</th><th>开始</th><th>结束</th><th>Telegram ID</th></tr></thead><tbody>{{range .Items}}<tr><td>{{.Date}}</td><td>{{.StaffName}}</td><td><span class="badge">{{.ShiftName}}</span></td><td>{{clock .StartTime}}</td><td>{{clock .EndTime}}</td><td>{{.TelegramUserID}}</td></tr>{{else}}<tr><td colspan="6">没有生成任何排班记录</td></tr>{{end}}</tbody></table></div></body></html>`

const baseTemplate = `{{define "layout_start"}}<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} - devops-worker</title><style>
:root{--bg:#eef3f8;--panel:#0f172a;--panel2:#111827;--card:#ffffff;--card2:#f8fafc;--line:#d9e3ef;--text:#0f172a;--muted:#64748b;--accent:#2563eb;--accent2:#38bdf8;--green:#d9fbe5;--green-line:#86efac;--green-text:#166534;--weekend:#f1f5f9;--gray:#94a3b8;--bad:#ef4444;--ok:#16a34a}*{box-sizing:border-box}body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Noto Sans SC",sans-serif;background:linear-gradient(135deg,#ecf5ff,#f8fafc 45%,#eef2f7);margin:0;color:var(--text);min-height:100vh;overflow:hidden}a{color:#2563eb;text-decoration:none}.shell{display:grid;grid-template-columns:168px 1fr;height:100vh}.side{background:linear-gradient(180deg,#0f172a,#111827);color:#e5edf7;border-right:1px solid rgba(255,255,255,.08);padding:14px 10px}.brand{display:flex;align-items:center;gap:8px;margin:4px 6px 16px}.logo{width:28px;height:28px;border-radius:9px;background:linear-gradient(135deg,#38bdf8,#2563eb);box-shadow:0 0 22px rgba(56,189,248,.28)}.brand b{font-size:14px}.brand .hint{font-size:10px;color:#94a3b8}.nav a{display:flex;align-items:center;gap:8px;padding:9px 10px;margin:4px 0;border-radius:12px;color:#cbd5e1;font-size:13px}.nav a:hover{background:rgba(148,163,184,.14);color:#fff}.content{height:100vh;overflow:hidden;padding:14px 16px}.topline{display:flex;align-items:center;justify-content:space-between;margin-bottom:10px}.kicker{color:var(--accent);letter-spacing:.12em;font-size:10px;text-transform:uppercase}.topline h1{margin:2px 0 0;font-size:20px}.version-box{display:flex;gap:8px;align-items:center;flex-wrap:wrap;justify-content:flex-end}.tag{display:inline-flex;align-items:center;border:1px solid var(--line);background:rgba(255,255,255,.72);border-radius:999px;padding:5px 9px;color:#334155;font-size:12px}.card{background:rgba(255,255,255,.9);border:1px solid var(--line);border-radius:18px;padding:14px;box-shadow:0 14px 40px rgba(15,23,42,.08)}.panel-card{background:rgba(255,255,255,.94);border:1px solid var(--line);border-radius:18px;padding:12px;box-shadow:0 14px 42px rgba(15,23,42,.07);min-height:0}.workbench{display:grid;grid-template-columns:minmax(640px,58%) minmax(360px,42%);gap:12px;height:calc(100vh - 74px);min-height:0}.calendar-card{display:flex;flex-direction:column;min-height:0}.detail-card{display:flex;flex-direction:column;min-height:0;overflow:hidden}.detail-scroll{overflow:auto;padding-right:4px}.calendar-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:8px;gap:8px}.calendar-title{font-size:17px;font-weight:800}.calendar-sub{font-size:12px;color:var(--muted);margin-top:2px}.calendar{display:grid;grid-template-columns:repeat(7,1fr);gap:6px;flex:1;min-height:0}.weekday{text-align:center;color:#64748b;font-size:11px;padding:4px}.day{border:1px solid var(--line);border-radius:13px;background:#fff;padding:7px;position:relative;overflow:hidden;transition:.12s;display:block;text-align:left;color:var(--text);width:100%;font:inherit;min-height:0}.day:hover{border-color:#93c5fd;box-shadow:0 8px 22px rgba(37,99,235,.09)}.day.weekend{background:var(--weekend)}.day.dim{opacity:.38}.day.today{background:var(--green);border-color:var(--green-line)}.day.selected{background:var(--green);border-color:#22c55e;box-shadow:0 0 0 2px rgba(34,197,94,.12)}.daynum{font-weight:800;font-size:13px}.day .items{margin-top:5px;display:flex;gap:4px;flex-wrap:wrap}.chip{font-size:10px;line-height:1;padding:3px 5px;border-radius:999px;background:#dbeafe;color:#1e40af;border:1px solid #bfdbfe;max-width:100%;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.muted,.hint{color:var(--muted);font-size:12px}.msg{padding:9px 11px;border-radius:12px;background:#dcfce7;border:1px solid #86efac;color:#166534;margin-bottom:10px}.err{padding:9px 11px;border-radius:12px;background:#fee2e2;border:1px solid #fca5a5;color:#991b1b;margin-bottom:10px}table{width:100%;border-collapse:collapse;background:#fff;border-radius:14px;overflow:hidden}th,td{border-bottom:1px solid #e5edf6;padding:9px;text-align:left;vertical-align:middle;font-size:13px}th{color:#475569;background:#f8fafc;font-weight:700}.btn,button{display:inline-flex;align-items:center;justify-content:center;gap:6px;border:0;border-radius:11px;background:linear-gradient(135deg,#2563eb,#38bdf8);color:#fff;padding:8px 12px;cursor:pointer;font-weight:700;font-size:13px}.btn.secondary{background:#eef2f7;border:1px solid #dbe4ef;color:#334155}.btn.danger,button.danger{background:linear-gradient(135deg,#dc2626,#f97316)}input,select{padding:8px 9px;border:1px solid #dbe4ef;border-radius:11px;margin:3px;background:#fff;color:var(--text);outline:none}input:focus,select:focus{border-color:#93c5fd;box-shadow:0 0 0 3px rgba(37,99,235,.1)}.row{display:flex;gap:8px;flex-wrap:wrap;align-items:center}.field-mini{display:inline-flex;align-items:center;gap:6px;color:#64748b;font-size:12px}.shift-form select{min-width:96px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:10px}.badge{display:inline-flex;align-items:center;gap:5px;padding:4px 8px;border-radius:999px;border:1px solid #bae6fd;background:#e0f2fe;color:#075985;font-size:12px}.pill{display:inline-flex;align-items:center;gap:5px;padding:4px 8px;border-radius:999px;font-size:12px;border:1px solid #e2e8f0;background:#f1f5f9;color:#64748b}.pill.ok{background:#dcfce7;border-color:#86efac;color:#166534}.pill.off{background:#f1f5f9;border-color:#e2e8f0;color:#64748b}.meta-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-bottom:10px}.meta{background:#f8fafc;border:1px solid #e2e8f0;border-radius:13px;padding:9px}.meta .label{font-size:11px;color:#64748b}.meta b{display:block;margin-top:3px;font-size:13px}.list{display:grid;gap:8px}.item-card{border:1px solid #e2e8f0;background:#fff;border-radius:14px;padding:10px}.item-head{display:flex;justify-content:space-between;gap:8px;align-items:center}.modal-backdrop{display:none;position:fixed;inset:0;background:rgba(15,23,42,.35);backdrop-filter:blur(8px);z-index:20}.modal{max-width:720px;margin:8vh auto;background:#fff;border:1px solid var(--line);border-radius:20px;padding:18px;box-shadow:0 28px 100px rgba(15,23,42,.25)}.modal-backdrop.show{display:block}.checkbox-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:8px}.checkbox-card{display:flex;align-items:center;gap:8px;border:1px solid #e2e8f0;background:#f8fafc;border-radius:13px;padding:9px}@media(max-width:1100px){body{overflow:auto}.shell{grid-template-columns:1fr;height:auto}.side{height:auto}.content{height:auto;overflow:visible}.workbench{grid-template-columns:1fr;height:auto}.calendar{min-height:620px}}@media(max-height:760px){.day .items .chip:nth-child(n+3){display:none}.day{padding:5px}.calendar{gap:5px}.weekday{padding:2px}.content{padding:10px}.workbench{height:calc(100vh - 62px)}}
</style><script>
function esc(v){return String(v ?? '').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));}
function clock(v){if(!v)return '-'; const d=new Date(v); if(isNaN(d))return esc(v); return String(d.getHours()).padStart(2,'0')+':'+String(d.getMinutes()).padStart(2,'0');}
function renderDetail(dayStatus,date,targetId,emptyText){const target=document.getElementById(targetId); if(!target)return; const rows=dayStatus[date]||[]; document.querySelectorAll('[data-current-date]').forEach(el=>el.textContent=date); if(rows.length===0){target.innerHTML='<div class="item-card muted">'+emptyText+'</div>';return;} target.innerHTML=rows.map(r=>'<div class="item-card"><div class="item-head"><b>'+esc(r.staff_name)+'</b><span class="badge">'+esc(r.shift_name)+'</span></div><div class="muted" style="margin-top:6px">'+clock(r.start_time)+' - '+clock(r.end_time)+'</div><div class="row" style="margin-top:8px"><span class="pill '+esc(r.notify_status)+'">'+esc(r.notify_status_label)+'</span><span class="pill '+esc(r.read_status)+'">'+esc(r.read_status_label)+'</span>'+(r.telegram_user_id?'<span class="pill">TG 已绑定</span>':'<span class="pill off">TG 未绑定</span>')+'</div></div>').join('');}
function bindCalendar(opts){const status=opts.status||{};let selected=opts.selected||'';const selectedSet=new Set(opts.multi?[]:[selected]);const cal=document.getElementById(opts.calendarId);if(!cal)return;const input=opts.inputId?document.getElementById(opts.inputId):null;const hint=opts.hintId?document.getElementById(opts.hintId):null;function paint(){cal.querySelectorAll('.day').forEach(el=>{const d=el.dataset.date;el.classList.toggle('selected',selectedSet.has(d));});if(input)input.value=[...selectedSet].sort().join(',');if(hint){const arr=[...selectedSet].sort();hint.textContent=arr.length?('已选择 '+arr.length+' 天：'+arr.join(', ')):'尚未选择日期';}if(opts.modalDatesId){const md=document.getElementById(opts.modalDatesId);if(md){const arr=[...selectedSet].sort();md.textContent=arr.length?arr.join(', '):'-';}}}cal.querySelectorAll('.day').forEach(el=>{el.addEventListener('click',()=>{const d=el.dataset.date;if(!d)return;if(opts.multi){if(selectedSet.has(d)){selectedSet.delete(d)}else{selectedSet.add(d)}selected=d;}else{selectedSet.clear();selectedSet.add(d);selected=d;}renderDetail(status,d,opts.detailId,opts.emptyText||'当天没有排班');paint();});});window.clearSelectedDates=function(){selectedSet.clear();paint();};window.openScheduleModal=function(){if(selectedSet.size===0){alert('请先在日历中选择日期');return;}paint();document.getElementById('scheduleModal').classList.add('show');};window.closeScheduleModal=function(){document.getElementById('scheduleModal').classList.remove('show');};paint();renderDetail(status,selected,opts.detailId,opts.emptyText||'当天没有排班');}
</script></head><body><div class="shell"><aside class="side"><div class="brand"><div class="logo"></div><div><b>devops-worker</b><div class="hint">schedule</div></div></div><nav class="nav"><a href="/">首页</a><a href="/schedule">排班设置</a><a href="/shifts">班次设置</a><a href="/users">用户</a><a href="/approvals">审批</a><a href="/history">历史</a></nav></aside><main class="content"><div class="topline"><div><div class="kicker">devops-worker</div><h1>{{.Title}}</h1></div><div class="version-box"><span class="tag">{{.NowDate}}</span>{{if .Active.VersionID}}<span class="tag">{{versionLabel .Active}}</span>{{end}}{{if .Active.ApprovedBy}}<span class="tag">审批：{{tgName .Active.ApprovedBy .Users}}</span>{{end}}</div></div>{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}{{if .Error}}<div class="err">{{.Error}}</div>{{end}}{{end}}
{{define "layout_end"}}</main></div></body></html>{{end}}`

const dashboardTemplate = `{{define "dashboard"}}{{template "layout_start" .}}<div class="workbench"><section class="panel-card calendar-card"><div class="calendar-head"><a class="btn secondary" href="/?year={{.CalendarPrevYear}}&month={{.CalendarPrevMonth}}&date={{.SelectedDate}}">上月</a><div><div class="calendar-title">{{.CalendarYear}} 年 {{.CalendarMonth}} 月</div><div class="calendar-sub">点击日期查看右侧详情，不刷新页面</div></div><a class="btn secondary" href="/?year={{.CalendarNextYear}}&month={{.CalendarNextMonth}}&date={{.SelectedDate}}">下月</a></div><div class="calendar" id="mainCalendar"><div class="weekday">周一</div><div class="weekday">周二</div><div class="weekday">周三</div><div class="weekday">周四</div><div class="weekday">周五</div><div class="weekday">周六</div><div class="weekday">周日</div>{{range .CalendarDays}}<button type="button" class="day {{if not .IsCurrentMonth}}dim{{end}} {{if .IsWeekend}}weekend{{end}} {{if .IsToday}}today{{end}} {{if .IsSelected}}selected{{end}}" data-date="{{.Date}}"><div class="daynum">{{.Day}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div></button>{{end}}</div></section><aside class="panel-card detail-card"><div class="meta-grid"><div class="meta"><div class="label">版本</div><b>{{versionLabel .Active}}</b></div><div class="meta"><div class="label">审批人</div><b>{{tgName .Active.ApprovedBy .Users}}</b></div><div class="meta"><div class="label">生效时间</div><b>{{if .Active.EffectiveAt}}{{shortTime .Active.EffectiveAt}}{{else}}-{{end}}</b></div><div class="meta"><div class="label">记录数</div><b>{{len .Active.Items}}</b></div></div><h2 style="margin:4px 0 10px"><span data-current-date>{{.SelectedDate}}</span> 排班明细</h2><div class="detail-scroll"><div class="list" id="dayDetail"></div><p class="hint">已读状态来自值班人员点击 Telegram 提醒中的“我已读”。</p></div></aside></div><script>bindCalendar({calendarId:'mainCalendar',detailId:'dayDetail',status:{{json .DayStatus}},selected:'{{.SelectedDate}}',multi:false,emptyText:'当天没有排班'});</script>{{template "layout_end" .}}{{end}}`

const usersTemplate = `{{define "users"}}{{template "layout_start" .}}<div class="card"><h2>新增用户</h2><form method="post" action="/users/create" class="row"><input name="name" placeholder="用户名/别名" required><input name="telegram_user_id" placeholder="Telegram User ID，可为空"><button type="submit">新增用户</button></form></div><div class="card" style="overflow:auto;max-height:calc(100vh - 170px)"><table><thead><tr><th>用户名</th><th>Telegram User ID</th><th>启用</th><th>操作</th></tr></thead><tbody>{{range .Users}}<tr><form method="post" action="/users/update"><input type="hidden" name="id" value="{{.ID}}"><td><input name="name" value="{{.Name}}" required></td><td><input name="telegram_user_id" value="{{.TelegramUserID}}"></td><td><select name="enabled"><option value="true" {{if .Enabled}}selected{{end}}>启用</option><option value="false" {{if not .Enabled}}selected{{end}}>禁用</option></select></td><td><button type="submit">保存</button></form><form method="post" action="/users/delete" style="display:inline"><input type="hidden" name="id" value="{{.ID}}"><button class="danger" type="submit">删除</button></form></td></tr>{{else}}<tr><td colspan="4">暂无用户</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const shiftsTemplate = `{{define "shifts"}}{{template "layout_start" .}}<div class="card"><h2>新增班次</h2><form method="post" action="/shifts/create" class="row shift-form"><input name="code" placeholder="编码，如 morning" required><input name="name" placeholder="名称，如 早班" required><input name="short_name" placeholder="简称，如 早" required><label class="field-mini">开始<select name="start" required>{{range .TimeOptions}}<option value="{{.}}" {{if eq . "09:00"}}selected{{end}}>{{.}}</option>{{end}}</select></label><label class="field-mini">结束<select name="end" required>{{range .TimeOptions}}<option value="{{.}}" {{if eq . "18:00"}}selected{{end}}>{{.}}</option>{{end}}</select></label><button type="submit">新增班次</button></form><p class="hint">班次默认启用；是否跨天会根据开始/结束时间自动判断。修改班次时间后，未来且未触发通知的排班会自动更新并立即生效。</p></div><div class="card" style="overflow:auto;max-height:calc(100vh - 230px)"><table><thead><tr><th>编码</th><th>名称</th><th>简称</th><th>开始</th><th>结束</th><th>跨天</th><th>状态</th><th>操作</th></tr></thead><tbody>{{range .Shifts}}{{$shift := .}}<tr><form method="post" action="/shifts/update"><input type="hidden" name="code" value="{{$shift.Code}}"><td><code>{{$shift.Code}}</code></td><td><input name="name" value="{{$shift.Name}}" required></td><td><input name="short_name" value="{{$shift.ShortName}}" required></td><td><select name="start" required>{{range $.TimeOptions}}<option value="{{.}}" {{if eq . $shift.Start}}selected{{end}}>{{.}}</option>{{end}}</select></td><td><select name="end" required>{{range $.TimeOptions}}<option value="{{.}}" {{if eq . $shift.End}}selected{{end}}>{{.}}</option>{{end}}</select></td><td>{{if $shift.CrossDay}}<span class="pill ok">自动跨天</span>{{else}}<span class="pill off">当天</span>{{end}}</td><td><select name="enabled"><option value="true" {{if $shift.Enabled}}selected{{end}}>启用</option><option value="false" {{if not $shift.Enabled}}selected{{end}}>禁用</option></select></td><td><button type="submit">保存</button></form><form method="post" action="/shifts/delete" style="display:inline"><input type="hidden" name="code" value="{{$shift.Code}}"><button class="danger" type="submit">删除</button></form></td></tr>{{else}}<tr><td colspan="8">暂无班次</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const scheduleTemplate = `{{define "schedule"}}{{template "layout_start" .}}<div class="workbench"><section class="panel-card calendar-card"><div class="calendar-head"><a class="btn secondary" href="/schedule?year={{.CalendarPrevYear}}&month={{.CalendarPrevMonth}}&date={{.SelectedDate}}">上月</a><div><div class="calendar-title">{{.CalendarYear}} 年 {{.CalendarMonth}} 月排班设置</div><div class="calendar-sub">选择一个或多个日期，右侧查看详情，下方提交审批</div></div><a class="btn secondary" href="/schedule?year={{.CalendarNextYear}}&month={{.CalendarNextMonth}}&date={{.SelectedDate}}">下月</a></div><div class="calendar" id="scheduleCalendar"><div class="weekday">周一</div><div class="weekday">周二</div><div class="weekday">周三</div><div class="weekday">周四</div><div class="weekday">周五</div><div class="weekday">周六</div><div class="weekday">周日</div>{{range .CalendarDays}}<button type="button" class="day {{if not .IsCurrentMonth}}dim{{end}} {{if .IsWeekend}}weekend{{end}} {{if .IsToday}}today{{end}}" data-date="{{.Date}}" {{if not .IsCurrentMonth}}disabled{{end}}><div class="daynum">{{.Day}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div></button>{{end}}</div><div class="row" style="margin-top:8px"><button type="button" onclick="openScheduleModal()">设置所选日期排班</button><button type="button" class="btn secondary" onclick="clearSelectedDates()">清空选择</button><span class="hint" id="selectedHint">尚未选择日期</span></div></section><aside class="panel-card detail-card"><div class="meta-grid"><div class="meta"><div class="label">当前版本</div><b>{{versionLabel .Active}}</b></div><div class="meta"><div class="label">排班记录</div><b>{{len .Active.Items}}</b></div></div><h2 style="margin:4px 0 10px"><span data-current-date>{{.SelectedDate}}</span> 当前排班</h2><div class="detail-scroll"><div class="list" id="scheduleDayDetail"></div></div></aside></div><div class="modal-backdrop" id="scheduleModal"><div class="modal"><h2>设置所选日期排班</h2><p class="muted">已选日期：<span id="modalDates">-</span></p><form method="post" action="/schedule/submit"><input type="hidden" name="year" value="{{.CalendarYear}}"><input type="hidden" name="month" value="{{.CalendarMonth}}"><input type="hidden" name="selected_dates" id="selectedDatesInput"><div class="grid"><div><h3>班次</h3><select name="shift_code" required>{{range .Shifts}}{{if .Enabled}}<option value="{{.Code}}">{{.Name}} {{.Start}}-{{.End}}</option>{{end}}{{end}}</select></div><div><h3>提交人</h3><input name="created_by" value="admin"></div></div><h3>选择用户</h3><div class="checkbox-grid">{{range .Users}}{{if .Enabled}}<label class="checkbox-card"><input type="checkbox" name="staff_ids" value="{{.ID}}">{{.Name}}</label>{{end}}{{end}}</div><div class="row" style="margin-top:16px"><button type="submit">提交 Telegram 审批</button><button type="button" class="btn secondary" onclick="closeScheduleModal()">取消</button></div></form></div></div><script>bindCalendar({calendarId:'scheduleCalendar',detailId:'scheduleDayDetail',inputId:'selectedDatesInput',hintId:'selectedHint',modalDatesId:'modalDates',status:{{json .DayStatus}},selected:'{{.SelectedDate}}',multi:true,emptyText:'当天没有排班'});</script>{{template "layout_end" .}}{{end}}`

const approvalsTemplate = `{{define "approvals"}}{{template "layout_start" .}}<div class="card" style="overflow:auto;max-height:calc(100vh - 110px)"><h2>审批记录</h2><table><thead><tr><th>审批</th><th>状态</th><th>版本</th><th>创建</th><th>审批人</th><th>预览</th></tr></thead><tbody>{{range .Approvals}}<tr><td><span class="tag">{{compact .ID}}</span></td><td><span class="badge">{{approvalStatus .Status}}</span></td><td>{{.BaseRevision}} → {{.NewRevision}}</td><td>{{.CreatedBy}}<div class="hint">{{shortTime .CreatedAt}}</div></td><td>{{if .ReviewedBy}}{{tgName .ReviewedBy $.Users}}{{else}}{{tgNames .ApproverIDs $.Users}}{{end}}</td><td><a class="btn secondary" href="/{{.PreviewHTML}}" target="_blank">HTML</a></td></tr>{{else}}<tr><td colspan="6">暂无审批记录</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const historyTemplate = `{{define "history"}}{{template "layout_start" .}}<div class="workbench"><section class="panel-card calendar-card"><div class="calendar-head"><a class="btn secondary" href="/history?year={{.CalendarPrevYear}}&month={{.CalendarPrevMonth}}&date={{.SelectedDate}}">上月</a><div><div class="calendar-title">{{.CalendarYear}} 年 {{.CalendarMonth}} 月历史</div><div class="calendar-sub">点击日期查看右侧历史排班</div></div><a class="btn secondary" href="/history?year={{.CalendarNextYear}}&month={{.CalendarNextMonth}}&date={{.SelectedDate}}">下月</a></div><div class="calendar" id="historyCalendar"><div class="weekday">周一</div><div class="weekday">周二</div><div class="weekday">周三</div><div class="weekday">周四</div><div class="weekday">周五</div><div class="weekday">周六</div><div class="weekday">周日</div>{{range .CalendarDays}}<button type="button" class="day {{if not .IsCurrentMonth}}dim{{end}} {{if .IsWeekend}}weekend{{end}} {{if .IsToday}}today{{end}} {{if .IsSelected}}selected{{end}}" data-date="{{.Date}}"><div class="daynum">{{.Day}}</div><div class="items">{{range .Items}}<span class="chip">{{.StaffName}} {{.ShiftShortName}}</span>{{end}}</div></button>{{end}}</div></section><aside class="panel-card detail-card"><h2 style="margin:4px 0 10px"><span data-current-date>{{.SelectedDate}}</span> 历史排班</h2><form method="get" action="/history" class="row" style="margin-bottom:10px"><input type="date" name="date" value="{{.HistoryDate}}"><button type="submit">跳转</button></form><div class="detail-scroll"><div class="list" id="historyDayDetail"></div></div></aside></div><script>bindCalendar({calendarId:'historyCalendar',detailId:'historyDayDetail',status:{{json .DayStatus}},selected:'{{.SelectedDate}}',multi:false,emptyText:'当天没有历史排班'});</script>{{template "layout_end" .}}{{end}}`
