package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func BuildScheduleItems(rules []ScheduleRule, users []StaffUser, shifts []Shift, loc *time.Location) ([]ScheduleItem, error) {
	userMap := map[string]StaffUser{}
	for _, u := range users {
		if u.Enabled {
			userMap[u.ID] = u
		}
	}
	shiftMap := map[string]Shift{}
	for _, sh := range shifts {
		shiftMap[sh.Code] = sh
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
		weekSet := intSet(rule.WeekNums)
		weekdaySet := intSet(rule.Weekdays)
		days := daysInMonth(rule.Year, time.Month(rule.Month))
		for day := 1; day <= days; day++ {
			weekNum := ((day - 1) / 7) + 1
			date := time.Date(rule.Year, time.Month(rule.Month), day, 0, 0, 0, 0, loc)
			weekday := goWeekdayToCN(date.Weekday())
			if !weekSet[weekNum] || !weekdaySet[weekday] {
				continue
			}
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

func makeShiftTime(date time.Time, shift Shift, loc *time.Location) (time.Time, time.Time, error) {
	startHour, startMin, err := parseClock(shift.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("班次 %s 开始时间错误: %w", shift.Name, err)
	}
	endHour, endMin, err := parseClock(shift.End)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("班次 %s 结束时间错误: %w", shift.Name, err)
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), startHour, startMin, 0, 0, loc)
	var end time.Time
	if shift.End == "24:00" {
		end = time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
	} else {
		end = time.Date(date.Year(), date.Month(), date.Day(), endHour, endMin, 0, 0, loc)
	}
	if shift.CrossDay || !end.After(start) {
		end = end.Add(24 * time.Hour)
	}
	return start, end, nil
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
