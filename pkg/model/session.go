package model

import "time"

type ProcessInfo struct {
	ID       int
	User     string
	Host     string
	Database string
	Command  string
	Time     int64
	State    string
	Info     string
}

type SessionInfo struct {
	ProcessInfo
	StartTime time.Time
	Duration  time.Duration
	BlockedBy int
	Blocking  []int
}
