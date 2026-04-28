package monitor

import (
	"context"
	"time"

	"github.com/dbs-rtf/agent/internal/adapters"
)

type PingResult struct {
	OK           bool
	Latency      time.Duration
	Error        error
	ProcessCount int
}

type HeartbeatChecker struct {
	adapter     adapters.DatabaseAdapter
	pingTimeout time.Duration
}

func NewHeartbeatChecker(adapter adapters.DatabaseAdapter, timeout time.Duration) *HeartbeatChecker {
	return &HeartbeatChecker{
		adapter:     adapter,
		pingTimeout: timeout,
	}
}

func (h *HeartbeatChecker) Ping(ctx context.Context) PingResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, h.pingTimeout)
	defer cancel()

	result := PingResult{}

	// Primary check: Ping
	if err := h.adapter.Ping(ctx); err != nil {
		result.OK = false
		result.Latency = time.Since(start)
		result.Error = err
		return result
	}

	// Secondary check: GetProcessList (deeper health check)
	processes, err := h.adapter.GetProcessList(ctx)
	result.Latency = time.Since(start)
	if err != nil {
		// Ping succeeded but process list failed — still unhealthy
		result.OK = false
		result.Error = err
		result.ProcessCount = 0
		return result
	}

	result.OK = true
	result.ProcessCount = len(processes)
	return result
}
