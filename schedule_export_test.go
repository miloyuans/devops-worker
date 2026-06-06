package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testScheduleExportStore(t *testing.T, loc *time.Location) *Storage {
	t.Helper()
	store := NewStorage(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	june := testDailyReportItem(t, loc, "2026-06-01", "u1", "Alice", "morning", "早班", "早", "09:00", "18:00")
	june.StaffPhone = "18800000001"
	mayV1 := testDailyReportItem(t, loc, "2026-05-01", "u1", "Alice", "morning", "早班", "早", "09:00", "18:00")
	mayV1.StaffPhone = "18800000001"
	mayV2 := testDailyReportItem(t, loc, "2026-05-02", "u2", "Bob", "night", "夜班", "夜", "00:00", "09:00")
	mayV2.StaffPhone = "18800000002"
	active := ActiveSchedule{Revision: 3, VersionID: "active-v3", EffectiveAt: "2026-06-02T09:00:00+04:00", Items: []ScheduleItem{june}}
	if err := writeJSONAtomic(filepath.Join(store.Dir, "schedules", "active.json"), active); err != nil {
		t.Fatal(err)
	}
	apv1 := Approval{ID: "apv_may_v1", Status: "approved", BaseRevision: 0, NewRevision: 1, ReviewedAt: "2026-05-01T10:00:00+04:00", PreviewItems: []ScheduleItem{mayV1}}
	apv2 := Approval{ID: "apv_may_v2", Status: "approved", BaseRevision: 1, NewRevision: 2, ReviewedAt: "2026-05-02T10:00:00+04:00", ReviewedByName: "主管", PreviewItems: []ScheduleItem{mayV2}}
	if err := writeJSONAtomic(filepath.Join(store.Dir, "approvals", "approved", apv1.ID+".json"), apv1); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(filepath.Join(store.Dir, "approvals", "approved", apv2.ID+".json"), apv2); err != nil {
		t.Fatal(err)
	}
	users := []StaffUser{
		{ID: "u1", Name: "Alice", Phone: "18800000001", GroupID: "devops", Enabled: true},
		{ID: "u2", Name: "Bob", Phone: "18800000002", GroupID: "devops", Enabled: true},
	}
	if err := store.SaveUsers(users); err != nil {
		t.Fatal(err)
	}
	return store
}

func flattenRows(rows [][]string) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row...)
	}
	return out
}

func TestScheduleExportDefaultsToLatestHistoricalMonthVersion(t *testing.T) {
	loc, _ := time.LoadLocation(DefaultShiftTimezone)
	store := testScheduleExportStore(t, loc)
	versions, err := store.ListScheduleMonthVersions(2026, 5, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 May versions, got %#v", versions)
	}
	if versions[0].Revision != 2 || !versions[0].IsLatest {
		t.Fatalf("expected revision 2 as latest May version, got %#v", versions[0])
	}
	selected, items, err := store.ResolveScheduleMonthVersion(2026, 5, "", loc)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Revision != 2 || len(items) != 1 || items[0].StaffName != "Bob" {
		t.Fatalf("expected latest May snapshot revision 2 / Bob, got version=%#v items=%#v", selected, items)
	}
}

func TestScheduleCSVExportIncludesNamePhoneAndMonthMatrix(t *testing.T) {
	loc, _ := time.LoadLocation(DefaultShiftTimezone)
	store := testScheduleExportStore(t, loc)
	version, items, err := store.ResolveScheduleMonthVersion(2026, 5, "revision:2", loc)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := store.LoadUsers()
	shifts, _ := store.LoadShifts()
	data := buildScheduleExportData(2026, 5, version, items, users, enabledShifts(shifts), loc)
	var buf bytes.Buffer
	if err := writeScheduleCSV(&buf, data); err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(buf.String(), "\ufeff")))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rows[2], "|")
	if !strings.Contains(joined, "Alice|18800000001") || !strings.Contains(strings.Join(rows[3], "|"), "Bob|18800000002") {
		t.Fatalf("expected exported rows to contain staff names and phones, got %#v", rows)
	}
	if rows[1][0] != "姓名" || rows[1][1] != "手机号" || rows[1][2] != "用户组" {
		t.Fatalf("unexpected matrix headers: %#v", rows[1][:3])
	}
	csvText := strings.Join(flattenRows(rows), "|")
	if !strings.Contains(csvText, "班次时间说明") || !strings.Contains(csvText, "00:00-09:00") {
		t.Fatalf("expected CSV export to include shift time legend, got %#v", rows)
	}
}

func TestScheduleXLSXExportIsValidZipWithShiftLegendSheet(t *testing.T) {
	loc, _ := time.LoadLocation(DefaultShiftTimezone)
	store := testScheduleExportStore(t, loc)
	version, items, err := store.ResolveScheduleMonthVersion(2026, 5, "revision:2", loc)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := store.LoadUsers()
	shifts, _ := store.LoadShifts()
	data := buildScheduleExportData(2026, 5, version, items, users, enabledShifts(shifts), loc)
	var buf bytes.Buffer
	if err := writeScheduleXLSX(&buf, data); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		seen[f.Name] = true
	}
	for _, name := range []string{"xl/workbook.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/worksheets/sheet3.xml"} {
		if !seen[name] {
			t.Fatalf("xlsx missing %s; files=%#v", name, seen)
		}
	}
}
