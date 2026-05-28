package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDailyReportItem(t *testing.T, loc *time.Location, date, staffID, staffName, shiftCode, shiftName, shortName, start, end string) ScheduleItem {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		t.Fatal(err)
	}
	sh := Shift{Code: shiftCode, Name: shiftName, ShortName: shortName, Start: start, End: end, Timezone: loc.String(), Enabled: true, NotifyEnabled: boolPtr(true)}
	st, et, err := makeShiftTime(d, sh, loc)
	if err != nil {
		t.Fatal(err)
	}
	return ScheduleItem{Date: date, StaffID: staffID, StaffName: staffName, ShiftCode: shiftCode, ShiftName: shiftName, ShiftShortName: shortName, StartTime: st.Format(time.RFC3339), EndTime: et.Format(time.RFC3339), NotifyEnabled: boolPtr(true)}
}

func testDailyReportStore(t *testing.T, items []ScheduleItem) *Storage {
	t.Helper()
	store := NewStorage(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	active := ActiveSchedule{Revision: 1, VersionID: "test-version", Items: items}
	if err := writeJSONAtomic(filepath.Join(store.Dir, "schedules", "active.json"), active); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestLoadDailyScheduleReportItemsUsesNextDayMidnightNight(t *testing.T) {
	loc, err := time.LoadLocation(DefaultShiftTimezone)
	if err != nil {
		t.Fatal(err)
	}
	items := []ScheduleItem{
		testDailyReportItem(t, loc, "2026-05-28", "u1", "Alice", "morning", "早班", "早", "09:00", "18:00"),
		testDailyReportItem(t, loc, "2026-05-28", "u2", "Bob", "night", "晚班", "晚", "00:00", "09:00"),
		testDailyReportItem(t, loc, "2026-05-29", "u3", "Carol", "night", "晚班", "晚", "00:00", "09:00"),
	}
	store := testDailyReportStore(t, items)
	svc := &TelegramService{Store: store, Loc: loc}
	reportAt := dailyReportTimeForDate(time.Date(2026, 5, 28, 12, 0, 0, 0, loc), "09:00", loc)

	got, err := svc.loadDailyScheduleReportItems("2026-05-28", reportAt)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range got {
		seen[item.Date+"|"+item.StaffID+"|"+item.ShiftCode] = true
	}
	if !seen["2026-05-28|u1|morning"] {
		t.Fatalf("expected today's non-night shift in report, got %#v", got)
	}
	if seen["2026-05-28|u2|night"] {
		t.Fatalf("did not expect today's 00:00 night shift in report, got %#v", got)
	}
	if !seen["2026-05-29|u3|night"] {
		t.Fatalf("expected next day's 00:00 night shift in report, got %#v", got)
	}
}

func TestDailyScheduleReportRendersForwardNightDateAndLeaveRemaining(t *testing.T) {
	loc, err := time.LoadLocation(DefaultShiftTimezone)
	if err != nil {
		t.Fatal(err)
	}
	items := []ScheduleItem{
		testDailyReportItem(t, loc, "2026-05-28", "u1", "Alice", "annual_leave", "年假", "年", "00:00", "23:59"),
		testDailyReportItem(t, loc, "2026-05-29", "u1", "Alice", "annual_leave", "年假", "年", "00:00", "23:59"),
		testDailyReportItem(t, loc, "2026-05-30", "u1", "Alice", "annual_leave", "年假", "年", "00:00", "23:59"),
		testDailyReportItem(t, loc, "2026-05-29", "u2", "Bob", "night", "晚班", "晚", "00:00", "09:00"),
	}
	store := testDailyReportStore(t, items)
	svc := &TelegramService{Store: store, Loc: loc, Cfg: Config{DailyReportTime: "09:00"}}
	reportAt := dailyReportTimeForDate(time.Date(2026, 5, 28, 12, 0, 0, 0, loc), "09:00", loc)
	reportItems, err := svc.loadDailyScheduleReportItems("2026-05-28", reportAt)
	if err != nil {
		t.Fatal(err)
	}
	statuses := store.BuildScheduleItemStatuses(reportItems, loc)
	statuses = svc.enrichDailyScheduleReportStatuses("2026-05-28", statuses)
	messages := svc.buildDailyScheduleReportMessages("2026-05-28", statuses)
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %d", len(messages))
	}
	msg := messages[0]
	for _, want := range []string{"夜班口径: 2026-05-29", "班次日期: 2026-05-29", "剩余休假: 含当日 3天"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected report to contain %q, got:\n%s", want, msg)
		}
	}
}

func TestDailyLeaveRemainingLabelFromMiddleOfLeaveBlock(t *testing.T) {
	loc, err := time.LoadLocation(DefaultShiftTimezone)
	if err != nil {
		t.Fatal(err)
	}
	items := []ScheduleItem{
		testDailyReportItem(t, loc, "2026-05-28", "u1", "Alice", "annual_leave", "年假", "年", "00:00", "23:59"),
		testDailyReportItem(t, loc, "2026-05-29", "u1", "Alice", "annual_leave", "年假", "年", "00:00", "23:59"),
		testDailyReportItem(t, loc, "2026-05-30", "u1", "Alice", "annual_leave", "年假", "年", "00:00", "23:59"),
	}
	label := dailyLeaveRemainingLabel(items[1], items, loc)
	if !strings.Contains(label, "含当日 2天") || !strings.Contains(label, "2026-05-30") {
		t.Fatalf("unexpected leave remaining label: %s", label)
	}
}
