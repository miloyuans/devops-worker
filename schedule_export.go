package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const latestScheduleVersionKey = "latest"

type scheduleExportData struct {
	Year        int
	Month       int
	Version     ScheduleMonthVersion
	Matrix      [][]string
	Details     [][]string
	ShiftLegend [][]string
}

func (s *Storage) ListScheduleMonthVersions(year int, month int, loc *time.Location) ([]ScheduleMonthVersion, error) {
	if loc == nil {
		loc = time.Local
	}
	if month < 1 || month > 12 {
		return nil, fmt.Errorf("月份参数无效")
	}
	byRevision := map[int]*ScheduleMonthVersion{}
	add := func(v ScheduleMonthVersion) {
		if v.Revision <= 0 || v.ItemsCount <= 0 {
			return
		}
		if strings.TrimSpace(v.Key) == "" {
			v.Key = scheduleRevisionKey(v.Revision)
		}
		if strings.TrimSpace(v.VersionID) == "" {
			v.VersionID = fmt.Sprintf("revision-%d", v.Revision)
		}
		if old, ok := byRevision[v.Revision]; ok {
			if v.IsActive {
				old.IsActive = true
				old.Source = "active"
				old.VersionID = firstNonEmpty(v.VersionID, old.VersionID)
			}
			if old.EffectiveAt == "" || v.EffectiveAt > old.EffectiveAt {
				old.EffectiveAt = v.EffectiveAt
			}
			if old.SourceApprovalID == "" {
				old.SourceApprovalID = v.SourceApprovalID
			}
			if old.Reviewer == "" {
				old.Reviewer = v.Reviewer
			}
			if v.ItemsCount > old.ItemsCount {
				old.ItemsCount = v.ItemsCount
			}
			return
		}
		vv := v
		byRevision[v.Revision] = &vv
	}

	active, err := s.LoadActive()
	if err == nil {
		count := len(filterItemsByMonth(active.Items, year, month, loc))
		add(ScheduleMonthVersion{
			Key:         scheduleRevisionKey(active.Revision),
			Revision:    active.Revision,
			VersionID:   firstNonEmpty(active.VersionID, fmt.Sprintf("revision-%d", active.Revision)),
			EffectiveAt: active.EffectiveAt,
			Source:      "active",
			ItemsCount:  count,
			IsActive:    true,
		})
	}

	approved, err := s.loadApprovedApprovals()
	if err != nil {
		return nil, err
	}
	for _, apv := range approved {
		if apv.Status != "approved" || len(apv.PreviewItems) == 0 {
			continue
		}
		rev := apv.NewRevision
		if rev <= 0 {
			rev = apv.BaseRevision + 1
		}
		count := len(filterItemsByMonth(apv.PreviewItems, year, month, loc))
		reviewer := strings.TrimSpace(apv.ReviewedByName)
		if reviewer == "" && apv.ReviewedBy != 0 {
			reviewer = strconv.FormatInt(apv.ReviewedBy, 10)
		}
		add(ScheduleMonthVersion{
			Key:              scheduleRevisionKey(rev),
			Revision:         rev,
			VersionID:        fmt.Sprintf("revision-%d", rev),
			EffectiveAt:      firstNonEmpty(apv.ReviewedAt, apv.CreatedAt),
			Source:           "approval",
			SourceApprovalID: apv.ID,
			Reviewer:         reviewer,
			ItemsCount:       count,
		})
	}

	versions := make([]ScheduleMonthVersion, 0, len(byRevision))
	for _, v := range byRevision {
		versions = append(versions, *v)
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Revision != versions[j].Revision {
			return versions[i].Revision > versions[j].Revision
		}
		return versions[i].EffectiveAt > versions[j].EffectiveAt
	})
	for i := range versions {
		versions[i].IsLatest = i == 0
		versions[i].Label = scheduleVersionLabel(versions[i])
	}
	return versions, nil
}

func (s *Storage) ResolveScheduleMonthVersion(year int, month int, versionKey string, loc *time.Location) (ScheduleMonthVersion, []ScheduleItem, error) {
	versions, err := s.ListScheduleMonthVersions(year, month, loc)
	if err != nil {
		return ScheduleMonthVersion{}, nil, err
	}
	if len(versions) == 0 {
		return ScheduleMonthVersion{}, []ScheduleItem{}, fmt.Errorf("%04d-%02d 暂无可导出的排班历史版本", year, month)
	}
	versionKey = strings.TrimSpace(versionKey)
	if versionKey == "" || versionKey == latestScheduleVersionKey {
		versionKey = versions[0].Key
	}
	var selected ScheduleMonthVersion
	found := false
	for _, v := range versions {
		if scheduleVersionKeyMatches(v, versionKey) {
			selected = v
			found = true
			break
		}
	}
	if !found {
		return ScheduleMonthVersion{}, nil, fmt.Errorf("未找到指定排班版本: %s", versionKey)
	}
	snapshot, err := s.loadScheduleSnapshotForRevision(selected.Revision)
	if err != nil {
		return ScheduleMonthVersion{}, nil, err
	}
	items := filterItemsByMonth(snapshot, year, month, loc)
	return selected, items, nil
}

func (s *Storage) loadScheduleSnapshotForRevision(revision int) ([]ScheduleItem, error) {
	active, err := s.LoadActive()
	if err == nil && active.Revision == revision {
		return NormalizeScheduleItems(active.Items), nil
	}
	approved, err := s.loadApprovedApprovals()
	if err != nil {
		return nil, err
	}
	for _, apv := range approved {
		rev := apv.NewRevision
		if rev <= 0 {
			rev = apv.BaseRevision + 1
		}
		if rev == revision {
			return NormalizeScheduleItems(apv.PreviewItems), nil
		}
	}
	return nil, fmt.Errorf("未找到 revision=%d 的排班快照", revision)
}

func (s *Storage) loadApprovedApprovals() ([]Approval, error) {
	dir := filepath.Join(s.Dir, "approvals", "approved")
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Approval{}, nil
		}
		return nil, err
	}
	var approvals []Approval
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		var apv Approval
		if err := readJSON(path, &apv); err == nil {
			approvals = append(approvals, apv)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(approvals, func(i, j int) bool {
		ri := approvals[i].NewRevision
		rj := approvals[j].NewRevision
		if ri != rj {
			return ri > rj
		}
		return firstNonEmpty(approvals[i].ReviewedAt, approvals[i].CreatedAt) > firstNonEmpty(approvals[j].ReviewedAt, approvals[j].CreatedAt)
	})
	return approvals, nil
}

func scheduleRevisionKey(revision int) string {
	return fmt.Sprintf("revision:%d", revision)
}

func scheduleVersionKeyMatches(v ScheduleMonthVersion, key string) bool {
	key = strings.TrimSpace(key)
	return key == v.Key || key == v.VersionID || key == strconv.Itoa(v.Revision) || key == scheduleRevisionKey(v.Revision)
}

func scheduleVersionLabel(v ScheduleMonthVersion) string {
	parts := []string{fmt.Sprintf("版本 %d", v.Revision)}
	if v.IsLatest {
		parts = append(parts, "默认最新")
	}
	if v.IsActive {
		parts = append(parts, "当前生效")
	}
	if v.EffectiveAt != "" {
		parts = append(parts, shortExportTime(v.EffectiveAt))
	}
	if v.SourceApprovalID != "" {
		parts = append(parts, compactID(v.SourceApprovalID))
	}
	parts = append(parts, fmt.Sprintf("%d 条", v.ItemsCount))
	return strings.Join(parts, " ｜ ")
}

func shortExportTime(s string) string {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func filterItemsByMonth(items []ScheduleItem, year int, month int, loc *time.Location) []ScheduleItem {
	if loc == nil {
		loc = time.Local
	}
	var out []ScheduleItem
	for _, item := range items {
		date := strings.TrimSpace(item.Date)
		if date == "" {
			if start, err := time.Parse(time.RFC3339, item.StartTime); err == nil {
				date = start.In(loc).Format("2006-01-02")
			}
		}
		t, err := time.ParseInLocation("2006-01-02", date, loc)
		if err == nil && t.Year() == year && int(t.Month()) == month {
			out = append(out, item)
		}
	}
	sortScheduleItems(out)
	return out
}

func buildScheduleExportData(year int, month int, version ScheduleMonthVersion, items []ScheduleItem, users []StaffUser, shifts []Shift, loc *time.Location) scheduleExportData {
	if loc == nil {
		loc = time.Local
	}
	days := daysInMonth(year, time.Month(month))
	staff := scheduleExportStaffRows(items, users)
	byStaffDay := map[string]ScheduleItem{}
	for _, item := range items {
		byStaffDay[item.StaffID+"|"+item.Date] = item
	}

	matrix := [][]string{}
	meta := fmt.Sprintf("%04d-%02d 排班月表", year, month)
	matrix = append(matrix, []string{meta, "版本", version.Label})
	header := []string{"姓名", "手机号", "用户组"}
	for d := 1; d <= days; d++ {
		date := time.Date(year, time.Month(month), d, 0, 0, 0, 0, loc)
		header = append(header, fmt.Sprintf("%02d %s", d, cnWeekday(date.Weekday())))
	}
	header = append(header, "排班数")
	matrix = append(matrix, header)
	for _, st := range staff {
		row := []string{st.Name, st.Phone, st.GroupName}
		count := 0
		for d := 1; d <= days; d++ {
			date := fmt.Sprintf("%04d-%02d-%02d", year, month, d)
			cell := ""
			if item, ok := byStaffDay[st.ID+"|"+date]; ok {
				cell = firstNonEmpty(item.ShiftShortName, item.ShiftName, item.ShiftCode)
				count++
			}
			row = append(row, cell)
		}
		row = append(row, strconv.Itoa(count))
		matrix = append(matrix, row)
	}

	shiftLegend := buildShiftLegendRows(shifts, items)
	shiftDescriptions := shiftDescriptionByCode(shiftLegend)
	details := [][]string{{"日期", "星期", "姓名", "手机号", "用户组", "班次", "班次简称", "班次时间说明", "开始时间", "结束时间", "Telegram User ID", "Staff ID"}}
	for _, item := range items {
		details = append(details, []string{
			item.Date,
			weekdayForDate(item.Date, loc),
			item.StaffName,
			item.StaffPhone,
			firstNonEmpty(item.StaffGroupName, item.StaffGroupID),
			item.ShiftName,
			item.ShiftShortName,
			shiftDescriptions[item.ShiftCode],
			formatExportClock(item.StartTime, loc),
			formatExportClock(item.EndTime, loc),
			formatMaybeInt64(item.TelegramUserID),
			item.StaffID,
		})
	}
	return scheduleExportData{Year: year, Month: month, Version: version, Matrix: matrix, Details: details, ShiftLegend: shiftLegend}
}

func buildShiftLegendRows(shifts []Shift, items []ScheduleItem) [][]string {
	rows := [][]string{{"班次编码", "班次名称", "简称", "班次时间说明", "开始", "结束", "时区", "是否跨天", "通知"}}
	seen := map[string]bool{}
	ordered := make([]Shift, 0, len(shifts))
	for _, sh := range shifts {
		code := strings.TrimSpace(sh.Code)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		ordered = append(ordered, sh)
	}
	for _, item := range items {
		code := strings.TrimSpace(item.ShiftCode)
		if code == "" || seen[code] {
			continue
		}
		startClock := clockFromRFC3339(item.StartTime)
		endClock := clockFromRFC3339(item.EndTime)
		if startClock == "" {
			startClock = clockPart(item.StartTime)
		}
		if endClock == "" {
			endClock = clockPart(item.EndTime)
		}
		sh := Shift{
			Code:      code,
			Name:      firstNonEmpty(item.ShiftName, code),
			ShortName: firstNonEmpty(item.ShiftShortName, code),
			Start:     startClock,
			End:       endClock,
			Timezone:  DefaultShiftTimezone,
			CrossDay:  deriveCrossDay(startClock, endClock),
			Enabled:   true,
		}
		seen[code] = true
		ordered = append(ordered, sh)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedBy == "system" && ordered[j].CreatedBy != "system" {
			return true
		}
		if ordered[i].CreatedBy != "system" && ordered[j].CreatedBy == "system" {
			return false
		}
		return ordered[i].Code < ordered[j].Code
	})
	for _, sh := range ordered {
		rows = append(rows, []string{
			sh.Code,
			sh.Name,
			sh.ShortName,
			formatShiftTimeDescription(sh),
			sh.Start,
			sh.End,
			firstNonEmpty(sh.Timezone, DefaultShiftTimezone),
			yesNo(shiftCrossesDay(sh)),
			yesNo(shiftNotificationEnabled(sh)),
		})
	}
	return rows
}

func shiftDescriptionByCode(rows [][]string) map[string]string {
	out := map[string]string{}
	for i, row := range rows {
		if i == 0 || len(row) < 4 {
			continue
		}
		out[row[0]] = row[3]
	}
	return out
}

func formatShiftTimeDescription(sh Shift) string {
	start := strings.TrimSpace(sh.Start)
	end := strings.TrimSpace(sh.End)
	if start == "" && end == "" {
		return ""
	}
	desc := start + "-" + end
	if strings.TrimSpace(sh.End) == "24:00" {
		desc += "（次日 00:00 结束）"
	} else if shiftCrossesDay(sh) {
		desc += "（跨天，次日结束）"
	} else {
		desc += "（当日）"
	}
	if tz := strings.TrimSpace(sh.Timezone); tz != "" {
		desc += " " + tz
	}
	return desc
}

func shiftCrossesDay(sh Shift) bool {
	if sh.CrossDay {
		return true
	}
	return deriveCrossDay(sh.Start, sh.End)
}

func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func clockFromRFC3339(v string) string {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
		return t.Format("15:04")
	}
	return ""
}

func clockPart(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 5 {
		return v[len(v)-5:]
	}
	return v
}

type scheduleExportStaff struct {
	ID        string
	Name      string
	Phone     string
	GroupName string
	Order     int
}

func scheduleExportStaffRows(items []ScheduleItem, users []StaffUser) []scheduleExportStaff {
	groupsByUser := map[string]string{}
	byID := map[string]*scheduleExportStaff{}
	order := 0
	for _, u := range users {
		if !u.Enabled {
			continue
		}
		id := strings.TrimSpace(u.ID)
		if id == "" {
			continue
		}
		groupName := strings.TrimSpace(u.GroupID)
		if groupName == "" {
			groupName = DefaultUserGroupID
		}
		groupsByUser[id] = groupName
		byID[id] = &scheduleExportStaff{ID: id, Name: u.Name, Phone: strings.TrimSpace(u.Phone), GroupName: groupName, Order: order}
		order++
	}
	for _, item := range items {
		id := strings.TrimSpace(item.StaffID)
		if id == "" {
			continue
		}
		groupName := firstNonEmpty(item.StaffGroupName, item.StaffGroupID, groupsByUser[id])
		if groupName == "" {
			groupName = DefaultUserGroupID
		}
		if st, ok := byID[id]; ok {
			if strings.TrimSpace(st.Name) == "" {
				st.Name = item.StaffName
			}
			if strings.TrimSpace(st.Phone) == "" {
				st.Phone = strings.TrimSpace(item.StaffPhone)
			}
			if strings.TrimSpace(st.GroupName) == "" || st.GroupName == DefaultUserGroupID {
				st.GroupName = groupName
			}
			continue
		}
		byID[id] = &scheduleExportStaff{ID: id, Name: item.StaffName, Phone: strings.TrimSpace(item.StaffPhone), GroupName: groupName, Order: order}
		order++
	}
	rows := make([]scheduleExportStaff, 0, len(byID))
	for _, st := range byID {
		rows = append(rows, *st)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Order != rows[j].Order {
			return rows[i].Order < rows[j].Order
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func writeScheduleCSV(w io.Writer, data scheduleExportData) error {
	_, _ = w.Write([]byte("\ufeff"))
	cw := csv.NewWriter(w)
	rows := append([][]string{}, data.Matrix...)
	if len(data.ShiftLegend) > 0 {
		rows = append(rows, []string{})
		rows = append(rows, data.ShiftLegend...)
	}
	for _, row := range padStringRows(rows) {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func padStringRows(rows [][]string) [][]string {
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = append([]string(nil), row...)
		for len(out[i]) < maxCols {
			out[i] = append(out[i], "")
		}
	}
	return out
}

func writeScheduleXLSX(w io.Writer, data scheduleExportData) error {
	zw := zip.NewWriter(w)
	files := map[string]string{
		"[Content_Types].xml":        xlsxContentTypesXML(),
		"_rels/.rels":                xlsxRootRelsXML(),
		"xl/workbook.xml":            xlsxWorkbookXML([]string{"排班月表", "排班明细", "班次说明"}),
		"xl/_rels/workbook.xml.rels": xlsxWorkbookRelsXML(3),
		"xl/styles.xml":              xlsxStylesXML(),
		"xl/worksheets/sheet1.xml":   xlsxWorksheetXML(data.Matrix, true),
		"xl/worksheets/sheet2.xml":   xlsxWorksheetXML(data.Details, false),
		"xl/worksheets/sheet3.xml":   xlsxWorksheetXML(data.ShiftLegend, false),
	}
	ordered := []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/worksheets/sheet3.xml"}
	for _, name := range ordered {
		fh, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err := fh.Write([]byte(files[name])); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

func xlsxContentTypesXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet3.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`
}

func xlsxRootRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`
}

func xlsxWorkbookXML(sheetNames []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, name := range sheetNames {
		b.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(name), i+1, i+1))
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func xlsxWorkbookRelsXML(sheetCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheetCount; i++ {
		b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i))
	}
	b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, sheetCount+1))
	b.WriteString(`</Relationships>`)
	return b.String()
}

func xlsxStylesXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Calibri"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF2563EB"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="2"><border><left/><right/><top/><bottom/><diagonal/></border><border><left style="thin"><color rgb="FFE2E8F0"/></left><right style="thin"><color rgb="FFE2E8F0"/></right><top style="thin"><color rgb="FFE2E8F0"/></top><bottom style="thin"><color rgb="FFE2E8F0"/></bottom><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1"/><xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`
}

func xlsxWorksheetXML(rows [][]string, freeze bool) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	if freeze {
		b.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane xSplit="3" ySplit="2" topLeftCell="D3" activePane="bottomRight" state="frozen"/></sheetView></sheetViews>`)
	}
	b.WriteString(xlsxColsXML(rows))
	b.WriteString(`<sheetData>`)
	for rIdx, row := range rows {
		b.WriteString(fmt.Sprintf(`<row r="%d">`, rIdx+1))
		for cIdx, val := range row {
			cell := xlsxCellRef(rIdx+1, cIdx+1)
			style := 0
			if rIdx == 0 || rIdx == 1 {
				style = 1
			} else {
				style = 2
			}
			b.WriteString(fmt.Sprintf(`<c r="%s" t="inlineStr" s="%d"><is><t>%s</t></is></c>`, cell, style, xmlEscape(val)))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData><pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/></worksheet>`)
	return b.String()
}

func xlsxColsXML(rows [][]string) string {
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<cols>`)
	for c := 1; c <= maxCols; c++ {
		width := 12.0
		if c == 1 {
			width = 16
		} else if c == 2 {
			width = 16
		} else if c == 3 {
			width = 14
		} else if c > 3 && c < maxCols {
			width = 8
		}
		b.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, c, c, width))
	}
	b.WriteString(`</cols>`)
	return b.String()
}

func xlsxCellRef(row int, col int) string {
	name := ""
	for col > 0 {
		col--
		name = string(rune('A'+(col%26))) + name
		col /= 26
	}
	return name + strconv.Itoa(row)
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func cnWeekday(w time.Weekday) string {
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
	default:
		return "周日"
	}
}

func weekdayForDate(date string, loc *time.Location) string {
	if t, err := time.ParseInLocation("2006-01-02", date, loc); err == nil {
		return cnWeekday(t.Weekday())
	}
	return ""
}

func formatExportClock(v string, loc *time.Location) string {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
		return t.In(loc).Format("2006-01-02 15:04")
	}
	return v
}

func formatMaybeInt64(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}
