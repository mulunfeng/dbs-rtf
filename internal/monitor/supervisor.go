package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
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

	heartbeat      map[string]*HeartbeatChecker
	guard          *FailoverGuard
	notifier       Notifier
	failoverFunc   func(ctx context.Context, req model.OperationRequest) (model.OperationResult, error)

	// activeMasterKey tracks the current writable master.
	// Set during NewSupervisor (first healthy node) and updated on each failover.
	activeMasterKey string

	// pendingDemotion marks a node that was down and is recovering.
	// Prevents repeated rejoin attempts on transient flapping.
	pendingDemotion map[string]bool

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
		instances:       instances,
		config:          config,
		status:          make(map[string]*model.InstanceHAStatus),
		maxEvents:       100,
		heartbeat:       make(map[string]*HeartbeatChecker),
		guard:           NewFailoverGuard(config.Failover.Cooldown),
		notifier:        notifier,
		pendingDemotion: make(map[string]bool),
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
			ctxInit, cancel := context.WithTimeout(context.Background(), config.Monitor.PingTimeout)
			if err := adapter.Connect(ctxInit); err == nil {
				s.heartbeat[key] = NewHeartbeatChecker(adapter, config.Monitor.PingTimeout)
			}
			cancel()
		}
	}

	// Load failover function from plugin
	plugin, err := operations.Get("ha_mgmt")
	if err == nil {
		s.failoverFunc = func(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
			return plugin.Execute(ctx, req)
		}
	}

	// Determine the initial active master by checking read_only status.
	// The node with read_only=OFF is the current writable master.
	// If all nodes are read_only or we can't determine, fall back to first healthy.
	ctxInit, cancelInit := context.WithTimeout(context.Background(), config.Monitor.PingTimeout)
	s.detectActiveMaster(ctxInit)
	cancelInit()
	return s
}

func (s *Supervisor) detectActiveMaster(ctx context.Context) {
	var fallback string
	for _, inst := range s.instances {
		key := inst.Address()
		hb, hasHB := s.heartbeat[key]
		if !hasHB {
			continue
		}
		if fallback == "" {
			fallback = key
		}
		// Check read_only status
		vars, err := hb.adapter.GetVariables(ctx, "read_only")
		if err != nil {
			continue
		}
		readOnly := vars["read_only"]
		superReadOnly := vars["super_read_only"]
		if readOnly != "ON" && superReadOnly != "ON" {
			s.activeMasterKey = key
			log.Printf("[HA] detected writable master: %s (read_only=%s, super_read_only=%s)", key, readOnly, superReadOnly)
			return
		}
	}
	if fallback != "" {
		s.activeMasterKey = fallback
		log.Printf("[HA] WARNING: could not detect writable master by read_only status, using first healthy: %s", fallback)
	}
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

// SyncProxy notifies the proxy about the current active master on startup,
// ensuring the proxy is pointing to the correct writable node.
func (s *Supervisor) SyncProxy() {
	s.mu.RLock()
	masterKey := s.activeMasterKey
	proxyURL := s.config.Failover.ProxySwitchURL
	s.mu.RUnlock()

	if proxyURL == "" || masterKey == "" {
		return
	}
	log.Printf("[HA] syncing proxy to master %s on startup", masterKey)
	s.notifyProxy(masterKey)
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
		wasDown := (status.State == model.StateDown)
		result := hb.Ping(ctx)
		status.LastCheck = time.Now()
		status.LastPingLatency = result.Latency

		if result.OK {
			// Node recovered from down state — check if it needs demotion
			if wasDown && key != s.activeMasterKey && s.activeMasterKey != "" {
				// This node was down but is not the current master.
				// It was the old master before a failover — demote it to replica.
				if !s.pendingDemotion[key] {
					s.pendingDemotion[key] = true
					// Release lock to avoid deadlock during adapter operations
					s.mu.Unlock()
					s.demoteAndRejoin(ctx, inst, s.activeMasterKey)
					s.mu.Lock()
				}
				continue
			}

			// Normal healthy recovery
			status.ConsecutiveFails = 0
			status.State = model.StateHealthy
			delete(s.pendingDemotion, key)
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

	// Execute failover — create a fresh adapter for the replica being promoted
	newAdapter, err := adapters.New(*newMasterCfg)
	if err != nil {
		status.State = model.StateDown
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_failed",
			From:      key,
			To:        newMasterKey,
			Reason:    fmt.Sprintf("failed to create adapter for new master: %v", err),
			Result:    "failed",
		})
		return
	}
	ctxConnect, cancelConnect := context.WithTimeout(ctx, s.config.Monitor.PingTimeout)
	if err := newAdapter.Connect(ctxConnect); err != nil {
		cancelConnect()
		status.State = model.StateDown
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(),
			EventType: "failover_failed",
			From:      key,
			To:        newMasterKey,
			Reason:    fmt.Sprintf("failed to connect to new master: %v", err),
			Result:    "failed",
		})
		return
	}
	cancelConnect()

	status.State = model.StateFailingOver

	dryRun := "false"
	if s.config.Failover.DryRun {
		dryRun = "true"
	}

	req := model.OperationRequest{
		Instance: inst,
		Adapter:  newAdapter,
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

	// Update active master — the promoted replica is now the master
	s.activeMasterKey = newMasterKey

	// Notify proxy to switch traffic to the new master
	if s.config.Failover.ProxySwitchURL != "" {
		go s.notifyProxy(newMasterKey)
	}
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

func (s *Supervisor) notifyProxy(newMaster string) {
	// Reverse lookup: find container name from address
	containerName := s.config.Failover.MasterHostnameMap[newMaster]
	port := "3306"
	if containerName == "" {
		// Fallback: parse host:port from the address
		host, p := parseHostPort(newMaster)
		containerName = host
		port = p
	}
	body, _ := json.Marshal(map[string]string{"host": containerName, "port": port})
	resp, err := http.Post(s.config.Failover.ProxySwitchURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[HA] proxy switch notification failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		log.Printf("[HA] proxy switched to %s (%s) successfully", containerName, newMaster)
	} else {
		log.Printf("[HA] proxy switch returned status %d", resp.StatusCode)
	}
}

func (s *Supervisor) demoteAndRejoin(ctx context.Context, recoveredInst model.InstanceConfig, newMasterKey string) {
	recoveredKey := recoveredInst.Address()
	log.Printf("[HA] detected recovered node %s — attempting demote to replica of %s", recoveredKey, newMasterKey)

	s.recordEvent(model.FailoverEvent{
		Timestamp: time.Now(),
		EventType: "instance_rejoining",
		From:      recoveredKey,
		To:        newMasterKey,
		Reason:    "old master recovered, reconfiguring as replica",
		Result:    "demoting",
	})

	// Connect to the recovered node
	recoveredAdapter, err := adapters.New(recoveredInst)
	if err != nil {
		log.Printf("[HA] failed to create adapter for recovered node %s: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("create adapter: %v", err), Result: "failed",
		})
		return
	}
	ctxConn, cancel := context.WithTimeout(ctx, s.config.Monitor.PingTimeout)
	if err := recoveredAdapter.Connect(ctxConn); err != nil {
		cancel()
		log.Printf("[HA] failed to connect to recovered node %s: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("connect: %v", err), Result: "failed",
		})
		return
	}
	cancel()

	// Parse new master host and port
	masterHost, masterPort := parseHostPort(newMasterKey)
	// Use Docker hostname if mapped (containers can't reach 127.0.0.1 on host ports)
	if mappedHost, ok := s.config.Failover.MasterHostnameMap[newMasterKey]; ok {
		masterHost = mappedHost
		masterPort = "3306"
	}
	if masterHost == "" {
		masterHost = recoveredInst.Host
	}
	if masterPort == "0" {
		masterPort = "3306"
	}

	// Step 1: Stop any existing replication
	log.Printf("[HA] [%s] Step 1: STOP SLAVE", recoveredKey)
	if _, err := recoveredAdapter.Exec(ctx, "STOP SLAVE"); err != nil {
		log.Printf("[HA] [%s] STOP SLAVE (may not have been a replica): %v", recoveredKey, err)
	}

	// Step 2: Reset slave configuration
	log.Printf("[HA] [%s] Step 2: RESET SLAVE ALL", recoveredKey)
	if _, err := recoveredAdapter.Exec(ctx, "RESET SLAVE ALL"); err != nil {
		log.Printf("[HA] [%s] RESET SLAVE ALL failed: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("RESET SLAVE ALL: %v", err), Result: "failed",
		})
		return
	}

	// Step 2.5: Sync data from new master via mysqldump
	// GTID-only approach (set gtid_purged) causes data divergence errors (1032/1062)
	// because the recovered master has its own writes as former primary.
	// The only safe way is to re-provision the recovered node from the new master.
	log.Printf("[HA] [%s] Step 2.5: Re-provisioning data from new master %s via mysqldump", recoveredKey, newMasterKey)
	if err := s.reprovisionFromMaster(ctx, newMasterKey, recoveredInst); err != nil {
		log.Printf("[HA] [%s] re-provision failed: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("re-provision from master: %v", err), Result: "failed",
		})
		return
	}

	// Step 2.6: Connect fresh adapter to recovered node after dump + load
	recoveredAdapter.Close()
	recoveredAdapter, err = adapters.New(recoveredInst)
	if err != nil {
		log.Printf("[HA] [%s] failed to create new adapter after re-provision: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("re-create adapter: %v", err), Result: "failed",
		})
		return
	}
	ctxConn, cancel = context.WithTimeout(ctx, s.config.Monitor.PingTimeout)
	if err := recoveredAdapter.Connect(ctxConn); err != nil {
		cancel()
		log.Printf("[HA] [%s] failed to reconnect after re-provision: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("re-connect: %v", err), Result: "failed",
		})
		return
	}
	cancel()

	// Step 2.7: Install and enable semi-sync slave plugin
	log.Printf("[HA] [%s] Step 2.7: enabling semi-sync replication (slave side)", recoveredKey)
	if err := setupSemiSyncSlave(ctx, recoveredAdapter); err != nil {
		log.Printf("[HA] [%s] semi-sync slave setup (non-fatal): %v", recoveredKey, err)
	}

	// Step 3: Configure replication to new master
	// Use CHANGE MASTER TO syntax (MySQL 5.7/8.0 compatible)
	changeMasterSQL := fmt.Sprintf(
		"CHANGE MASTER TO MASTER_HOST='%s', MASTER_PORT=%s, MASTER_USER='root', MASTER_PASSWORD='rootpass123', MASTER_AUTO_POSITION=1",
		masterHost, masterPort,
	)
	log.Printf("[HA] [%s] Step 3: CHANGE MASTER TO %s", recoveredKey, newMasterKey)
	if _, err := recoveredAdapter.Exec(ctx, changeMasterSQL); err != nil {
		log.Printf("[HA] [%s] CHANGE MASTER TO failed: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("CHANGE MASTER TO: %v", err), Result: "failed",
		})
		return
	}

	// Step 4: Set read-only mode
	// MySQL requires read_only=ON before super_read_only=ON.
	// Replication SQL thread is exempt from both (kernel-level exemption),
	// so enabling super_read_only on the replica is safe and recommended.
	log.Printf("[HA] [%s] Step 4: SET read_only=ON, super_read_only=ON", recoveredKey)
	if err := recoveredAdapter.SetVariable(ctx, "read_only", "ON", model.ScopeGlobal); err != nil {
		log.Printf("[HA] [%s] SET read_only=ON failed: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("SET read_only=ON: %v", err), Result: "failed",
		})
		return
	}
	if err := recoveredAdapter.SetVariable(ctx, "super_read_only", "ON", model.ScopeGlobal); err != nil {
		log.Printf("[HA] [%s] SET super_read_only=ON failed: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("SET super_read_only=ON: %v", err), Result: "failed",
		})
		return
	}

	// Step 5: Start replication
	log.Printf("[HA] [%s] Step 5: START SLAVE", recoveredKey)
	if _, err := recoveredAdapter.Exec(ctx, "START SLAVE"); err != nil {
		log.Printf("[HA] [%s] START SLAVE failed: %v", recoveredKey, err)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_failed", From: recoveredKey,
			Reason: fmt.Sprintf("START SLAVE: %v", err), Result: "failed",
		})
		return
	}

	// Step 6: Verify replication
	time.Sleep(2 * time.Second)
	status, err := recoveredAdapter.GetReplicationStatus(ctx)
	if err != nil {
		log.Printf("[HA] [%s] failed to verify replication: %v", recoveredKey, err)
	} else if status.IOState == "Yes" && status.SQLState == "Yes" {
		log.Printf("[HA] [%s] demotion complete — now replicating from %s (lag: %ds)", recoveredKey, newMasterKey, status.SecondsBehind)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_complete", From: recoveredKey,
			To: newMasterKey,
			Reason: fmt.Sprintf("replication established, lag=%ds", status.SecondsBehind),
			Result: "success",
		})

		s.mu.Lock()
		if s.status[recoveredKey] != nil {
			s.status[recoveredKey].State = model.StateHealthy
		}
		delete(s.pendingDemotion, recoveredKey)
		s.mu.Unlock()
	} else {
		log.Printf("[HA] [%s] replication not healthy: IO=%s SQL=%s", recoveredKey, status.IOState, status.SQLState)
		s.recordEvent(model.FailoverEvent{
			Timestamp: time.Now(), EventType: "rejoin_warning", From: recoveredKey,
			Reason: fmt.Sprintf("replication not healthy: IO=%s SQL=%s", status.IOState, status.SQLState),
			Result: "warning",
		})
	}
}

func (s *Supervisor) getMasterGTIDSet(ctx context.Context, masterKey string) (string, error) {
	for _, inst := range s.instances {
		if inst.Address() == masterKey {
			masterAdapter, err := adapters.New(inst)
			if err != nil {
				return "", fmt.Errorf("create adapter for master %s: %w", masterKey, err)
			}
			ctxConn, cancel := context.WithTimeout(ctx, s.config.Monitor.PingTimeout)
			if err := masterAdapter.Connect(ctxConn); err != nil {
				cancel()
				return "", fmt.Errorf("connect to master %s: %w", masterKey, err)
			}
			cancel()
			defer masterAdapter.Close()

			return masterAdapter.GetExecutedGTIDSet(ctx)
		}
	}
	return "", fmt.Errorf("master instance %s not found in config", masterKey)
}

// reprovisionFromMaster re-provisions the recovered node by streaming a mysqldump
// from the new master. This is the only reliable way to handle data divergence
// between an old master and its replacement after failover.
func (s *Supervisor) reprovisionFromMaster(ctx context.Context, newMasterKey string, recoveredInst model.InstanceConfig) error {
	masterName := ""
	recoveredName := recoveredInst.Name
	for _, inst := range s.instances {
		if inst.Address() == newMasterKey {
			masterName = inst.Name
			break
		}
	}
	if masterName == "" {
		return fmt.Errorf("master instance %s not found in config", newMasterKey)
	}

	// Step 1: Reset recovered node and disable read-only
	log.Printf("[HA] re-provisioning: resetting %s and disabling read-only", recoveredName)
	resetCmd := exec.CommandContext(ctx, "docker", "exec", recoveredName,
		"mysql", "-u", "root", "-prootpass123",
		"-e", "INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'; STOP SLAVE; RESET SLAVE ALL; RESET MASTER; SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF; SET sql_log_bin=0;")
	if out, err := resetCmd.CombinedOutput(); err != nil {
		log.Printf("[HA] reset on %s: %v (output: %s)", recoveredName, err, string(out))
	}

	// Step 2: Dump from master with --set-gtid-purged=ON
	// This embeds the snapshot GTID set inside the dump, so the recovered node
	// knows exactly which transactions it already has. Using --set-gtid-purged=ON
	// is correct because the dump's data matches the GTID set at snapshot time.
	log.Printf("[HA] re-provisioning: dumping data from %s (GTID-consistent snapshot)", masterName)
	dumpCmd := exec.CommandContext(ctx, "docker", "exec", masterName,
		"mysqldump", "--single-transaction", "--all-databases", "--triggers",
		"--routines", "--events", "--set-gtid-purged=ON",
		"-u", "root", "-prootpass123")
	var dumpBuf bytes.Buffer
	dumpCmd.Stdout = &dumpBuf
	dumpCmd.Stderr = os.Stderr
	if err := dumpCmd.Run(); err != nil {
		return fmt.Errorf("mysqldump from %s: %v", masterName, err)
	}

	log.Printf("[HA] re-provisioning: dump complete (%d bytes), loading into %s", dumpBuf.Len(), recoveredName)

	// Step 3: Load dump into recovered node with binary logging OFF
	// so the loaded rows don't generate new GTIDs on this node
	log.Printf("[HA] re-provisioning: loading data into %s (with SET sql_log_bin=0)", recoveredName)
	loadScript := "SET sql_log_bin=0;\n" + dumpBuf.String()
	loadCmd := exec.CommandContext(ctx, "docker", "exec", "-i", recoveredName,
		"mysql", "-u", "root", "-prootpass123")
	loadCmd.Stdin = strings.NewReader(loadScript)
	var loadOut bytes.Buffer
	loadCmd.Stdout = &loadOut
	loadCmd.Stderr = &loadOut
	if err := loadCmd.Run(); err != nil {
		return fmt.Errorf("mysql load into %s: %v (output: %s)", recoveredName, err, loadOut.String())
	}

	log.Printf("[HA] re-provisioning complete: %s now has %s's data", recoveredName, masterName)

	// Set both read_only and super_read_only on the recovered node.
	// Replication SQL thread is exempt from these restrictions at the kernel level,
	// so enabling super_read_only is safe.
	log.Printf("[HA] re-provisioning: setting read_only=ON, super_read_only=ON on %s", recoveredName)
	resetCmd2 := exec.CommandContext(ctx, "docker", "exec", recoveredName,
		"mysql", "-u", "root", "-prootpass123",
		"-e", "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;")
	if out, err := resetCmd2.CombinedOutput(); err != nil {
		log.Printf("[HA] WARNING: failed to set read_only on %s: %v (output: %s)", recoveredName, err, string(out))
	}

	return nil
}

// setupSemiSyncSlave installs and enables semi-sync replication on the slave side.
// The plugin must already be present in the MySQL plugin directory (default in MySQL images).
func setupSemiSyncSlave(ctx context.Context, adapter adapters.DatabaseAdapter) error {
	// Install plugin (fails silently if already installed)
	_, _ = adapter.Exec(ctx, "INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'")

	// Enable semi-sync slave
	if err := adapter.SetVariable(ctx, "rpl_semi_sync_slave_enabled", "ON", model.ScopeGlobal); err != nil {
		return fmt.Errorf("enable rpl_semi_sync_slave_enabled: %v", err)
	}

	// Force restart of slave replication to pick up semi-sync
	if _, err := adapter.Exec(ctx, "STOP SLAVE"); err != nil {
		// May not have been running yet, ignore
	}
	if _, err := adapter.Exec(ctx, "START SLAVE"); err != nil {
		return fmt.Errorf("START SLAVE after semi-sync: %v", err)
	}

	return nil
}

func parseHostPort(addr string) (host, port string) {
	host = addr
	port = "3306"
	if idx := len(addr); idx > 0 {
		for i := idx - 1; i >= 0; i-- {
			if addr[i] == ':' {
				host = addr[:i]
				port = addr[i+1:]
				break
			}
		}
	}
	return
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
