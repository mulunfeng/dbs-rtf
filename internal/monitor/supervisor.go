package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

type Supervisor struct {
	mu         sync.RWMutex
	instances  []model.InstanceConfig
	config     HAConfig
	status     map[string]*model.InstanceHAStatus
	events     []model.FailoverEvent
	maxEvents  int

	heartbeat     map[string]*HeartbeatChecker
	guard         *FailoverGuard
	notifier      Notifier
	failoverFunc  func(ctx context.Context, req model.OperationRequest) (model.OperationResult, error)

	cancel   context.CancelFunc
	running  bool
	paused   bool
}

func NewSupervisor(
	instances []model.InstanceConfig,
	config HAConfig,
	notifier Notifier,
) *Supervisor {
	s := &Supervisor{
		instances:  instances,
		config:     config,
		status:     make(map[string]*model.InstanceHAStatus),
		maxEvents:  100,
		heartbeat:  make(map[string]*HeartbeatChecker),
		guard:      NewFailoverGuard(config.Failover.Cooldown),
		notifier:   notifier,
	}

	// Initialize heartbeat checkers and status for each instance
	for _, inst := range instances {
		key := inst.Address()
		s.status[key] = &model.InstanceHAStatus{
			Name:  inst.Name,
			Host:  inst.Host,
			Port:  inst.Port,
			State: model.StateHealthy,
		}
		adapter, err := adapters.New(inst)
		if err == nil {
			s.heartbeat[key] = NewHeartbeatChecker(adapter, config.Monitor.PingTimeout)
		}
	}

	// Load failover function from plugin
	plugin, err := operations.Get("ha_mgmt")
	if err == nil {
		s.failoverFunc = func(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
			return plugin.Execute(ctx, req)
		}
	}

	return s
}

func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	go s.loop(ctx)
}

func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
}

func (s *Supervisor) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
}

func (s *Supervisor) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
}

func (s *Supervisor) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *Supervisor) IsPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.paused
}

func (s *Supervisor) GetAllStatus() map[string]*model.InstanceHAStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*model.InstanceHAStatus)
	for k, v := range s.status {
		result[k] = v
	}
	return result
}

func (s *Supervisor) GetEvents() []model.FailoverEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.FailoverEvent, len(s.events))
	copy(result, s.events)
	return result
}

func (s *Supervisor) loop(ctx context.Context) {
	ticker := time.NewTicker(s.config.Monitor.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			paused := s.paused
			s.mu.RUnlock()

			if paused {
				continue
			}

			s.tick(ctx)
		}
	}
}

func (s *Supervisor) tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, inst := range s.instances {
		key := inst.Address()
		hb, exists := s.heartbeat[key]
		if !exists {
			continue
		}

		status := s.status[key]
		result := hb.Ping(ctx)
		status.LastCheck = time.Now()
		status.LastPingLatency = result.Latency

		if result.OK {
			// Healthy — reset failure counter
			status.ConsecutiveFails = 0
			status.State = model.StateHealthy
			continue
		}

		// Ping failed
		status.ConsecutiveFails++

		if status.ConsecutiveFails >= s.config.Monitor.ConsecutiveFailures {
			// Down — trigger auto failover
			s.handleInstanceDown(ctx, key, inst, status, result.Error)
		} else {
			// Unhealthy — alert but don't failover yet
			status.State = model.StateUnhealthy
			s.recordEvent(model.FailoverEvent{
				Timestamp: time.Now(),
				EventType: "instance_unhealthy",
				From:      key,
				Reason:    fmt.Sprintf("ping failed (%d/%d): %v", status.ConsecutiveFails, s.config.Monitor.ConsecutiveFailures, result.Error),
				Result:    "alert",
			})
		}
	}
}

func (s *Supervisor) handleInstanceDown(ctx context.Context, key string, inst model.InstanceConfig, status *model.InstanceHAStatus, pingErr error) {
	status.State = model.StateDown

	s.recordEvent(model.FailoverEvent{
		Timestamp: time.Now(),
		EventType: "instance_down",
		From:      key,
		Reason:    fmt.Sprintf("consecutive ping failures: %d, last error: %v", status.ConsecutiveFails, pingErr),
		Result:    "detected",
	})

	// Find a healthy replica to promote
	newMasterCfg := s.findHealthyReplica(ctx, key)
	if newMasterCfg == nil {
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_failed",
			From:      key,
			Reason:    "no healthy replica found for failover",
			Result:    "failed",
		})
		return
	}

	newMasterKey := newMasterCfg.Address()

	// Check cooldown
	if err := s.guard.CheckCooldown(key); err != nil {
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_failed",
			From:      key,
			Reason:    err.Error(),
			Result:    "cooldown",
		})
		return
	}

	// Verify new master is reachable
	if err := s.guard.VerifyNewMaster(ctx, *newMasterCfg); err != nil {
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_failed",
			From:      key,
			To:        newMasterKey,
			Reason:    fmt.Sprintf("new master verification failed: %v", err),
			Result:    "failed",
		})
		return
	}

	// Notify before failover
	if s.config.Failover.NotifyBefore {
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_start",
			From:      key,
			To:        newMasterKey,
			Reason:    "auto failover triggered",
			Result:    "starting",
		})
	}

	// Execute failover
	status.State = model.StateFailingOver

	dryRun := "false"
	if s.config.Failover.DryRun {
		dryRun = "true"
	}

	req := model.OperationRequest{
		Instance: inst,
		Params: map[string]string{
			"op":         "failover",
			"new_master": newMasterKey,
			"dry_run":    dryRun,
		},
	}

	result, err := s.failoverFunc(ctx, req)
	if err != nil || !result.Success {
		status.State = model.StateDown
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_failed",
			From:      key,
			To:        newMasterKey,
			Reason:    fmt.Sprintf("failover execution failed: %v, output: %s", err, result.RawOutput),
			Result:    "failed",
		})
		return
	}

	// Success
	s.guard.RecordFailover(key)
	status.LastFailover = time.Now()
	status.State = model.StateHealthy

	var warnings []string
	for _, w := range result.Warnings {
		warnings = append(warnings, w)
	}

	s.recordEvent(model.FailoverEvent{
		Timestamp: time.Now(),
		EventType: "failover_complete",
		From:      key,
		To:        newMasterKey,
		Reason:    "auto failover completed successfully",
		Result:    "success",
		Warnings:  warnings,
	})
}

func (s *Supervisor) findHealthyReplica(ctx context.Context, masterKey string) *model.InstanceConfig {
	for _, inst := range s.instances {
		key := inst.Address()
		if key == masterKey {
			continue
		}

		hb, exists := s.heartbeat[key]
		if !exists {
			continue
		}

		result := hb.Ping(ctx)
		if result.OK {
			return &inst
		}
	}
	return nil
}

func (s *Supervisor) recordEvent(event model.FailoverEvent) {
	s.events = append(s.events, event)
	if len(s.events) > s.maxEvents {
		s.events = s.events[len(s.events)-s.maxEvents:]
	}

	// Notify
	if s.notifier != nil {
		s.notifier.Notify(event)
	}
}
