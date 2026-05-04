package main

import (
	"bytes"
	"html/template"
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
	tpl := template.Must(template.New("preview").Parse(previewTemplate))
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderPage(wr *bytes.Buffer, name string, data PageData) error {
	tpl := template.Must(template.New("base").Funcs(template.FuncMap{
		"clock": func(s string) string {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t.Format("15:04")
			}
			return s
		},
		"dateOnly": func(s string) string {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t.Format("2006-01-02")
			}
			return s
		},
	}).Parse(baseTemplate + dashboardTemplate + usersTemplate + scheduleTemplate + approvalsTemplate + historyTemplate))
	return tpl.ExecuteTemplate(wr, name, data)
}

const previewTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>排班审批预览</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Noto Sans SC",sans-serif;background:#f7f7f8;color:#222;margin:0;padding:24px}.card{background:#fff;border-radius:14px;padding:20px;box-shadow:0 4px 18px rgba(0,0,0,.06);max-width:1100px;margin:auto}table{width:100%;border-collapse:collapse;margin-top:16px}th,td{border-bottom:1px solid #eee;padding:10px;text-align:left}th{background:#fafafa}.badge{display:inline-block;padding:3px 8px;border-radius:999px;background:#eef2ff}.muted{color:#666}.summary{display:flex;gap:12px;flex-wrap:wrap}.box{background:#f5f5f5;border-radius:10px;padding:10px 14px}</style>
</head><body><div class="card"><h1>排班策略生效预览</h1><p class="muted">审批ID：{{.ApprovalID}} ｜ 生成时间：{{.GeneratedAt}}</p><div class="summary"><div class="box">当前版本：{{.ActiveRevision}}</div><div class="box">审批通过后版本：{{.NewRevision}}</div><div class="box">排班记录：{{len .Items}} 条</div></div><table><thead><tr><th>日期</th><th>员工</th><th>班次</th><th>开始</th><th>结束</th><th>Telegram ID</th></tr></thead><tbody>{{range .Items}}<tr><td>{{.Date}}</td><td>{{.StaffName}}</td><td><span class="badge">{{.ShiftName}}</span></td><td>{{.StartTime}}</td><td>{{.EndTime}}</td><td>{{.TelegramUserID}}</td></tr>{{else}}<tr><td colspan="6">没有生成任何排班记录</td></tr>{{end}}</tbody></table></div></body></html>`

const baseTemplate = `{{define "layout_start"}}<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} - devops-worker</title><style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Noto Sans SC",sans-serif;background:#f6f7fb;margin:0;color:#1f2937}a{color:#2563eb;text-decoration:none}.nav{background:#111827;color:#fff;padding:14px 24px;display:flex;gap:18px;align-items:center}.nav a{color:#fff}.wrap{max-width:1180px;margin:24px auto;padding:0 18px}.card{background:#fff;border-radius:16px;padding:22px;box-shadow:0 8px 24px rgba(15,23,42,.06);margin-bottom:18px}table{width:100%;border-collapse:collapse}th,td{border-bottom:1px solid #e5e7eb;padding:10px;text-align:left}th{background:#f9fafb}.btn{display:inline-block;border:0;border-radius:10px;background:#2563eb;color:#fff;padding:9px 14px;cursor:pointer}.btn.danger{background:#dc2626}.btn.gray{background:#6b7280}input,select{padding:8px;border:1px solid #d1d5db;border-radius:8px;margin:4px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px}.msg{padding:10px 14px;border-radius:10px;background:#ecfdf5;color:#065f46}.err{padding:10px 14px;border-radius:10px;background:#fef2f2;color:#991b1b}.muted{color:#6b7280}.badge{display:inline-block;padding:3px 8px;border-radius:999px;background:#eef2ff}.row{display:flex;gap:10px;flex-wrap:wrap;align-items:center}</style></head><body><div class="nav"><b>devops-worker</b><a href="/">首页</a><a href="/users">用户管理</a><a href="/schedule">排班设置</a><a href="/approvals">审批记录</a><a href="/history">历史查询</a></div><div class="wrap">{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}{{if .Error}}<div class="err">{{.Error}}</div>{{end}}{{end}}
{{define "layout_end"}}</div></body></html>{{end}}`

const dashboardTemplate = `{{define "dashboard"}}{{template "layout_start" .}}<div class="card"><h1>今日排班</h1><p class="muted">当前正式版本：{{.Active.Revision}} ｜ 生效时间：{{.Active.EffectiveAt}} ｜ 来源审批：{{.Active.SourceApprovalID}}</p><table><thead><tr><th>日期</th><th>员工</th><th>班次</th><th>开始</th><th>结束</th><th>Telegram ID</th></tr></thead><tbody>{{range .TodayItems}}<tr><td>{{.Date}}</td><td>{{.StaffName}}</td><td><span class="badge">{{.ShiftName}}</span></td><td>{{clock .StartTime}}</td><td>{{clock .EndTime}}</td><td>{{.TelegramUserID}}</td></tr>{{else}}<tr><td colspan="6">今天没有排班</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const usersTemplate = `{{define "users"}}{{template "layout_start" .}}<div class="card"><h1>用户管理</h1><form method="post" action="/users/create" class="row"><input name="name" placeholder="用户名/别名" required><input name="telegram_user_id" placeholder="Telegram User ID，可为空"><button class="btn" type="submit">新增用户</button></form></div><div class="card"><table><thead><tr><th>用户名</th><th>Telegram User ID</th><th>启用</th><th>操作</th></tr></thead><tbody>{{range .Users}}<tr><form method="post" action="/users/update"><input type="hidden" name="id" value="{{.ID}}"><td><input name="name" value="{{.Name}}" required></td><td><input name="telegram_user_id" value="{{.TelegramUserID}}"></td><td><select name="enabled"><option value="true" {{if .Enabled}}selected{{end}}>启用</option><option value="false" {{if not .Enabled}}selected{{end}}>禁用</option></select></td><td><button class="btn" type="submit">保存</button></form><form method="post" action="/users/delete" style="display:inline"><input type="hidden" name="id" value="{{.ID}}"><button class="btn danger" type="submit">删除</button></form></td></tr>{{else}}<tr><td colspan="4">暂无用户</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const scheduleTemplate = `{{define "schedule"}}{{template "layout_start" .}}<div class="card"><h1>创建排班策略</h1><p class="muted">周次定义：第1周=本月1-7日，第2周=8-14日，以此类推。提交后不会立即生效，必须经 Telegram 审批通过。</p><form method="post" action="/schedule/submit"><div class="grid"><div><h3>年月</h3><input name="year" type="number" value="{{.NowYear}}" required><select name="month">{{range .Months}}<option value="{{.}}">{{.}}月</option>{{end}}</select></div><div><h3>班次</h3><select name="shift_code">{{range .Shifts}}<option value="{{.Code}}">{{.Name}} {{.Start}}-{{.End}}</option>{{end}}</select></div><div><h3>提交人</h3><input name="created_by" value="admin"></div></div><h3>选择周次</h3><div class="row">{{range .WeekNums}}<label><input type="checkbox" name="week_nums" value="{{.}}">第{{.}}周</label>{{end}}</div><h3>选择星期</h3><div class="row"><label><input type="checkbox" name="weekdays" value="1">周一</label><label><input type="checkbox" name="weekdays" value="2">周二</label><label><input type="checkbox" name="weekdays" value="3">周三</label><label><input type="checkbox" name="weekdays" value="4">周四</label><label><input type="checkbox" name="weekdays" value="5">周五</label><label><input type="checkbox" name="weekdays" value="6">周六</label><label><input type="checkbox" name="weekdays" value="7">周日</label></div><h3>选择用户</h3><div class="row">{{range .Users}}{{if .Enabled}}<label><input type="checkbox" name="staff_ids" value="{{.ID}}">{{.Name}}</label>{{end}}{{end}}</div><p><button class="btn" type="submit">提交 Telegram 审批</button></p></form></div>{{template "layout_end" .}}{{end}}`

const approvalsTemplate = `{{define "approvals"}}{{template "layout_start" .}}<div class="card"><h1>审批记录</h1><table><thead><tr><th>审批ID</th><th>状态</th><th>版本</th><th>创建人</th><th>创建时间</th><th>审批人</th><th>预览</th></tr></thead><tbody>{{range .Approvals}}<tr><td>{{.ID}}</td><td><span class="badge">{{.Status}}</span></td><td>{{.BaseRevision}} -> {{.NewRevision}}</td><td>{{.CreatedBy}}</td><td>{{.CreatedAt}}</td><td>{{.ReviewedBy}}</td><td><a href="/{{.PreviewHTML}}" target="_blank">打开 HTML</a></td></tr>{{else}}<tr><td colspan="7">暂无审批记录</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`

const historyTemplate = `{{define "history"}}{{template "layout_start" .}}<div class="card"><h1>历史排班查询</h1><form method="get" action="/history" class="row"><input type="date" name="date" value="{{.HistoryDate}}"><button class="btn" type="submit">查询</button></form></div><div class="card"><h2>{{.HistoryDate}}</h2><table><thead><tr><th>日期</th><th>员工</th><th>班次</th><th>开始</th><th>结束</th><th>Telegram ID</th></tr></thead><tbody>{{range .History}}<tr><td>{{.Date}}</td><td>{{.StaffName}}</td><td><span class="badge">{{.ShiftName}}</span></td><td>{{clock .StartTime}}</td><td>{{clock .EndTime}}</td><td>{{.TelegramUserID}}</td></tr>{{else}}<tr><td colspan="6">没有历史记录</td></tr>{{end}}</tbody></table></div>{{template "layout_end" .}}{{end}}`
