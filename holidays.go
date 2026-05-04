package main

import "time"

type ChinaCalendarInfo struct {
	Name string
	Type string
}

func ChinaCalendar(date string) ChinaCalendarInfo {
	// 2026 official holiday / make-up workday ranges from the State Council notice.
	holidays := map[string]string{}
	workdays := map[string]string{}
	addRange := func(m map[string]string, start, end, name string) {
		st, err1 := time.Parse("2006-01-02", start)
		ed, err2 := time.Parse("2006-01-02", end)
		if err1 != nil || err2 != nil {
			return
		}
		for d := st; !d.After(ed); d = d.AddDate(0, 0, 1) {
			m[d.Format("2006-01-02")] = name
		}
	}
	addRange(holidays, "2026-01-01", "2026-01-03", "元旦")
	workdays["2026-01-04"] = "调休班"
	addRange(holidays, "2026-02-15", "2026-02-23", "春节")
	workdays["2026-02-14"] = "调休班"
	workdays["2026-02-28"] = "调休班"
	addRange(holidays, "2026-04-04", "2026-04-06", "清明")
	addRange(holidays, "2026-05-01", "2026-05-05", "劳动节")
	workdays["2026-05-09"] = "调休班"
	addRange(holidays, "2026-06-19", "2026-06-21", "端午")
	addRange(holidays, "2026-09-25", "2026-09-27", "中秋")
	addRange(holidays, "2026-10-01", "2026-10-07", "国庆")
	workdays["2026-09-20"] = "调休班"
	workdays["2026-10-10"] = "调休班"
	if v, ok := workdays[date]; ok {
		return ChinaCalendarInfo{Name: v, Type: "work"}
	}
	if v, ok := holidays[date]; ok {
		return ChinaCalendarInfo{Name: v, Type: "holiday"}
	}
	return ChinaCalendarInfo{}
}

func clockInLocation(rfc string, loc *time.Location) string {
	if ts, err := time.Parse(time.RFC3339, rfc); err == nil {
		return ts.In(loc).Format("15:04")
	}
	return rfc
}
