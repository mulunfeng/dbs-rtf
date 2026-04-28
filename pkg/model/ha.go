package model

import "time"

type InstanceState string

const (
	StateHealthy     InstanceState = "healthy"
	StateUnhealthy   InstanceState = "unhealthy"
	StateDown        InstanceState = "down"
	StateFailingOver InstanceState = "failing_over"
)

type InstanceHAStatus struct {
	Name             string
	Host             string
	Port             int
	State            InstanceState
	LastCheck        time.Time
	LastPingLatency  time.Duration
	ConsecutiveFails int
	LastFailover     time.Time
}

type FailoverEvent struct {
	Timestamp time.Time
	EventType string // instance_unhealthy, instance_down, failover_start, failover_complete, failover_failed
	From      string
	To        string
	Reason    string
	Result    string
	Warnings  []string
}
