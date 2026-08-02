package domain

import "time"

type AuditOutcome string

const (
	AuditStarted   AuditOutcome = "STARTED"
	AuditSucceeded AuditOutcome = "SUCCEEDED"
	AuditFailed    AuditOutcome = "FAILED"
)

type AuditEvent struct {
	ID        string
	Action    string
	TargetID  string
	Outcome   AuditOutcome
	Details   map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}
