package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultShiftTimezone = "Asia/Dubai"

func BuildScheduleItems(rules []ScheduleRule, users []StaffUser, shifts []Shift, loc *time.Location) ([]ScheduleItem, error) {
	userMap := map[string]StaffUser{}
	for _, u := range users {
		if u.Enabled {
			userMap[u.ID] = u
		}
	}
	shiftMap := map[string]Shift{}
	for _, sh := range shifts {
		if sh.Enabled || sh.Code != "" {
			shiftMap[sh.Code] = sh
		}
	}
	byKey := map[string]ScheduleItem{}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		shift, ok := shiftMap[rule.ShiftCode]
		if !ok {
			return nil, fmt.Errorf("未知班次: %s", rule.ShiftCode)
		}
		if rule.Year < 2000 || rule.Month < 1 || rule.Month > 12 {
			return nil, fmt.Errorf("年月参数无效")
		}

		dates, err := expandRuleDates(rule, loc)
		if err != nil {
			return nil, err
		}
		for _, date := range dates {
			start, end, err := makeShiftTime(date, shift, loc)
			if err != nil {
				return nil, err
			}
			for _, staffID := range rule.StaffIDs {
				staff, ok := userMap[staffID]
				if !ok {
					return nil, fmt.Errorf("未知或已禁用用户: %s", staffID)
				}
				item := ScheduleItem{
					Date:           date.Format("2006-01-02"),
					StaffID:        staff.ID,
					StaffName:      staff.Name,
					TelegramUserID: staff.TelegramUserID,
					ShiftCode:      shift.Code,
					ShiftName:      shift.Name,
					ShiftShortName: shift.ShortName,
					StartTime:      start.Format(time.RFC3339),
					EndTime:        end.Format(time.RFC3339),
				}
				byKey[item.Date+"|"+item.StaffID] = item
			}
		}
	}
	items := make([]ScheduleItem, 0, len(byKey))
	for _, v := range byKey {
		items = append(items, v)
	}
	sortScheduleItems(items)
	return items, nil
}

func expandRuleDates(rule ScheduleRule, loc *time.Location) ([]time.Time, error) {
	seen := map[string]bool{}
	var dates []time.Time
	if len(rule.Dates) > 0 {
		for _, raw := range rule.Dates {
			date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), loc)
			if err != nil {
				return nil, fmt.Errorf("日期格式无效 %q: %w", raw, err)
			}
			if date.Year() != rule.Year || int(date.Month()) != rule.Month {
				return nil, fmt.Errorf("日期 %s 不属于 %04d-%02d", raw, rule.Year, rule.Month)
			}
			key := date.Format("2006-01-02")
			if !seen[key] {
				seen[key] = true
				dates = append(dates, date)
			}
		}
		sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
		return dates, nil
	}

	weekSet := intSet(rule.WeekNums)
	weekdaySet := intSet(rule.Weekdays)
	days := daysInMonth(rule.Year, time.Month(rule.Month))
	for day := 1; day <= days; day++ {
		weekNum := ((day - 1) / 7) + 1
		date := time.Date(rule.Year, time.Month(rule.Month), day, 0, 0, 0, 0, loc)
		weekday := goWeekdayToCN(date.Weekday())
		if weekSet[weekNum] && weekdaySet[weekday] {
			dates = append(dates, date)
		}
	}
	return dates, nil
}

func MergeScheduleItems(existing []ScheduleItem, updates []ScheduleItem) []ScheduleItem {
	updateKeys := map[string]bool{}
	for _, item := range updates {
		updateKeys[item.Date+"|"+item.StaffID] = true
	}
	merged := make([]ScheduleItem, 0, len(existing)+len(updates))
	for _, item := range existing {
		if !updateKeys[item.Date+"|"+item.StaffID] {
			merged = append(merged, item)
		}
	}
	merged = append(merged, updates...)
	sortScheduleItems(merged)
	return merged
}

func NormalizeDraftChanges(changes []ScheduleDraftChange) []ScheduleDraftChange {
	latest := map[string]string{}
	dateSet := map[string]bool{}
	staffSet := map[string]bool{}
	for _, ch := range changes {
		shift := strings.TrimSpace(ch.ShiftCode)
		if shift == "" {
			continue
		}
		for _, rawDate := range ch.Dates {
			date := strings.TrimSpace(rawDate)
			if date == "" {
				continue
			}
			for _, rawStaff := range ch.StaffIDs {
				staff := strings.TrimSpace(rawStaff)
				if staff == "" {
					continue
				}
				key := date + "|" + staff
				latest[key] = shift
				dateSet[date] = true
				staffSet[staff] = true
			}
		}
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ScheduleDraftChange, 0, len(keys))
	for _, key := range keys {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		out = append(out, ScheduleDraftChange{Dates: []string{parts[0]}, StaffIDs: []string{parts[1]}, ShiftCode: latest[key]})
	}
	_ = dateSet
	_ = staffSet
	return out
}

func DraftChangesToRules(changes []ScheduleDraftChange, year int, month int) []ScheduleRule {
	changes = NormalizeDraftChanges(changes)
	rules := make([]ScheduleRule, 0, len(changes))
	for _, ch := range changes {
		if len(ch.Dates) == 0 || len(ch.StaffIDs) == 0 || strings.TrimSpace(ch.ShiftCode) == "" {
			continue
		}
		rules = append(rules, ScheduleRule{ID: newID("rule"), Year: year, Month: month, Dates: ch.Dates, StaffIDs: ch.StaffIDs, ShiftCode: strings.TrimSpace(ch.ShiftCode), Enabled: true})
	}
	return rules
}

func makeShiftTime(date time.Time, shift Shift, loc *time.Location) (time.Time, time.Time, error) {
	startHour, startMin, err := parseClock(shift.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("班次 %s 开始时间错误: %w", shift.Name, err)
	}
	endHour, endMin, err := parseClock(shift.End)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("班次 %s 结束时间错误: %w", shift.Name, err)
	}
	shiftLoc := loc
	if strings.TrimSpace(shift.Timezone) != "" {
		loaded, err := time.LoadLocation(strings.TrimSpace(shift.Timezone))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("班次 %s 时区错误: %w", shift.Name, err)
		}
		shiftLoc = loaded
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), startHour, startMin, 0, 0, shiftLoc)
	var end time.Time
	if strings.TrimSpace(shift.End) == "24:00" {
		end = time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, shiftLoc)
	} else {
		end = time.Date(date.Year(), date.Month(), date.Day(), endHour, endMin, 0, 0, shiftLoc)
	}
	if !end.After(start) {
		end = end.Add(24 * time.Hour)
	}
	return start, end, nil
}

func deriveCrossDay(start, end string) bool {
	if strings.TrimSpace(end) == "24:00" {
		return true
	}
	sh, sm, err1 := parseClock(start)
	eh, em, err2 := parseClock(end)
	if err1 != nil || err2 != nil {
		return false
	}
	return eh*60+em <= sh*60+sm
}

func normalizeShift(sh Shift) (Shift, error) {
	sh.Code = sanitizeFileName(strings.TrimSpace(sh.Code))
	sh.Name = strings.TrimSpace(sh.Name)
	sh.ShortName = strings.TrimSpace(sh.ShortName)
	sh.Start = strings.TrimSpace(sh.Start)
	sh.End = strings.TrimSpace(sh.End)
	sh.Timezone = strings.TrimSpace(sh.Timezone)
	if sh.Timezone == "" {
		sh.Timezone = DefaultShiftTimezone
	}
	if sh.Code == "" || sh.Name == "" || sh.ShortName == "" || sh.Start == "" || sh.End == "" {
		return sh, fmt.Errorf("班次编码、名称、简称、开始和结束时间都不能为空")
	}
	loc, err := time.LoadLocation(sh.Timezone)
	if err != nil {
		return sh, fmt.Errorf("时区无效: %s", sh.Timezone)
	}
	if _, _, err := makeShiftTime(time.Now().In(loc), sh, loc); err != nil {
		return sh, err
	}
	sh.CrossDay = deriveCrossDay(sh.Start, sh.End)
	return sh, nil
}

func parseClock(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("时间格式应为 HH:MM")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return 0, 0, fmt.Errorf("时间超出范围")
	}
	if h == 24 {
		h = 0
	}
	return h, m, nil
}

func sortScheduleItems(items []ScheduleItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date < items[j].Date
		}
		if items[i].StartTime != items[j].StartTime {
			return items[i].StartTime < items[j].StartTime
		}
		return items[i].StaffName < items[j].StaffName
	})
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.Local).Day()
}

func goWeekdayToCN(w time.Weekday) int {
	if w == time.Sunday {
		return 7
	}
	return int(w)
}

func intSet(vals []int) map[int]bool {
	m := map[int]bool{}
	for _, v := range vals {
		m[v] = true
	}
	return m
}

func newID(prefix string) string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s_%s_%s", prefix, time.Now().Format("20060102_150405"), hex.EncodeToString(buf))
}

func newVersionID(revision int) string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("version-%d-%s", revision, hex.EncodeToString(buf))
}

func compactID(s string) string {
	if len(s) <= 14 {
		return s
	}
	return s[:10] + "..." + s[len(s)-4:]
}

func containsInt64(vals []int64, target int64) bool {
	for _, v := range vals {
		if v == target {
			return true
		}
	}
	return false
}

func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func notificationItemKey(item ScheduleItem) string {
	return item.Date + "|" + item.StaffID + "|" + item.ShiftCode + "|" + item.StartTime
}

func notificationKey(item ScheduleItem, chatID int64) string {
	return fmt.Sprintf("%s|%d", notificationItemKey(item), chatID)
}

func hashID(prefix, value string) string {
	sum := sha1.Sum([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:])[:16]
}
