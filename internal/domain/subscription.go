package domain

import "time"

type Subscription struct {
	ID            string
	Name          string
	URL           string
	Enabled       bool
	Interval      int
	LastDigest    string
	LastSuccessAt time.Time
	LastAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
