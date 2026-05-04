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
	return nil
}

func (s *Storage) ensureDefaultShifts() error {
	path := filepath.Join(s.Dir, "config", "shifts.json")
	if fileExists(path) {
		return nil
	}
	cfg := ShiftConfig{Shifts: []Shift{
		{Code: "morning", Name: "早班", ShortName: "早", Start: "09:00", End: "18:00"},
		{Code: "middle", Name: "中班", ShortName: "中", Start: "15:00", End: "24:00"},
		{Code: "night", Name: "晚班", ShortName: "晚", Start: "00:00", End: "09:00"},
		{Code: "normal", Name: "正常班", ShortName: "正常", Start: "09:00", End: "18:00"},
	}}
	return writeJSONAtomic(path, cfg)
}

func (s *Storage) ensureDefaultUsers() error {
	path := filepath.Join(s.Dir, "users", "users.json")
	if fileExists(path) {
		return nil
	}
	return writeJSONAtomic(path, UserDB{Users: []StaffUser{}})
}

func (s *Storage) ensureDefaultActive() error {
	path := filepath.Join(s.Dir, "schedules", "active.json")
	if fileExists(path) {
		return nil
	}
	return writeJSONAtomic(path, ActiveSchedule{Revision: 0, Items: []ScheduleItem{}})
}

func (s *Storage) ensureDefaultReminderState() error {
	path := filepath.Join(s.Dir, "meta", "reminders.json")
	if fileExists(path) {
		return nil
	}
	return writeJSONAtomic(path, ReminderState{Sent: map[string]bool{}})
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
	return active, err
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
			ID:           id,
			Status:       "pending",
			CreatedBy:    createdBy,
			CreatedAt:    time.Now().Format(time.RFC3339),
			ApproverIDs:  approvers,
			Rules:        rules,
			PreviewItems: previewItems,
			PreviewHTML:  htmlRel,
			BaseRevision: active.Revision,
			NewRevision:  active.Revision + 1,
		}
		return writeJSONAtomic(filepath.Join(s.Dir, "approvals", "pending", id+".json"), approval)
	})
	if err != nil {
		return nil, err
	}
	return approval, nil
}

func (s *Storage) LoadPendingApproval(id string) (Approval, string, error) {
	var approval Approval
	path := filepath.Join(s.Dir, "approvals", "pending", sanitizeFileName(id)+".json")
	if err := readJSON(path, &approval); err != nil {
		return approval, path, err
	}
	return approval, path, nil
}

func (s *Storage) Approve(id string, reviewerID int64) (*Approval, error) {
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
		if active.Revision != approval.BaseRevision {
			return fmt.Errorf("正式排班版本已从 %d 变为 %d，请重新提交审批", approval.BaseRevision, active.Revision)
		}
		now := time.Now().Format(time.RFC3339)
		newActive := ActiveSchedule{
			Revision:         approval.NewRevision,
			EffectiveAt:      now,
			ApprovedBy:       reviewerID,
			SourceApprovalID: approval.ID,
			Items:            approval.PreviewItems,
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
		approval.StatusMessage = "审批通过，排班已生效"
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
