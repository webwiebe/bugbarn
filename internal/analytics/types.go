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
	Pageviews     int64 `json:"pageviews"`
	Sessions      int64 `json:"sessions"`
	PagesCount    int64 `json:"pages"`
	AvgDurationMs int64 `json:"avgDurationMs"`
}

type PageStat struct {
	Pathname  string `json:"pathname"`
	Pageviews int64  `json:"pageviews"`
	Sessions  int64  `json:"sessions"`
}

type TimelineBucket struct {
	Date      string `json:"date"`
	Pageviews int64  `json:"pageviews"`
	Sessions  int64  `json:"sessions"`
}

type ReferrerStat struct {
	Host      string `json:"host"`
	Pageviews int64  `json:"pageviews"`
	Sessions  int64  `json:"sessions"`
}

type SegmentBucket struct {
	Value     string `json:"value"`
	Pageviews int64  `json:"pageviews"`
	Sessions  int64  `json:"sessions"`
}

type Query struct {
	ProjectID int64
	Start     time.Time
	End       time.Time
	Pathname  string
	Limit     int
}

type PageFlowResult struct {
	Pathname string      `json:"pathname"`
	CameFrom []FlowEntry `json:"cameFrom"`
	WentTo   []FlowEntry `json:"wentTo"`
}

type FlowEntry struct {
	Pathname string  `json:"pathname"`
	Count    int64   `json:"count"`
	Pct      float64 `json:"pct"`
}

type ScrollDepthResult struct {
	Pathname string         `json:"pathname"`
	Buckets  []ScrollBucket `json:"buckets"`
}

type ScrollBucket struct {
	Label string  `json:"label"`
	Count int64   `json:"count"`
	Pct   float64 `json:"pct"`
}

type DropoutStat struct {
	Pathname        string  `json:"pathname"`
	Pageviews       int64   `json:"pageviews"`
	BouncedSessions int64   `json:"bouncedSessions"`
	BounceRate      float64 `json:"bounceRate"`
}
