package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TelegramService struct {
	Cfg        Config
	Store      *Storage
	Loc        *time.Location
	HTTPClient *http.Client
	BaseURL    string
	BotName    string
	BotID      int64
	QueueWake  chan struct{}
}

type telegramAPIResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
}

type telegramMe struct {
	ID       int64  `json:"id"`
	UserName string `json:"username"`
}

type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramMessage struct {
	MessageID       int           `json:"message_id"`
	MessageThreadID int           `json:"message_thread_id"`
	Chat            telegramChat  `json:"chat"`
	From            *telegramUser `json:"from"`
	Text            string        `json:"text"`
}

type telegramChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	UserName  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    telegramUser     `json:"from"`
	Message *telegramMessage `json:"message"`
	Data    string           `json:"data"`
}

func NewTelegramService(cfg Config, store *Storage, loc *time.Location) (*TelegramService, error) {
	if cfg.BotToken == "" {
		return nil, nil
	}
	t := &TelegramService{
		Cfg:        cfg,
		Store:      store,
		Loc:        loc,
		HTTPClient: &http.Client{Timeout: 65 * time.Second},
		BaseURL:    "https://api.telegram.org/bot" + cfg.BotToken + "/",
	}
	me, err := t.getMe()
	if err != nil {
		return nil, err
	}
	t.BotName = me.UserName
	t.BotID = me.ID
	log.Printf("Telegram bot initialized: @%s id=%d", t.BotName, t.BotID)
	if err := t.deleteWebhook(false); err != nil {
		log.Printf("WARN: delete webhook failed: %v", err)
	}
	return t, nil
}

func (t *TelegramService) StartPollingAndScheduler() {
	if t == nil {
		return
	}
	lockPath := filepath.Join(t.Cfg.DataDir, "locks", "bot.lock")
	lockFile, err := TryAcquireLock(lockPath)
	if err != nil {
		log.Printf("WARN: bot lock unavailable, this instance will not run getUpdates/scheduler: %v", err)
		return
	}
	_ = lockFile
	log.Printf("bot lock acquired; starting getUpdates and scheduler")
	go t.startScheduler()
	go t.startDailyScheduleReportScheduler()
	go t.pollUpdates()
}

func (t *TelegramService) pollUpdates() {
	var offset int64
	for {
		updates, err := t.getUpdates(offset, 30)
		if err != nil {
			log.Printf("getUpdates failed, retrying in 3 seconds: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.CallbackQuery != nil {
				t.handleCallback(update.CallbackQuery)
				continue
			}
			if update.Message != nil && update.Message.Text != "" {
				t.handleText(update.Message)
			}
		}
	}
}

func (t *TelegramService) getMe() (telegramMe, error) {
	var resp telegramAPIResponse[telegramMe]
	err := t.getJSON("getMe", nil, &resp)
	if err != nil {
		return telegramMe{}, err
	}
	if !resp.OK {
		return telegramMe{}, fmt.Errorf(resp.Description)
	}
	return resp.Result, nil
}

func (t *TelegramService) deleteWebhook(dropPending bool) error {
	vals := url.Values{}
	vals.Set("drop_pending_updates", strconv.FormatBool(dropPending))
	var resp telegramAPIResponse[bool]
	if err := t.getJSON("deleteWebhook", vals, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf(resp.Description)
	}
	return nil
}

func (t *TelegramService) getUpdates(offset int64, timeout int) ([]telegramUpdate, error) {
	vals := url.Values{}
	vals.Set("timeout", strconv.Itoa(timeout))
	if offset > 0 {
		vals.Set("offset", strconv.FormatInt(offset, 10))
	}
	vals.Set("allowed_updates", `["message","callback_query"]`)
	var resp telegramAPIResponse[[]telegramUpdate]
	if err := t.getJSON("getUpdates", vals, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf(resp.Description)
	}
	return resp.Result, nil
}

func (t *TelegramService) getJSON(method string, vals url.Values, out any) error {
	endpoint := t.BaseURL + method
	if vals != nil {
		endpoint += "?" + vals.Encode()
	}
	resp, err := t.HTTPClient.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram http %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

func (t *TelegramService) postForm(method string, vals url.Values, out any) error {
	resp, err := t.HTTPClient.PostForm(t.BaseURL+method, vals)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram http %d: %s", resp.StatusCode, string(body))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

func (t *TelegramService) SendApproval(a *Approval) error {
	if t == nil || a == nil {
		return nil
	}
	approverText := t.approverMentionText(a.ApproverIDs)
	createdAtText := dateTimeWithWeekday(a.CreatedAt, t.Loc)
	caption := fmt.Sprintf("📋 <b>排班策略审批请求</b>\n\n审批ID: <code>%s</code>\n事务ID: <code>%s</code>\n提交人: %s\n提交时间: %s\n变更记录: %d\n审批人: %s\n\n请打开 HTML 附件查看预览。任一审批人点击同意或拒绝后，所有审批窗口会同步更新状态。", html.EscapeString(a.ID), html.EscapeString(a.TransactionID), html.EscapeString(a.CreatedBy), html.EscapeString(createdAtText), len(a.Rules), approverText)
	replyMarkup := approvalActionMarkup(a.ID, false)
	previewPath := filepath.Join(t.Cfg.DataDir, a.PreviewHTML)
	refs := make([]ApprovalMessageRef, 0, len(t.Cfg.GroupChatIDs)+len(a.ApproverIDs))
	var lastErr error
	for _, chatID := range t.Cfg.GroupChatIDs {
		threadID := t.getThreadID(chatID, 0)
		ref, err := t.sendDocument(chatID, threadID, previewPath, caption, "HTML", replyMarkup)
		if err != nil {
			lastErr = err
			log.Printf("send approval to group %d failed: %v", chatID, err)
			continue
		}
		refs = append(refs, ref)
	}
	for _, uid := range a.ApproverIDs {
		ref, err := t.sendDocument(uid, 0, previewPath, caption, "HTML", replyMarkup)
		if err != nil {
			lastErr = err
			log.Printf("send approval to approver %d failed: %v", uid, err)
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) > 0 {
		a.MessageRefs = append(a.MessageRefs, refs...)
		if err := t.Store.AttachApprovalMessageRefs(a.ID, refs); err != nil {
			log.Printf("attach approval message refs failed: %v", err)
		}
	}
	return lastErr
}

func approvalActionMarkup(approvalID string, disabled bool) map[string]any {
	if disabled {
		return map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "审批已结束", "callback_data": "noop:" + approvalID}}}}
	}
	return map[string]any{
		"inline_keyboard": [][]map[string]string{{
			{"text": "✅ 同意生效", "callback_data": "approve:" + approvalID},
			{"text": "❌ 拒绝", "callback_data": "reject:" + approvalID},
		}},
	}
}

func (t *TelegramService) sendDocument(chatID int64, threadID int, filePath string, caption string, parseMode string, replyMarkup any) (ApprovalMessageRef, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if threadID > 0 {
		_ = mw.WriteField("message_thread_id", strconv.Itoa(threadID))
	}
	if caption != "" {
		_ = mw.WriteField("caption", caption)
	}
	if parseMode != "" {
		_ = mw.WriteField("parse_mode", parseMode)
	}
	if replyMarkup != nil {
		rm, _ := json.Marshal(replyMarkup)
		_ = mw.WriteField("reply_markup", string(rm))
	}
	file, err := os.Open(filePath)
	if err != nil {
		return ApprovalMessageRef{}, err
	}
	defer file.Close()
	fw, err := mw.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return ApprovalMessageRef{}, err
	}
	if _, err := io.Copy(fw, file); err != nil {
		return ApprovalMessageRef{}, err
	}
	if err := mw.Close(); err != nil {
		return ApprovalMessageRef{}, err
	}
	req, err := http.NewRequest(http.MethodPost, t.BaseURL+"sendDocument", &body)
	if err != nil {
		return ApprovalMessageRef{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := t.HTTPClient.Do(req)
	if err != nil {
		return ApprovalMessageRef{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return ApprovalMessageRef{}, fmt.Errorf("telegram http %d: %s", resp.StatusCode, string(respBody))
	}
	var apiResp telegramAPIResponse[telegramMessage]
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ApprovalMessageRef{}, err
	}
	if !apiResp.OK {
		return ApprovalMessageRef{}, fmt.Errorf(apiResp.Description)
	}
	return ApprovalMessageRef{ChatID: chatID, ThreadID: threadID, MessageID: apiResp.Result.MessageID}, nil
}

func (t *TelegramService) editApprovalMessages(a *Approval) {
	if t == nil || a == nil || len(a.MessageRefs) == 0 {
		return
	}
	reviewer := t.telegramDisplayName(a.ReviewedBy)
	if strings.TrimSpace(a.ReviewedByName) != "" {
		reviewer = a.ReviewedByName
	}
	statusLine := "审批已结束"
	if a.Status == "approved" {
		statusLine = "✅ " + reviewer + " 审批通过，排班已按最新正式数据合并生效"
	} else if a.Status == "rejected" {
		statusLine = "❌ " + reviewer + " 审批拒绝，本次策略未生效"
	}
	caption := fmt.Sprintf("📋 <b>排班策略审批结果</b>\n\n审批ID: <code>%s</code>\n事务ID: <code>%s</code>\n状态: %s\n处理时间: %s\n\n所有审批窗口已同步更新。", html.EscapeString(a.ID), html.EscapeString(a.TransactionID), html.EscapeString(statusLine), html.EscapeString(dateTimeWithWeekday(a.ReviewedAt, t.Loc)))
	for _, ref := range a.MessageRefs {
		if err := t.editMessageCaption(ref.ChatID, ref.MessageID, caption, "HTML", approvalActionMarkup(a.ID, true)); err != nil {
			log.Printf("edit approval message failed chat=%d msg=%d: %v", ref.ChatID, ref.MessageID, err)
		}
	}
}

func (t *TelegramService) editMessageCaption(chatID int64, messageID int, caption string, parseMode string, replyMarkup any) error {
	vals := url.Values{}
	vals.Set("chat_id", strconv.FormatInt(chatID, 10))
	vals.Set("message_id", strconv.Itoa(messageID))
	vals.Set("caption", caption)
	if parseMode != "" {
		vals.Set("parse_mode", parseMode)
	}
	if replyMarkup != nil {
		rm, _ := json.Marshal(replyMarkup)
		vals.Set("reply_markup", string(rm))
	}
	var resp telegramAPIResponse[json.RawMessage]
	if err := t.postForm("editMessageCaption", vals, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf(resp.Description)
	}
	return nil
}

func (t *TelegramService) handleCallback(cq *telegramCallbackQuery) {
	parts := strings.SplitN(cq.Data, ":", 2)
	if len(parts) != 2 {
		t.answerCallback(cq.ID, "无效操作", true)
		return
	}
	action, approvalID := parts[0], parts[1]
	reviewerID := cq.From.ID
	switch action {
	case "approve":
		approval, err := t.Store.Approve(approvalID, reviewerID, t.Loc)
		if err != nil {
			t.answerCallback(cq.ID, "审批失败: "+err.Error(), true)
			return
		}
		t.answerCallback(cq.ID, "已同意，排班已生效", true)
		t.editApprovalMessages(approval)
		// Approval changes the active schedule, so refresh notification tasks
		// immediately instead of waiting for the next 300 second queue cycle.
		t.WakeNotificationQueue()
		t.replyToCallbackMessage(cq, fmt.Sprintf("✅ 审批已通过并生效\n审批ID: %s\n审批人: %s\n处理时间: %s", approval.ID, t.telegramDisplayName(reviewerID), dateTimeWithWeekday(approval.ReviewedAt, t.Loc)))
	case "reject":
		approval, err := t.Store.Reject(approvalID, reviewerID, "telegram callback rejected")
		if err != nil {
			t.answerCallback(cq.ID, "拒绝失败: "+err.Error(), true)
			return
		}
		t.answerCallback(cq.ID, "已拒绝，本次策略未生效", true)
		t.editApprovalMessages(approval)
		t.replyToCallbackMessage(cq, fmt.Sprintf("❌ 审批已拒绝，策略未生效\n审批ID: %s\n审批人: %s\n处理时间: %s", approval.ID, t.telegramDisplayName(reviewerID), dateTimeWithWeekday(approval.ReviewedAt, t.Loc)))
	case "noop":
		t.answerCallback(cq.ID, "该审批已经结束", false)
	case "read":
		rec, firstRead, err := t.Store.MarkNotificationRead(approvalID, reviewerID)
		if err != nil {
			t.answerCallback(cq.ID, "确认失败: "+err.Error(), true)
			return
		}
		if firstRead {
			t.answerCallback(cq.ID, "已记录已读", false)
			t.editCallbackMessageMarkup(cq, readDoneMarkup())
		} else {
			t.answerCallback(cq.ID, fmt.Sprintf("%s 已确认，无需重复点击", rec.StaffName), false)
			t.editCallbackMessageMarkup(cq, readDoneMarkup())
		}
	default:
		t.answerCallback(cq.ID, "未知操作", true)
	}
}

func (t *TelegramService) answerCallback(id, text string, alert bool) {
	vals := url.Values{}
	vals.Set("callback_query_id", id)
	vals.Set("text", text)
	vals.Set("show_alert", strconv.FormatBool(alert))
	var resp telegramAPIResponse[bool]
	if err := t.postForm("answerCallbackQuery", vals, &resp); err != nil {
		log.Printf("answer callback failed: %v", err)
	}
}

func (t *TelegramService) approverMentionText(ids []int64) string {
	if len(ids) == 0 {
		return "未配置"
	}
	users, _ := t.Store.LoadUsers()
	nameByID := map[int64]string{}
	for _, u := range users {
		if u.TelegramUserID != 0 {
			nameByID[u.TelegramUserID] = u.Name
		}
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		name := nameByID[id]
		if name == "" {
			name = fmt.Sprintf("审批人%d", id)
		}
		parts = append(parts, fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", id, html.EscapeString(name)))
	}
	return strings.Join(parts, "、")
}

func (t *TelegramService) telegramDisplayName(id int64) string {
	users, err := t.Store.LoadUsers()
	if err == nil {
		for _, u := range users {
			if u.TelegramUserID == id {
				return u.Name
			}
		}
	}
	return "未绑定审批人"
}

func (t *TelegramService) replyToCallbackMessage(cq *telegramCallbackQuery, text string) {
	if cq.Message == nil {
		return
	}
	_ = t.sendMessage(cq.Message.Chat.ID, cq.Message.MessageThreadID, text, "")
}

func readDoneMarkup() any {
	return map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "✅ 已读", "callback_data": "noop:read"}}}}
}

func (t *TelegramService) editCallbackMessageMarkup(cq *telegramCallbackQuery, replyMarkup any) {
	if cq == nil || cq.Message == nil {
		return
	}
	if err := t.editMessageReplyMarkup(cq.Message.Chat.ID, cq.Message.MessageID, replyMarkup); err != nil {
		log.Printf("edit read markup failed: %v", err)
	}
}

func (t *TelegramService) editMessageReplyMarkup(chatID int64, messageID int, replyMarkup any) error {
	vals := url.Values{}
	vals.Set("chat_id", strconv.FormatInt(chatID, 10))
	vals.Set("message_id", strconv.Itoa(messageID))
	if replyMarkup != nil {
		rm, _ := json.Marshal(replyMarkup)
		vals.Set("reply_markup", string(rm))
	}
	var resp telegramAPIResponse[json.RawMessage]
	if err := t.postForm("editMessageReplyMarkup", vals, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf(resp.Description)
	}
	return nil
}

func (t *TelegramService) handleText(msg *telegramMessage) {
	text := msg.Text
	keywords := []string{"值班", "排班", "谁值班", "值班人员", "今天谁值班"}
	matched := false
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			matched = true
			break
		}
	}
	if !matched {
		return
	}
	today := time.Now().In(t.Loc).Format("2006-01-02")
	items, err := t.Store.LoadDay(today)
	threadID := t.getThreadID(msg.Chat.ID, msg.MessageThreadID)
	if err != nil {
		_ = t.sendMessage(msg.Chat.ID, threadID, "查询排班失败: "+err.Error(), "")
		return
	}
	dateTitle := dateWithWeekday(today, t.Loc)
	if len(items) == 0 {
		_ = t.sendMessage(msg.Chat.ID, threadID, fmt.Sprintf("📅 %s\n\n今天没有排班。", dateTitle), "")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📅 <b>%s 今日排班</b>\n\n", html.EscapeString(dateTitle)))
	for _, item := range items {
		b.WriteString(fmt.Sprintf("• %s %s %s-%s\n", html.EscapeString(item.StaffName), html.EscapeString(item.ShiftName), html.EscapeString(formatClock(item.StartTime)), html.EscapeString(formatClock(item.EndTime))))
	}
	_ = t.sendMessage(msg.Chat.ID, threadID, b.String(), "HTML")
}

func (t *TelegramService) startDailyScheduleReportScheduler() {
	if t == nil || t.Store == nil {
		return
	}
	for {
		wait := t.maybeSendDailyScheduleReport()
		if wait < time.Second {
			wait = time.Minute
		}
		time.Sleep(wait)
	}
}

func (t *TelegramService) maybeSendDailyScheduleReport() time.Duration {
	if t == nil || t.Store == nil || len(t.Cfg.GroupChatIDs) == 0 {
		return 30 * time.Minute
	}
	now := time.Now().In(t.Loc)
	reportAt := dailyReportTimeForDate(now, t.Cfg.DailyReportTime, t.Loc)
	if now.Before(reportAt) {
		return time.Until(reportAt)
	}

	date := now.Format("2006-01-02")
	items, err := t.Store.LoadDay(date)
	if err != nil {
		log.Printf("daily schedule report load day failed date=%s: %v", date, err)
		return 300 * time.Second
	}
	statuses := t.Store.BuildScheduleItemStatuses(items, t.Loc)
	messages := t.buildDailyScheduleReportMessages(date, statuses)
	if len(messages) == 0 {
		messages = []string{fmt.Sprintf("📋 <b>%s 排班明细</b>\n\n今日无排班。", html.EscapeString(dateWithWeekday(date, t.Loc)))}
	}

	allSent := true
	for _, chatID := range t.Cfg.GroupChatIDs {
		if chatID == 0 {
			continue
		}
		if t.Store.DailyReportAlreadySent(date, chatID) {
			continue
		}
		threadID := t.getThreadID(chatID, 0)
		chatSent := true
		for i, msg := range messages {
			if err := t.sendMessage(chatID, threadID, msg, "HTML"); err != nil {
				chatSent = false
				allSent = false
				log.Printf("send daily schedule report failed chat=%d part=%d/%d date=%s: %v", chatID, i+1, len(messages), date, err)
				break
			}
		}
		if chatSent {
			if err := t.Store.RecordDailyReportSent(date, chatID, time.Now()); err != nil {
				allSent = false
				log.Printf("record daily schedule report sent failed chat=%d date=%s: %v", chatID, date, err)
			}
		}
	}
	if !allSent {
		return 300 * time.Second
	}
	return time.Until(dailyReportTimeForDate(now.AddDate(0, 0, 1), t.Cfg.DailyReportTime, t.Loc))
}

func dailyReportTimeForDate(day time.Time, clock string, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	hour, min := 9, 0
	if h, m, err := parseDailyReportClock(clock); err == nil {
		hour, min = h, m
	}
	d := day.In(loc)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, loc)
}

func parseDailyReportClock(clock string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(clock), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid daily report time")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid daily report time range")
	}
	return h, m, nil
}

type dailyScheduleGroup struct {
	Code  string
	Name  string
	Start string
	End   string
	Rank  int
	Items []ScheduleItemStatus
}

func (t *TelegramService) buildDailyScheduleReportMessages(date string, statuses []ScheduleItemStatus) []string {
	sort.Slice(statuses, func(i, j int) bool {
		ri, rj := scheduleStatusRank(statuses[i]), scheduleStatusRank(statuses[j])
		if ri != rj {
			return ri < rj
		}
		if statuses[i].ShiftName != statuses[j].ShiftName {
			return statuses[i].ShiftName < statuses[j].ShiftName
		}
		return statuses[i].StaffName < statuses[j].StaffName
	})

	groupsByCode := map[string]*dailyScheduleGroup{}
	var groups []*dailyScheduleGroup
	for _, st := range statuses {
		code := strings.TrimSpace(st.ShiftCode)
		if code == "" {
			code = st.ShiftName
		}
		g := groupsByCode[code]
		if g == nil {
			g = &dailyScheduleGroup{Code: code, Name: st.ShiftName, Start: st.StartClock, End: st.EndClock, Rank: scheduleStatusRank(st)}
			groupsByCode[code] = g
			groups = append(groups, g)
		}
		g.Items = append(g.Items, st)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Rank != groups[j].Rank {
			return groups[i].Rank < groups[j].Rank
		}
		return groups[i].Name < groups[j].Name
	})

	header := fmt.Sprintf("📋 <b>%s 排班明细</b>\n<code>自动日报 %s</code>\n", html.EscapeString(dateWithWeekday(date, t.Loc)), html.EscapeString(strings.TrimSpace(t.Cfg.DailyReportTime)))
	if strings.TrimSpace(t.Cfg.WorkOrderURL) != "" {
		header += fmt.Sprintf("工单: %s\n", html.EscapeString(strings.TrimSpace(t.Cfg.WorkOrderURL)))
	}

	var messages []string
	current := header
	appendChunk := func() {
		if strings.TrimSpace(current) != "" {
			messages = append(messages, current)
		}
		current = header
	}
	for _, g := range groups {
		section := t.renderDailyScheduleGroup(g)
		if len([]rune(current))+len([]rune(section)) > 3600 && current != header {
			appendChunk()
		}
		if len([]rune(section)) > 3400 {
			lines := strings.Split(section, "\n")
			for _, line := range lines {
				if len([]rune(current))+len([]rune(line))+1 > 3600 && current != header {
					appendChunk()
				}
				current += line + "\n"
			}
			continue
		}
		current += section + "\n"
	}
	if current != header || len(messages) == 0 {
		messages = append(messages, current)
	}
	if len(messages) > 1 {
		for i := range messages {
			messages[i] += fmt.Sprintf("\n<code>第 %d/%d 段</code>", i+1, len(messages))
		}
	}
	return messages
}

func (t *TelegramService) renderDailyScheduleGroup(g *dailyScheduleGroup) string {
	name := g.Name
	if strings.TrimSpace(name) == "" {
		name = g.Code
	}
	timePart := ""
	if g.Start != "" || g.End != "" {
		timePart = fmt.Sprintf("  <code>%s - %s</code>", html.EscapeString(g.Start), html.EscapeString(g.End))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n<b>【%s】</b>%s  <code>%d人</code>\n", html.EscapeString(name), timePart, len(g.Items)))
	for _, st := range g.Items {
		tg := "未绑"
		if st.TelegramUserID > 0 {
			tg = "TG"
		}
		phone := ""
		if strings.TrimSpace(st.StaffPhone) != "" {
			phone = " ｜ ☎ " + html.EscapeString(strings.TrimSpace(st.StaffPhone))
		}
		b.WriteString(fmt.Sprintf("• <b>%s</b>%s ｜ %s ｜ %s ｜ %s\n", html.EscapeString(st.StaffName), phone, html.EscapeString(st.NotifyStatusLabel), html.EscapeString(st.ReadStatusLabel), html.EscapeString(tg)))
	}
	return b.String()
}

func scheduleStatusRank(st ScheduleItemStatus) int {
	return shiftGroupRank(st.ShiftCode, st.ShiftName, st.ShiftShortName)
}

func shiftGroupRank(code, name, short string) int {
	c := strings.ToLower(strings.TrimSpace(code))
	joined := name + short
	switch {
	case c == "morning" || strings.Contains(joined, "早"):
		return 10
	case c == "middle" || strings.Contains(joined, "中"):
		return 20
	case c == "night" || strings.Contains(joined, "晚") || strings.Contains(joined, "夜"):
		return 30
	case c == "normal" || strings.Contains(joined, "正常"):
		return 40
	case c == "rest" || strings.Contains(joined, "休"):
		return 50
	case c == "annual_leave" || strings.Contains(joined, "年"):
		return 60
	case c == "sick_leave" || strings.Contains(joined, "病"):
		return 70
	default:
		return 99
	}
}

func (t *TelegramService) startScheduler() {
	// Notification reminders are task-queue based.  The scheduler consumes due
	// tasks every 300 seconds while today's queue still has pending work.  Once
	// all tasks for the current local day are sent/cancelled, it sleeps until the
	// next day to avoid repeated full scans.  Schedule/shift/user changes call
	// WakeNotificationQueue, so a same-day update can wake the scheduler early and
	// create/consume newly added tasks.
	if t.QueueWake == nil {
		t.QueueWake = make(chan struct{}, 1)
	}

	for {
		t.processNotificationQueue()

		complete, total, open, err := t.Store.TodayNotificationTasksComplete(t.Loc)
		wait := 300 * time.Second
		if err != nil {
			log.Printf("check today notification queue completion failed: %v", err)
		} else if complete {
			wait = durationUntilNextLocalDay(t.Loc)
			log.Printf("today notification queue complete: total=%d open=%d, sleeping until next local day or queue wake", total, open)
		}

		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-t.QueueWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
}

func (t *TelegramService) WakeNotificationQueue() {
	if t == nil || t.QueueWake == nil {
		return
	}
	select {
	case t.QueueWake <- struct{}{}:
	default:
	}
}

func durationUntilNextLocalDay(loc *time.Location) time.Duration {
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 5, 0, loc)
	return time.Until(next)
}

func (t *TelegramService) processNotificationQueue() {
	if t == nil || t.Store == nil {
		return
	}
	active, err := t.Store.LoadActive()
	if err != nil {
		log.Printf("load active for notification queue failed: %v", err)
		return
	}
	created, cancelled, refreshed, err := t.Store.SyncNotificationTasks(active, t.Cfg.GroupChatIDs, t.Loc)
	if err != nil {
		log.Printf("sync notification tasks failed: %v", err)
		return
	}
	if created > 0 || cancelled > 0 || refreshed > 0 {
		log.Printf("notification queue synced: created=%d refreshed=%d cancelled=%d", created, refreshed, cancelled)
	}

	now := time.Now().In(t.Loc)
	tasks, err := t.Store.ClaimDueNotificationTasks(now, 100)
	if err != nil {
		log.Printf("claim due notification tasks failed: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	log.Printf("notification queue consuming %d due tasks", len(tasks))
	for _, task := range tasks {
		if t.Store.NotificationAlreadySent(task.Key) {
			record := t.notificationRecordFromTask(task)
			if record.SentAt == "" {
				record.SentAt = time.Now().Format(time.RFC3339)
			}
			if err := t.Store.CompleteNotificationTaskSent(task.ID, record); err != nil {
				log.Printf("mark duplicate notification task sent failed task=%s: %v", task.ID, err)
			}
			continue
		}
		if err := t.sendReminderTask(task); err != nil {
			log.Printf("send notification task failed task=%s staff=%s shift=%s attempt=%d err=%v", task.ID, task.Item.StaffName, task.Item.ShiftName, task.Attempts, err)
			_ = t.Store.FailNotificationTask(task.ID, err, time.Now().Add(300*time.Second))
			continue
		}
		record := t.notificationRecordFromTask(task)
		record.SentAt = time.Now().Format(time.RFC3339)
		if err := t.Store.CompleteNotificationTaskSent(task.ID, record); err != nil {
			log.Printf("complete notification task failed task=%s: %v", task.ID, err)
			_ = t.Store.FailNotificationTask(task.ID, err, time.Now().Add(300*time.Second))
		}
	}
}

func (t *TelegramService) notificationRecordFromTask(task NotificationTask) NotificationRecord {
	item := task.Item
	return NotificationRecord{
		ID:             hashID("ntf", task.Key),
		Key:            task.Key,
		ItemKey:        notificationItemKey(item),
		Date:           item.Date,
		StaffID:        item.StaffID,
		StaffName:      item.StaffName,
		StaffPhone:     strings.TrimSpace(item.StaffPhone),
		ShiftCode:      item.ShiftCode,
		ShiftName:      item.ShiftName,
		TelegramUserID: item.TelegramUserID,
		ChatID:         task.ChatID,
	}
}

func (t *TelegramService) sendReminderTask(task NotificationTask) error {
	item := task.Item
	if !shiftNeedsNotification(item) {
		return fmt.Errorf("班次无需通知: %s", item.ShiftCode)
	}
	mention := html.EscapeString(item.StaffName)
	if item.TelegramUserID > 0 {
		mention = fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", item.TelegramUserID, html.EscapeString(item.StaffName))
	}
	body := fmt.Sprintf("⏰ <b>工作提醒</b>\n\n员工: %s\n日期: %s\n班次: %s\n时间: %s", mention, html.EscapeString(dateWithWeekday(item.Date, t.Loc)), html.EscapeString(item.ShiftName), html.EscapeString(formatClock(item.StartTime)))
	if strings.TrimSpace(item.StaffPhone) != "" {
		body += fmt.Sprintf("\n电话: %s", html.EscapeString(strings.TrimSpace(item.StaffPhone)))
	}
	if strings.TrimSpace(t.Cfg.WorkOrderURL) != "" {
		body += fmt.Sprintf("\n\n排班工单: %s", html.EscapeString(strings.TrimSpace(t.Cfg.WorkOrderURL)))
	}
	footer := t.reminderFooter(item)
	body += "\n\n" + footer
	recordID := hashID("ntf", task.Key)
	replyMarkup := map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "我已读", "callback_data": "read:" + recordID}}}}
	return t.sendMessageWithMarkup(task.ChatID, t.getThreadID(task.ChatID, 0), body, "HTML", replyMarkup)
}

func (t *TelegramService) reminderFooter(item ScheduleItem) string {
	base := "请注意交接。"
	if strings.TrimSpace(t.Cfg.WorkOrderURL) != "" {
		base = "请注意交接, 如需查阅变更请使用工单。"
	}
	start, err := time.Parse(time.RFC3339, item.StartTime)
	if err != nil {
		return "还有 30 分钟开始值班，" + base
	}
	remaining := start.In(t.Loc).Sub(time.Now().In(t.Loc))
	if remaining <= 0 {
		return "班次已到开始时间，" + base
	}
	minutesLeft := int(remaining.Minutes())
	if minutesLeft < 1 {
		minutesLeft = 1
	}
	return fmt.Sprintf("还有 %d 分钟开始值班，%s", minutesLeft, base)
}

func (t *TelegramService) getThreadID(chatID int64, msgThreadID int) int {
	if msgThreadID > 0 {
		return msgThreadID
	}
	if v, ok := t.Cfg.GroupTopicMap[chatID]; ok {
		return v
	}
	return 0
}

func (t *TelegramService) sendMessage(chatID int64, threadID int, text string, parseMode string) error {
	return t.sendMessageWithMarkup(chatID, threadID, text, parseMode, nil)
}

func (t *TelegramService) sendMessageWithMarkup(chatID int64, threadID int, text string, parseMode string, replyMarkup any) error {
	vals := url.Values{}
	vals.Set("chat_id", strconv.FormatInt(chatID, 10))
	vals.Set("text", text)
	if parseMode != "" {
		vals.Set("parse_mode", parseMode)
	}
	if threadID > 0 {
		vals.Set("message_thread_id", strconv.Itoa(threadID))
	}
	if replyMarkup != nil {
		rm, _ := json.Marshal(replyMarkup)
		vals.Set("reply_markup", string(rm))
	}
	var resp telegramAPIResponse[json.RawMessage]
	if err := t.postForm("sendMessage", vals, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf(resp.Description)
	}
	return nil
}

func chineseWeekdayName(w time.Weekday) string {
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

func dateWithWeekday(date string, loc *time.Location) string {
	date = strings.TrimSpace(date)
	if loc == nil {
		loc = time.Local
	}
	if d, err := time.ParseInLocation("2006-01-02", date, loc); err == nil {
		return fmt.Sprintf("%s %s", d.Format("2006-01-02"), chineseWeekdayName(d.Weekday()))
	}
	if ts, err := time.Parse(time.RFC3339, date); err == nil {
		t := ts.In(loc)
		return fmt.Sprintf("%s %s", t.Format("2006-01-02"), chineseWeekdayName(t.Weekday()))
	}
	return date
}

func dateTimeWithWeekday(rfc string, loc *time.Location) string {
	rfc = strings.TrimSpace(rfc)
	if rfc == "" {
		return "-"
	}
	if loc == nil {
		loc = time.Local
	}
	if ts, err := time.Parse(time.RFC3339, rfc); err == nil {
		t := ts.In(loc)
		return fmt.Sprintf("%s %s %s", t.Format("2006-01-02"), chineseWeekdayName(t.Weekday()), t.Format("15:04"))
	}
	return rfc
}

func formatClock(rfc string) string {
	if ts, err := time.Parse(time.RFC3339, rfc); err == nil {
		return ts.Format("15:04")
	}
	return rfc
}
