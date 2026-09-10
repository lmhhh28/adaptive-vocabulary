package core

import "time"

// Clock abstracts time operations to support deterministic testing in E2E.
type Clock interface {
	Now() time.Time
	NowMicro() int64
}

// NowMicro returns the current UTC time in microseconds consistently.
func NowMicro() int64 {
	return time.Now().UTC().UnixMicro()
}

// RealClock implements Clock using real system time.
type RealClock struct{}

func NewRealClock() Clock {
	return &RealClock{}
}

func (c *RealClock) Now() time.Time {
	return time.Now().UTC()
}

func (c *RealClock) NowMicro() int64 {
	return NowMicro()
}
