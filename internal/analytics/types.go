package analytics

import "time"

type PageView struct {
	ProjectID    int64
	Ts           time.Time
	Pathname     string
	Hostname     string
	ReferrerHost string
	ReferrerPath string
	SessionID    string
	DurationMs   int64
	ScreenWidth  int
	Props        map[string]string
}

type OverviewResult struct {
	Pageviews     int64
	Sessions      int64
	PagesCount    int64
	AvgDurationMs int64
}

type PageStat struct {
	Pathname  string
	Pageviews int64
	Sessions  int64
}

type TimelineBucket struct {
	Date      string // YYYY-MM-DD (day), YYYY-WXX (ISO week), YYYY-MM (month)
	Pageviews int64
	Sessions  int64
}

type ReferrerStat struct {
	Host      string
	Pageviews int64
	Sessions  int64
}

type SegmentBucket struct {
	Value     string
	Pageviews int64
	Sessions  int64
}

type Query struct {
	ProjectID int64
	Start     time.Time
	End       time.Time
	Pathname  string // optional filter
	Limit     int    // 0 = default 50
}
