package domain

import (
	"time"
)

// MonitorCheckIn is one check-in as the storage layer applies it. Interval and
// Grace are nil when the caller did not send them: the stored values stay, and
// an unknown monitor is then not created.
type MonitorCheckIn struct {
	ProjectID int64
	Slug      string
	Status    string
	Interval  *time.Duration
	Grace     *time.Duration
	// Schedule and Timezone switch the monitor to a cron schedule. Sending an
	// Interval switches it back to interval mode and clears both. Timezone
	// alone changes the zone of a monitor that already has a schedule.
	Schedule *string
	Timezone *string
	At       time.Time
}

// MonitorCheckInResult is the monitor after the check-in plus what it was
// before, so a caller can tell a recovery from a plain check-in.
type MonitorCheckInResult struct {
	Monitor       Monitor
	Created       bool
	PreviousState string
}
