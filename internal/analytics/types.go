package analytics

import "time"

type PageView struct {
	ProjectID        int64
	Ts               time.Time
	Pathname         string
	Hostname         string
	ReferrerHost     string
	ReferrerPath     string
	SessionID        string
	DurationMs       int64
	ScreenWidth      int
	Props            map[string]string
	VisitorID        string
	MaxScrollPct     int
	InteractionCount int
	ExitPathname     string
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

type PageFlowResult struct {
	Pathname string
	CameFrom []FlowEntry
	WentTo   []FlowEntry
}

type FlowEntry struct {
	Pathname string
	Count    int64
	Pct      float64
}

type ScrollDepthResult struct {
	Pathname string
	Buckets  []ScrollBucket
}

type ScrollBucket struct {
	Label string // "0–24%", "25–49%", "50–74%", "75–99%", "100%"
	Count int64
	Pct   float64
}

type DropoutStat struct {
	Pathname        string
	Pageviews       int64
	BouncedSessions int64
	BounceRate      float64
}
