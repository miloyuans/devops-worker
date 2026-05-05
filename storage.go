package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Storage struct {
	Dir string
}

func NewStorage(dir string) *Storage {
	return &Storage{Dir: dir}
}

func (s *Storage) Init() error {
	dirs := []string{
		"config", "users", "schedules", "schedules/by_day", "approvals/pending", "approvals/approved",
		"approvals/rejected", "previews", "history", "locks", "meta",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(s.Dir, d), 0755); err != nil {
			return err
		}
	}
	if err := s.ensureDefaultShifts(); err != nil {
		return err
	}
	if err := s.ensureDefaultUsers(); err != nil {
		return err
	}
	if err := s.ensureDefaultActive(); err != nil {
		return err
	}
	if err := s.ensureDefaultReminderState(); err != nil {
		return err
	}
	if err := s.ensureDefaultNotificationState(); err != nil {
		return err
	}
	return nil
}

func defaultShifts() []Shift {
	return []Shift{
		{Code: "morning", Name: "早班", ShortName: "早", Start: "09:00", End: "18:00", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("09:00", "18:00"), Enabled: true, CreatedBy: "system"},
		{Code: "middle", Name: "中班", ShortName: "中", Start: "15:00", End: "24:00", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("15:00", "24:00"), Enabled: true, CreatedBy: "system"},
		{Code: "night", Name: "晚班", ShortName: "晚", Start: "00:00", End: "09:00", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("00:00", "09:00"), Enabled: true, CreatedBy: "system"},
		{Code: "normal", Name: "正常班", ShortName: "正常", Start: "09:00", End: "18:00", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("09:00", "18:00"), Enabled: true, CreatedBy: "system"},
		{Code: "rest", Name: "休息", ShortName: "休", Start: "00:00", End: "23:59", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("00:00", "23:59"), Enabled: true, CreatedBy: "system"},
		{Code: "annual_leave", Name: "年假", ShortName: "年", Start: "00:00", End: "23:59", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("00:00", "23:59"), Enabled: true, CreatedBy: "system"},
		{Code: "sick_leave", Name: "病假", ShortName: "病", Start: "00:00", End: "23:59", Timezone: DefaultShiftTimezone, CrossDay: deriveCrossDay("00:00", "23:59"), Enabled: true, CreatedBy: "system"},
	}
}

func (s *Storage) ensureDefaultShifts() error {
	path := filepath.Join(s.Dir, "config", "shifts.json")
	defaults := defaultShifts()
	if fileExists(path) {
		shifts, err := s.LoadShifts()
		if err != nil {
			return err
		}
		changed := false
		seen := map[string]bool{}
		for i := range shifts {
			seen[shifts[i].Code] = true
			if shifts[i].CreatedBy == "" {
				shifts[i].CreatedBy = "system"
				changed = true
			}
			if shifts[i].Timezone == "" {
				shifts[i].Timezone = DefaultShiftTimezone
				changed = true
			}
			cross := deriveCrossDay(shifts[i].Start, shifts[i].End)
			if shifts[i].CrossDay != cross {
				shifts[i].CrossDay = cross
				changed = true
			}
		}
		for _, def := range defaults {
			if !seen[def.Code] {
				shifts = append(shifts, def)
				changed = true
			}
		}
		if changed {
			return writeJSONAtomic(path, ShiftConfig{Shifts: shifts})
		}
		return nil
	}
	return writeJSONAtomic(path, ShiftConfig{Shifts: defaults})
}

func (s *Storage) ensureDefaultUsers() error {
	path := filepath.Join(s.Dir, "users", "users.json")
	if fileExists(path) {
		var db UserDB
		if err := readJSON(path, &db); err != nil {
			return err
		}
		changed := false
		for i := range db.Users {
			if db.Users[i].CreatedBy == "" {
				db.Users[i].CreatedBy = "admin"
				changed = true
			}
		}
		if changed {
			return writeJSONAtomic(path, db)
		}
		return nil
	}
	return writeJSONAtomic(path, UserDB{Users: []StaffUser{}})
}

func (s *Storage) ensureDefaultActive() error {
	path := filepath.Join(s.Dir, "schedules", "active.json")
	if fileExists(path) {
		var active ActiveSchedule
		if err := readJSON(path, &active); err != nil {
			return err
		}
		if active.VersionID == "" {
			active.VersionID = newVersionID(active.Revision)
			return writeJSONAtomic(path, active)
		}
		return nil
	}
	return writeJSONAtomic(path, ActiveSchedule{Revision: 0, VersionID: newVersionID(0), Items: []ScheduleItem{}})
}

func (s *Storage) ensureDefaultReminderState() error {
	path := filepath.Join(s.Dir, "meta", "reminders.json")
	if fileExists(path) {
		return nil
	}
	return writeJSONAtomic(path, ReminderState{Sent: map[string]bool{}})
}

func (s *Storage) ensureDefaultNotificationState() error {
	path := filepath.Join(s.Dir, "meta", "notifications.json")
	if fileExists(path) {
		return nil
	}
	return writeJSONAtomic(path, NotificationState{Records: []NotificationRecord{}})
}

func (s *Storage) WithLock(fn func() error) error {
	lockPath := filepath.Join(s.Dir, "locks", "storage.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func TryAcquireLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "pid=%d started_at=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	return f, nil
}

func (s *Storage) LoadShifts() ([]Shift, error) {
	var cfg ShiftConfig
	if err := readJSON(filepath.Join(s.Dir, "config", "shifts.json"), &cfg); err != nil {
		return nil, err
	}
	return cfg.Shifts, nil
}

func (s *Storage) SaveShifts(shifts []Shift) error {
	return s.WithLock(func() error {
		return writeJSONAtomic(filepath.Join(s.Dir, "config", "shifts.json"), ShiftConfig{Shifts: shifts})
	})
}

func (s *Storage) LoadUsers() ([]StaffUser, error) {
	var db UserDB
	if err := readJSON(filepath.Join(s.Dir, "users", "users.json"), &db); err != nil {
		return nil, err
	}
	return db.Users, nil
}

func (s *Storage) SaveUsers(users []StaffUser) error {
	return s.WithLock(func() error {
		return writeJSONAtomic(filepath.Join(s.Dir, "users", "users.json"), UserDB{Users: users})
	})
}

func (s *Storage) LoadActive() (ActiveSchedule, error) {
	var active ActiveSchedule
	err := readJSON(filepath.Join(s.Dir, "schedules", "active.json"), &active)
	if err != nil {
		return active, err
	}
	if active.VersionID == "" {
		active.VersionID = newVersionID(active.Revision)
	}
	return active, nil
}

func (s *Storage) SaveActiveLocked(active ActiveSchedule) error {
	return writeJSONAtomic(filepath.Join(s.Dir, "schedules", "active.json"), active)
}

func (s *Storage) CreateApproval(createdBy string, rules []ScheduleRule, previewItems []ScheduleItem, previewHTML string, approvers []int64) (*Approval, error) {
	var approval *Approval
	err := s.WithLock(func() error {
		active, err := s.LoadActive()
		if err != nil {
			return err
		}
		id := newID("apv")
		htmlRel := filepath.Join("previews", id+".html")
		htmlAbs := filepath.Join(s.Dir, htmlRel)
		if err := writeFileAtomic(htmlAbs, []byte(previewHTML)); err != nil {
			return err
		}
		approval = &Approval{
			ID:            id,
			TransactionID: newID("txn"),
			Status:        "pending",
			CreatedBy:     createdBy,
			CreatedAt:     time.Now().Format(time.RFC3339),
			ApproverIDs:   approvers,
			Rules:         rules,
			PreviewItems:  previewItems,
			PreviewHTML:   htmlRel,
			BaseRevision:  active.Revision,
			NewRevision:   active.Revision + 1,
		}
		return writeJSONAtomic(filepath.Join(s.Dir, "approvals", "pending", id+".json"), approval)
	})
	if err != nil {
		return nil, err
	}
	return approval, nil
}

func (s *Storage) AttachApprovalMessageRefs(id string, refs []ApprovalMessageRef) error {
	if len(refs) == 0 {
		return nil
	}
	return s.WithLock(func() error {
		approval, path, err := s.LoadPendingApproval(id)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, ref := range approval.MessageRefs {
			seen[fmt.Sprintf("%d:%d", ref.ChatID, ref.MessageID)] = true
		}
		for _, ref := range refs {
			if ref.ChatID == 0 || ref.MessageID == 0 {
				continue
			}
			key := fmt.Sprintf("%d:%d", ref.ChatID, ref.MessageID)
			if seen[key] {
				continue
			}
			seen[key] = true
			approval.MessageRefs = append(approval.MessageRefs, ref)
		}
		return writeJSONAtomic(path, approval)
	})
}

func (s *Storage) LoadPendingApproval(id string) (Approval, string, error) {
	var approval Approval
	path := filepath.Join(s.Dir, "approvals", "pending", sanitizeFileName(id)+".json")
	if err := readJSON(path, &approval); err != nil {
		return approval, path, err
	}
	return approval, path, nil
}

func (s *Storage) Approve(id string, reviewerID int64, loc *time.Location) (*Approval, error) {
	var updated *Approval
	err := s.WithLock(func() error {
		approval, pendingPath, err := s.LoadPendingApproval(id)
		if err != nil {
			return err
		}
		if approval.Status != "pending" {
			return fmt.Errorf("审批单状态不是 pending")
		}
		if !containsInt64(approval.ApproverIDs, reviewerID) {
			return fmt.Errorf("当前 Telegram 用户无审批权限")
		}
		active, err := s.LoadActive()
		if err != nil {
			return err
		}
		users, err := s.LoadUsers()
		if err != nil {
			return err
		}
		shifts, err := s.LoadShifts()
		if err != nil {
			return err
		}
		updateItems, err := BuildScheduleItems(approval.Rules, users, shifts, loc)
		if err != nil {
			return err
		}
		finalItems := MergeScheduleItems(active.Items, updateItems)
		now := time.Now().Format(time.RFC3339)
		newRevision := active.Revision + 1
		if html, err := RenderPreviewHTML(approval.ID, finalItems, active.Revision, newRevision); err == nil {
			_ = writeFileAtomic(filepath.Join(s.Dir, approval.PreviewHTML), []byte(html))
		}
		newActive := ActiveSchedule{
			Revision:         newRevision,
			VersionID:        newVersionID(newRevision),
			EffectiveAt:      now,
			ApprovedBy:       reviewerID,
			SourceApprovalID: approval.ID,
			Items:            finalItems,
		}
		if err := s.SaveActiveLocked(newActive); err != nil {
			return err
		}
		if err := s.writeByDayAndHistoryLocked(newActive); err != nil {
			return err
		}
		approval.Status = "approved"
		approval.ReviewedBy = reviewerID
		approval.ReviewedAt = now
		approval.NewRevision = newRevision
		approval.PreviewItems = finalItems
		approval.StatusMessage = "审批通过，排班已按最新正式数据合并生效"
		approvedPath := filepath.Join(s.Dir, "approvals", "approved", approval.ID+".json")
		if err := writeJSONAtomic(approvedPath, approval); err != nil {
			return err
		}
		if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		updated = &approval
		return nil
	})
	return updated, err
}

func (s *Storage) Reject(id string, reviewerID int64, reason string) (*Approval, error) {
	var updated *Approval
	err := s.WithLock(func() error {
		approval, pendingPath, err := s.LoadPendingApproval(id)
		if err != nil {
			return err
		}
		if approval.Status != "pending" {
			return fmt.Errorf("审批单状态不是 pending")
		}
		if !containsInt64(approval.ApproverIDs, reviewerID) {
			return fmt.Errorf("当前 Telegram 用户无审批权限")
		}
		now := time.Now().Format(time.RFC3339)
		approval.Status = "rejected"
		approval.ReviewedBy = reviewerID
		approval.ReviewedAt = now
		approval.RejectReason = reason
		approval.StatusMessage = "审批拒绝，排班未生效"
		rejectedPath := filepath.Join(s.Dir, "approvals", "rejected", approval.ID+".json")
		if err := writeJSONAtomic(rejectedPath, approval); err != nil {
			return err
		}
		if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		updated = &approval
		return nil
	})
	return updated, err
}

func (s *Storage) writeByDayAndHistoryLocked(active ActiveSchedule) error {
	byDate := map[string][]ScheduleItem{}
	for _, item := range active.Items {
		byDate[item.Date] = append(byDate[item.Date], item)
	}
	for date, items := range byDate {
		sortScheduleItems(items)
		if err := writeJSONAtomic(filepath.Join(s.Dir, "schedules", "by_day", date+".json"), items); err != nil {
			return err
		}
		t, err := time.Parse("2006-01-02", date)
		if err != nil {
			return err
		}
		histDir := filepath.Join(s.Dir, "history", fmt.Sprintf("%04d", t.Year()), fmt.Sprintf("%02d", int(t.Month())))
		if err := os.MkdirAll(histDir, 0755); err != nil {
			return err
		}
		if err := writeJSONAtomic(filepath.Join(histDir, date+".json"), items); err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) LoadDay(date string) ([]ScheduleItem, error) {
	path := filepath.Join(s.Dir, "schedules", "by_day", sanitizeFileName(date)+".json")
	var items []ScheduleItem
	if err := readJSON(path, &items); err == nil {
		return items, nil
	}
	active, err := s.LoadActive()
	if err != nil {
		return nil, err
	}
	for _, item := range active.Items {
		if item.Date == date {
			items = append(items, item)
		}
	}
	sortScheduleItems(items)
	return items, nil
}

func (s *Storage) LoadHistoryDay(date string) ([]ScheduleItem, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.Dir, "history", fmt.Sprintf("%04d", t.Year()), fmt.Sprintf("%02d", int(t.Month())), date+".json")
	var items []ScheduleItem
	if err := readJSON(path, &items); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ScheduleItem{}, nil
		}
		return nil, err
	}
	sortScheduleItems(items)
	return items, nil
}

func (s *Storage) LoadHistoryMonth(year int, month int) ([]ScheduleItem, error) {
	if month < 1 || month > 12 {
		return []ScheduleItem{}, nil
	}
	dir := filepath.Join(s.Dir, "history", fmt.Sprintf("%04d", year), fmt.Sprintf("%02d", month))
	var items []ScheduleItem
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ScheduleItem{}, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		var dayItems []ScheduleItem
		if err := readJSON(path, &dayItems); err == nil {
			items = append(items, dayItems...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortScheduleItems(items)
	return items, nil
}

func (s *Storage) ListApprovals() ([]Approval, error) {
	var all []Approval
	for _, st := range []string{"pending", "approved", "rejected"} {
		dir := filepath.Join(s.Dir, "approvals", st)
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
				return nil
			}
			var a Approval
			if err := readJSON(path, &a); err == nil {
				all = append(all, a)
			}
			return nil
		})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt > all[j].CreatedAt })
	return all, nil
}

func (s *Storage) MarkReminderIfNeeded(key string) (bool, error) {
	var shouldSend bool
	err := s.WithLock(func() error {
		path := filepath.Join(s.Dir, "meta", "reminders.json")
		state := ReminderState{Sent: map[string]bool{}}
		_ = readJSON(path, &state)
		if state.Sent == nil {
			state.Sent = map[string]bool{}
		}
		if state.Sent[key] {
			shouldSend = false
			return nil
		}
		state.Sent[key] = true
		shouldSend = true
		return writeJSONAtomic(path, state)
	})
	return shouldSend, err
}

func (s *Storage) LoadNotifications() (NotificationState, error) {
	var state NotificationState
	path := filepath.Join(s.Dir, "meta", "notifications.json")
	if err := readJSON(path, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NotificationState{Records: []NotificationRecord{}}, nil
		}
		return state, err
	}
	if state.Records == nil {
		state.Records = []NotificationRecord{}
	}
	return state, nil
}

func (s *Storage) NotificationAlreadySent(key string) bool {
	state, err := s.LoadNotifications()
	if err != nil {
		return false
	}
	for _, rec := range state.Records {
		if rec.Key == key {
			return true
		}
	}
	return false
}

func (s *Storage) RecordNotificationSent(record NotificationRecord) (bool, error) {
	var created bool
	err := s.WithLock(func() error {
		path := filepath.Join(s.Dir, "meta", "notifications.json")
		state := NotificationState{Records: []NotificationRecord{}}
		_ = readJSON(path, &state)
		for _, rec := range state.Records {
			if rec.Key == record.Key {
				created = false
				return nil
			}
		}
		state.Records = append(state.Records, record)
		created = true
		return writeJSONAtomic(path, state)
	})
	return created, err
}

func (s *Storage) MarkNotificationRead(recordID string, readerID int64) (*NotificationRecord, bool, error) {
	var updated *NotificationRecord
	firstRead := false
	err := s.WithLock(func() error {
		path := filepath.Join(s.Dir, "meta", "notifications.json")
		state := NotificationState{Records: []NotificationRecord{}}
		_ = readJSON(path, &state)
		for i := range state.Records {
			if state.Records[i].ID != recordID {
				continue
			}
			if state.Records[i].TelegramUserID > 0 && state.Records[i].TelegramUserID != readerID {
				return fmt.Errorf("当前 Telegram 用户不是该排班人员")
			}
			if state.Records[i].ReadAt == "" {
				state.Records[i].ReadAt = time.Now().Format(time.RFC3339)
				state.Records[i].ReadBy = readerID
				firstRead = true
			}
			updated = &state.Records[i]
			return writeJSONAtomic(path, state)
		}
		return fmt.Errorf("通知记录不存在或已过期")
	})
	return updated, firstRead, err
}

func (s *Storage) BuildScheduleItemStatuses(items []ScheduleItem, locs ...*time.Location) []ScheduleItemStatus {
	loc := time.Local
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
	state, _ := s.LoadNotifications()
	byItem := map[string][]NotificationRecord{}
	for _, rec := range state.Records {
		byItem[rec.ItemKey] = append(byItem[rec.ItemKey], rec)
	}
	out := make([]ScheduleItemStatus, 0, len(items))
	for _, item := range items {
		status := ScheduleItemStatus{ScheduleItem: item, StartClock: clockInLocation(item.StartTime, loc), EndClock: clockInLocation(item.EndTime, loc), NotifyStatus: "off", NotifyStatusLabel: "未通知", ReadStatus: "off", ReadStatusLabel: "未读"}
		if !shiftNeedsNotification(item) {
			status.NotifyStatus = "muted"
			status.NotifyStatusLabel = "无需通知"
			status.ReadStatus = "muted"
			status.ReadStatusLabel = "无需确认"
		}
		recs := byItem[notificationItemKey(item)]
		if shiftNeedsNotification(item) {
			if len(recs) > 0 {
				status.NotifyStatus = "ok"
				status.NotifyStatusLabel = "已通知"
			}
			for _, rec := range recs {
				if rec.ReadAt != "" {
					status.ReadStatus = "ok"
					status.ReadStatusLabel = "已读"
					break
				}
			}
		}
		out = append(out, status)
	}
	return out
}

func (s *Storage) NotificationTriggeredForItem(item ScheduleItem) bool {
	state, err := s.LoadNotifications()
	if err != nil {
		return false
	}
	for _, rec := range state.Records {
		if rec.Date == item.Date && rec.StaffID == item.StaffID && rec.ShiftCode == item.ShiftCode {
			return true
		}
	}
	return false
}

func (s *Storage) UpdateFutureItemsForShift(shift Shift, loc *time.Location) (ShiftUpdateSummary, error) {
	var summary ShiftUpdateSummary
	err := s.WithLock(func() error {
		active, err := s.LoadActive()
		if err != nil {
			return err
		}
		now := time.Now().In(loc)
		changed := 0
		for i := range active.Items {
			item := active.Items[i]
			if item.ShiftCode != shift.Code {
				continue
			}
			oldStart, err := time.Parse(time.RFC3339, item.StartTime)
			if err != nil {
				continue
			}
			if oldStart.In(loc).Before(now) || s.NotificationTriggeredForItem(item) {
				continue
			}
			date, err := time.ParseInLocation("2006-01-02", item.Date, loc)
			if err != nil {
				continue
			}
			start, end, err := makeShiftTime(date, shift, loc)
			if err != nil {
				return err
			}
			active.Items[i].ShiftName = shift.Name
			active.Items[i].ShiftShortName = shift.ShortName
			active.Items[i].StartTime = start.Format(time.RFC3339)
			active.Items[i].EndTime = end.Format(time.RFC3339)
			changed++
		}
		if changed == 0 {
			summary = ShiftUpdateSummary{ChangedItems: 0, NewRevision: active.Revision, VersionID: active.VersionID}
			return nil
		}
		active.Revision++
		active.VersionID = newVersionID(active.Revision)
		active.EffectiveAt = time.Now().Format(time.RFC3339)
		active.SourceApprovalID = "shift_update"
		if err := s.SaveActiveLocked(active); err != nil {
			return err
		}
		if err := s.writeByDayAndHistoryLocked(active); err != nil {
			return err
		}
		summary = ShiftUpdateSummary{ChangedItems: changed, NewRevision: active.Revision, VersionID: active.VersionID}
		return nil
	})
	return summary, err
}

func (s *Storage) SyncActiveItemsWithLatestShifts(loc *time.Location) (ShiftUpdateSummary, error) {
	var summary ShiftUpdateSummary
	err := s.WithLock(func() error {
		active, err := s.LoadActive()
		if err != nil {
			return err
		}
		shifts, err := s.LoadShifts()
		if err != nil {
			return err
		}
		shiftMap := map[string]Shift{}
		for _, sh := range shifts {
			if sh.Code != "" {
				shiftMap[sh.Code] = sh
			}
		}
		now := time.Now()
		changed := 0
		for i := range active.Items {
			item := active.Items[i]
			shift, ok := shiftMap[item.ShiftCode]
			if !ok {
				continue
			}
			if s.NotificationTriggeredForItem(item) {
				continue
			}
			shiftLoc := loc
			if strings.TrimSpace(shift.Timezone) != "" {
				if loaded, err := time.LoadLocation(strings.TrimSpace(shift.Timezone)); err == nil {
					shiftLoc = loaded
				}
			}
			date, err := time.ParseInLocation("2006-01-02", item.Date, shiftLoc)
			if err != nil {
				continue
			}
			// Only rewrite today/future items. The decision is based on the
			// schedule date, not the stored historical start_time, so stale start
			// clocks cannot cause missed future reminders.
			today := time.Date(now.In(shiftLoc).Year(), now.In(shiftLoc).Month(), now.In(shiftLoc).Day(), 0, 0, 0, 0, shiftLoc)
			if date.Before(today) {
				continue
			}
			start, end, err := makeShiftTime(date, shift, loc)
			if err != nil {
				return err
			}
			newStart := start.Format(time.RFC3339)
			newEnd := end.Format(time.RFC3339)
			if active.Items[i].ShiftName == shift.Name && active.Items[i].ShiftShortName == shift.ShortName && active.Items[i].StartTime == newStart && active.Items[i].EndTime == newEnd {
				continue
			}
			active.Items[i].ShiftName = shift.Name
			active.Items[i].ShiftShortName = shift.ShortName
			active.Items[i].StartTime = newStart
			active.Items[i].EndTime = newEnd
			changed++
		}
		if changed == 0 {
			summary = ShiftUpdateSummary{ChangedItems: 0, NewRevision: active.Revision, VersionID: active.VersionID}
			return nil
		}
		active.Revision++
		active.VersionID = newVersionID(active.Revision)
		active.EffectiveAt = time.Now().Format(time.RFC3339)
		active.SourceApprovalID = "auto_shift_time_sync"
		if err := s.SaveActiveLocked(active); err != nil {
			return err
		}
		if err := s.writeByDayAndHistoryLocked(active); err != nil {
			return err
		}
		summary = ShiftUpdateSummary{ChangedItems: changed, NewRevision: active.Revision, VersionID: active.VersionID}
		return nil
	})
	return summary, err
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
