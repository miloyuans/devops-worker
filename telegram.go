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
	caption := fmt.Sprintf("📋 <b>排班策略审批请求</b>\n\n审批ID: <code>%s</code>\n事务ID: <code>%s</code>\n提交人: %s\n变更记录: %d\n审批人: %s\n\n请打开 HTML 附件查看预览。任一审批人点击同意或拒绝后，所有审批窗口会同步更新状态。", html.EscapeString(a.ID), html.EscapeString(a.TransactionID), html.EscapeString(a.CreatedBy), len(a.Rules), approverText)
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
	statusLine := "审批已结束"
	if a.Status == "approved" {
		statusLine = "✅ " + reviewer + " 审批通过，排班已按最新正式数据合并生效"
	} else if a.Status == "rejected" {
		statusLine = "❌ " + reviewer + " 审批拒绝，本次策略未生效"
	}
	caption := fmt.Sprintf("📋 <b>排班策略审批结果</b>\n\n审批ID: <code>%s</code>\n事务ID: <code>%s</code>\n状态: %s\n处理时间: %s\n\n所有审批窗口已同步更新。", html.EscapeString(a.ID), html.EscapeString(a.TransactionID), html.EscapeString(statusLine), html.EscapeString(a.ReviewedAt))
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
		t.replyToCallbackMessage(cq, fmt.Sprintf("✅ 审批已通过并生效\n审批ID: %s\n审批人: %s", approval.ID, t.telegramDisplayName(reviewerID)))
	case "reject":
		approval, err := t.Store.Reject(approvalID, reviewerID, "telegram callback rejected")
		if err != nil {
			t.answerCallback(cq.ID, "拒绝失败: "+err.Error(), true)
			return
		}
		t.answerCallback(cq.ID, "已拒绝，本次策略未生效", true)
		t.editApprovalMessages(approval)
		t.replyToCallbackMessage(cq, fmt.Sprintf("❌ 审批已拒绝，策略未生效\n审批ID: %s\n审批人: %s", approval.ID, t.telegramDisplayName(reviewerID)))
	case "noop":
		t.answerCallback(cq.ID, "该审批已经结束", false)
	case "read":
		rec, err := t.Store.MarkNotificationRead(approvalID, reviewerID)
		if err != nil {
			t.answerCallback(cq.ID, "确认失败: "+err.Error(), true)
			return
		}
		t.answerCallback(cq.ID, "已记录已读", false)
		t.replyToCallbackMessage(cq, fmt.Sprintf("✅ 已读确认\n员工: %s\n日期: %s\n班次: %s", rec.StaffName, rec.Date, rec.ShiftName))
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
	if len(items) == 0 {
		_ = t.sendMessage(msg.Chat.ID, threadID, fmt.Sprintf("📅 %s\n\n今天没有排班。", today), "")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📅 <b>%s 今日排班</b>\n\n", html.EscapeString(today)))
	for _, item := range items {
		b.WriteString(fmt.Sprintf("• %s %s %s-%s\n", html.EscapeString(item.StaffName), html.EscapeString(item.ShiftName), html.EscapeString(formatClock(item.StartTime)), html.EscapeString(formatClock(item.EndTime))))
	}
	_ = t.sendMessage(msg.Chat.ID, threadID, b.String(), "HTML")
}

func (t *TelegramService) startScheduler() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		t.checkAndNotify()
	}
}

func (t *TelegramService) checkAndNotify() {
	active, err := t.Store.LoadActive()
	if err != nil {
		log.Printf("load active for reminder failed: %v", err)
		return
	}
	target := time.Now().In(t.Loc).Add(30 * time.Minute).Format("2006-01-02 15:04")
	for _, item := range active.Items {
		start, err := time.Parse(time.RFC3339, item.StartTime)
		if err != nil || start.In(t.Loc).Format("2006-01-02 15:04") != target {
			continue
		}
		for _, chatID := range t.Cfg.GroupChatIDs {
			key := notificationKey(item, chatID)
			if t.Store.NotificationAlreadySent(key) {
				continue
			}
			record := NotificationRecord{
				ID:             hashID("ntf", key),
				Key:            key,
				ItemKey:        notificationItemKey(item),
				Date:           item.Date,
				StaffID:        item.StaffID,
				StaffName:      item.StaffName,
				ShiftCode:      item.ShiftCode,
				ShiftName:      item.ShiftName,
				TelegramUserID: item.TelegramUserID,
				ChatID:         chatID,
				SentAt:         time.Now().Format(time.RFC3339),
			}
			mention := html.EscapeString(item.StaffName)
			if item.TelegramUserID > 0 {
				mention = fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", item.TelegramUserID, html.EscapeString(item.StaffName))
			}
			body := fmt.Sprintf("⏰ <b>上班提醒</b>\n\n员工: %s\n班次: %s\n时间: %s\n\n还有 30 分钟开始值班，请注意交接。", mention, html.EscapeString(item.ShiftName), html.EscapeString(formatClock(item.StartTime)))
			replyMarkup := map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "我已读", "callback_data": "read:" + record.ID}}}}
			if err := t.sendMessageWithMarkup(chatID, t.getThreadID(chatID, 0), body, "HTML", replyMarkup); err != nil {
				log.Printf("send reminder failed: %v", err)
				continue
			}
			if _, err := t.Store.RecordNotificationSent(record); err != nil {
				log.Printf("record notification failed: %v", err)
			}
		}
	}
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

func formatClock(rfc string) string {
	if ts, err := time.Parse(time.RFC3339, rfc); err == nil {
		return ts.Format("15:04")
	}
	return rfc
}
