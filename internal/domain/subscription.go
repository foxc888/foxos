package domain

import (
	"errors"
	"time"
)

var ErrSubscriptionSettingsStale = errors.New("subscription settings changed during update")

type Subscription struct {
	ID               string
	Name             string
	URL              string
	Enabled          bool
	Interval         int
	SettingsRevision int64
	LastDigest       string
	LastSuccessAt    time.Time
	LastAttemptAt    time.Time
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
