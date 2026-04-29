package models

import "time"

type Decision struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
	ResetTime  time.Time
	Message    string
}
