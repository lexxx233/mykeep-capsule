package retrieval

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// window is an inclusive unix-second time range.
type window struct{ start, end int64 }

var (
	isoRe       = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	agoRe       = regexp.MustCompile(`\b(\d+)\s+(day|week|month|year)s?\s+ago\b`)
	monthYearRe = regexp.MustCompile(`\b(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)\.?\s+(\d{4})\b`)
	yearRe      = regexp.MustCompile(`\b(19|20)\d{2}\b`)
)

var monthNames = map[string]time.Month{
	"january": 1, "jan": 1, "february": 2, "feb": 2, "march": 3, "mar": 3,
	"april": 4, "apr": 4, "may": 5, "june": 6, "jun": 6, "july": 7, "jul": 7,
	"august": 8, "aug": 8, "september": 9, "sep": 9, "sept": 9, "october": 10, "oct": 10,
	"november": 11, "nov": 11, "december": 12, "dec": 12,
}

// extractTemporalWindow parses a closed set of date expressions (PLAN §5.4, D14):
// ISO dates, "N days/weeks/months/years ago", yesterday/today/tomorrow,
// last/this/next week|month|year, "Month YYYY", and a standalone 4-digit year.
// Unsupported phrasing yields ok=false (the recall arm is then simply absent).
func extractTemporalWindow(query string, now int64) (window, bool) {
	t := time.Unix(now, 0).UTC()
	q := strings.ToLower(query)

	if m := isoRe.FindStringSubmatch(q); m != nil {
		if d, err := time.ParseInLocation("2006-01-02", m[0], time.UTC); err == nil {
			return dayWindow(d), true
		}
	}
	if m := agoRe.FindStringSubmatch(q); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "day":
			return dayWindow(t.AddDate(0, 0, -n)), true
		case "week":
			return weekWindow(t.AddDate(0, 0, -7*n)), true
		case "month":
			return monthWindow(t.AddDate(0, -n, 0)), true
		case "year":
			return yearWindow(t.AddDate(-n, 0, 0)), true
		}
	}
	switch {
	case strings.Contains(q, "yesterday"):
		return dayWindow(t.AddDate(0, 0, -1)), true
	case strings.Contains(q, "today"):
		return dayWindow(t), true
	case strings.Contains(q, "tomorrow"):
		return dayWindow(t.AddDate(0, 0, 1)), true
	}
	for _, u := range []struct {
		phrase string
		fn     func(time.Time) window
		shift  func(time.Time, int) time.Time
	}{
		{"week", weekWindow, func(t time.Time, n int) time.Time { return t.AddDate(0, 0, 7*n) }},
		{"month", monthWindow, func(t time.Time, n int) time.Time { return t.AddDate(0, n, 0) }},
		{"year", yearWindow, func(t time.Time, n int) time.Time { return t.AddDate(n, 0, 0) }},
	} {
		if strings.Contains(q, "last "+u.phrase) {
			return u.fn(u.shift(t, -1)), true
		}
		if strings.Contains(q, "this "+u.phrase) {
			return u.fn(t), true
		}
		if strings.Contains(q, "next "+u.phrase) {
			return u.fn(u.shift(t, 1)), true
		}
	}
	if m := monthYearRe.FindStringSubmatch(q); m != nil {
		mon := monthNames[strings.TrimSuffix(m[1], ".")]
		year, _ := strconv.Atoi(m[2])
		return monthWindow(time.Date(year, mon, 1, 0, 0, 0, 0, time.UTC)), true
	}
	if m := yearRe.FindString(q); m != "" {
		year, _ := strconv.Atoi(m)
		return yearWindow(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)), true
	}
	return window{}, false
}

func dayWindow(t time.Time) window {
	s := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return window{s.Unix(), s.AddDate(0, 0, 1).Add(-time.Second).Unix()}
}

func weekWindow(t time.Time) window {
	wd := int(t.Weekday()) // Sunday=0
	if wd == 0 {
		wd = 7
	}
	s := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(wd - 1))
	return window{s.Unix(), s.AddDate(0, 0, 7).Add(-time.Second).Unix()}
}

func monthWindow(t time.Time) window {
	s := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return window{s.Unix(), s.AddDate(0, 1, 0).Add(-time.Second).Unix()}
}

func yearWindow(t time.Time) window {
	s := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	return window{s.Unix(), s.AddDate(1, 0, 0).Add(-time.Second).Unix()}
}
