package domain

import "time"

// JobStatus is persisted so an operation remains inspectable across restarts.
type JobStatus string

const (
	JobQueued     JobStatus = "QUEUED"
	JobRunning    JobStatus = "RUNNING"
	JobVerifying  JobStatus = "VERIFYING"
	JobSucceeded  JobStatus = "SUCCEEDED"
	JobFailed     JobStatus = "FAILED"
	JobRolledBack JobStatus = "ROLLED_BACK"
)

type Job struct {
	ID             string
	Kind           string
	Status         JobStatus
	Progress       int
	IdempotencyKey string
	Request        map[string]any
	Result         map[string]any
	ErrorClass     string
	ErrorMessage   string
	Attempts       int
	CreatedAt      time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
	UpdatedAt      time.Time
}

func (j Job) Terminal() bool {
	return j.Status == JobSucceeded || j.Status == JobFailed || j.Status == JobRolledBack
}

type MihomoDraft struct {
	ID        string
	Mode      string
	MixedPort int
	AllowLAN  bool
	Rules     []string
	Revision  int64
	UpdatedAt time.Time
}

type MihomoSnapshot struct {
	ID        string
	Digest    string
	Label     string
	Body      []byte
	CreatedAt time.Time
}

type AlertSeverity string

const (
	AlertInfo     AlertSeverity = "info"
	AlertWarning  AlertSeverity = "warning"
	AlertCritical AlertSeverity = "critical"
)

type Alert struct {
	ID           string
	Key          string
	Severity     AlertSeverity
	Title        string
	Message      string
	Acknowledged bool
	FirstSeen    time.Time
	LastSeen     time.Time
	ResolvedAt   time.Time
}
