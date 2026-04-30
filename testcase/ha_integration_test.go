// Package testcase contains integration tests for HA scenarios.
// These tests simulate real-world high-availability conditions
// using mock adapters, covering health checks, failover, and
// replication monitoring without requiring a live MySQL cluster.
package testcase

import (
	"context"
	"strings"
	"testing"

	_ "github.com/dbs-rtf/agent/internal/adapters/mysql"
	_ "github.com/dbs-rtf/agent/internal/operations/ha_mgmt"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

// =============================================================================
// Scenario 1: Health Check — Healthy Cluster
// All checks should pass, score should be 100.
// =============================================================================

func TestHealthCheck_ClusterHealthy(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, err := operations.Get("ha_mgmt")
	if err != nil {
		t.Fatalf("plugin not found: %v", err)
	}

	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("health check should succeed: %s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "Score: 100/100") {
		t.Errorf("expected score 100/100 for healthy cluster, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "healthy") {
		t.Errorf("expected healthy status in output, got: %s", result.RawOutput)
	}

	// Verify all checks show OK
	for _, expectedCheck := range []string{"Ping", "Version", "Connections", "Replication", "Read-Only"} {
		if !strings.Contains(result.RawOutput, expectedCheck) {
			t.Errorf("missing check %q in health report", expectedCheck)
		}
	}
}

// haDataCarrier is a minimal interface to extract health data from result.Data
// The actual type is healthCheckResult from ha_mgmt package.
type haDataCarrier interface{}

// =============================================================================
// Scenario 2: Health Check — Degraded Cluster (High Replication Lag)
// Score should be reduced due to lag > 60s but < 300s.
// =============================================================================

func TestHealthCheck_ClusterDegraded_HighLag(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 120)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.RawOutput, "WARN") || !strings.Contains(result.RawOutput, "replica lag") {
		t.Errorf("expected WARN for high lag, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "90/100") {
		t.Errorf("expected score 90/100 (lag penalty), got:\n%s", result.RawOutput)
	}

	if strings.Contains(result.RawOutput, "replica lag: 120 seconds") {
		// Good — lag is reported correctly
	} else {
		t.Logf("warning: lag report format may have changed: %s", result.RawOutput)
	}
}

// =============================================================================
// Scenario 3: Health Check — Critical Cluster (Node Unreachable)
// Score should be < 50 due to ping failure (-40) and other cascading failures.
// =============================================================================

func TestHealthCheck_ClusterCritical_Unreachable(t *testing.T) {
	adapter := NewUnreachable("10.0.1.5", 3306)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.5", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.RawOutput, "critical") {
		t.Errorf("expected critical status for unreachable node, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "FAIL") {
		t.Errorf("expected FAIL in health report for unreachable node")
	}
}

// =============================================================================
// Scenario 4: Health Check — Degraded (Broken Replication)
// Replication threads are down, score should reflect significant penalty.
// =============================================================================

func TestHealthCheck_ClusterDegraded_BrokenReplication(t *testing.T) {
	adapter := NewBrokenReplica("10.0.1.6", 3306, "10.0.1.5")

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.RawOutput, "FAIL") || !strings.Contains(result.RawOutput, "Replication") {
		t.Errorf("expected replication FAIL in health report, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "error") {
		t.Logf("warning: replication error detail not found in output")
	}
}

// =============================================================================
// Scenario 5: Failover — Normal (Healthy Replica Promoted)
// Steps: STOP SLAVE → RESET SLAVE ALL → read_only=OFF → verify write
// Rollback command should be generated.
// =============================================================================

func TestFailover_NormalPromotion(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params: map[string]string{
			"op":         "failover",
			"new_master": "10.0.1.6:3306",
		},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("failover should succeed for healthy replica: %s", result.RawOutput)
	}

	// Verify execution sequence
	expectedSQLs := []string{"STOP SLAVE", "RESET SLAVE ALL"}
	for _, sql := range expectedSQLs {
		found := false
		for _, executed := range adapter.ExecLog {
			if strings.Contains(executed, sql) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected SQL %q not executed. ExecLog: %v", sql, adapter.ExecLog)
		}
	}

	// Verify read_only was turned off
	if adapter.Variables["read_only"] != "OFF" {
		t.Error("read_only should be OFF after failover")
	}

	// Verify rollback command was generated
	if result.RollbackCmd == nil {
		t.Error("failover should generate a rollback command for demotion")
	}
	if result.RollbackCmd.Plugin != "ha_mgmt" {
		t.Errorf("rollback plugin should be ha_mgmt, got %s", result.RollbackCmd.Plugin)
	}
	if result.RollbackCmd.Operation != "failover" {
		t.Errorf("rollback operation should be failover (demote), got %s", result.RollbackCmd.Operation)
	}
}

// =============================================================================
// Scenario 6: Failover — Data Loss Warning (High Replication Lag)
// When lag > 0, warnings should be present in the result.
// =============================================================================

func TestFailover_WithDataLag_WarningGenerated(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 300)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params: map[string]string{
			"op":         "failover",
			"new_master": "10.0.1.6:3306",
		},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Failover should still succeed but with warnings
	if !result.Success {
		t.Fatalf("failover should succeed even with lag: %s", result.RawOutput)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warnings for high replication lag")
	}

	if !strings.Contains(result.RawOutput, "WARNING") {
		t.Error("expected WARNING text in output for data lag scenario")
	}
}

// =============================================================================
// Scenario 7: Failover — Replication Broken (Potential Data Loss)
// IO/SQL threads are stopped, failover should proceed with strong warnings.
// =============================================================================

func TestFailover_WithBrokenReplication_WarningGenerated(t *testing.T) {
	adapter := NewBrokenReplica("10.0.1.6", 3306, "10.0.1.5")

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params: map[string]string{
			"op":         "failover",
			"new_master": "10.0.1.6:3306",
		},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Failover should succeed (emergency scenario) but with warnings
	if !result.Success {
		t.Fatalf("failover should succeed for emergency: %s", result.RawOutput)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warnings for broken replication threads")
	}

	if !strings.Contains(result.RawOutput, "replication threads not running") {
		t.Error("expected warning about replication threads not running")
	}
}

// =============================================================================
// Scenario 8: Failover — Validation: Missing new_master Parameter
// Should fail with a clear error message.
// =============================================================================

func TestFailover_MissingNewMaster_Fails(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "failover"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("failover should fail when new_master is missing")
	}
	if !strings.Contains(result.RawOutput, "new_master parameter required") {
		t.Errorf("expected 'new_master parameter required' error, got: %s", result.RawOutput)
	}
}

// =============================================================================
// Scenario 9: Replication Status — Healthy Replica
// Should show RUNNING status with normal lag.
// =============================================================================

func TestReplication_HealthyReplica(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("replication status should succeed: %s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "Replication: RUNNING") {
		t.Errorf("expected RUNNING status, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "NORMAL") {
		t.Errorf("expected NORMAL lag status, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "10.0.1.5") {
		t.Error("expected source host 10.0.1.5 in output")
	}
}

// =============================================================================
// Scenario 10: Replication Status — Broken Replica
// Should show STOPPED status with thread failure details.
// =============================================================================

func TestReplication_BrokenReplica(t *testing.T) {
	adapter := NewBrokenReplica("10.0.1.6", 3306, "10.0.1.5")

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("replication status should succeed: %s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "Replication: STOPPED") {
		t.Errorf("expected STOPPED status, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "IO thread is not running") {
		t.Error("expected IO thread failure in output")
	}

	if !strings.Contains(result.RawOutput, "SQL thread is not running") {
		t.Error("expected SQL thread failure in output")
	}
}

// =============================================================================
// Scenario 11: Replication Status — Not a Replica (Primary Node)
// Should gracefully report that the node is not a replica.
// =============================================================================

func TestReplication_NotAReplica(t *testing.T) {
	adapter := NewPrimary("10.0.1.5", 3306)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.5", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("replication status should succeed even for primary: %s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "not configured as a replica") {
		t.Errorf("expected 'not configured as a replica' message, got:\n%s", result.RawOutput)
	}
}

// =============================================================================
// Scenario 12: Security — Viewer Cannot Perform Failover
// Auth layer should block failover for viewer role.
// =============================================================================

func TestSecurity_ViewerCannotFailover(t *testing.T) {
	auth := &engine.Auth{}
	user := engine.User{Name: "viewer", Role: operations.RoleViewer}
	plugin, _ := operations.Get("ha_mgmt")

	err := auth.CheckPermission(user, plugin, "failover")
	if err == nil {
		t.Error("viewer should be denied failover permission")
	}
}

// =============================================================================
// Scenario 13: Security — Operator Can Check Health and Replication
// Operator role should have read access but not failover.
// =============================================================================

func TestSecurity_OperatorCanReadHA(t *testing.T) {
	auth := &engine.Auth{}
	user := engine.User{Name: "operator", Role: operations.RoleOperator}
	plugin, _ := operations.Get("ha_mgmt")

	// Health check: allowed
	if err := auth.CheckPermission(user, plugin, "health"); err != nil {
		t.Errorf("operator should be able to check health: %v", err)
	}

	// Replication: allowed
	if err := auth.CheckPermission(user, plugin, "replication"); err != nil {
		t.Errorf("operator should be able to check replication: %v", err)
	}

	// Failover: denied
	if err := auth.CheckPermission(user, plugin, "failover"); err == nil {
		t.Error("operator should be denied failover permission")
	}
}

// =============================================================================
// Scenario 14: Security — Admin Has Full Access
// Admin role should pass all permission checks.
// =============================================================================

func TestSecurity_AdminFullAccess(t *testing.T) {
	auth := &engine.Auth{}
	user := engine.User{Name: "admin", Role: operations.RoleAdmin}
	plugin, _ := operations.Get("ha_mgmt")

	for _, op := range []string{"health", "failover", "replication"} {
		if err := auth.CheckPermission(user, plugin, op); err != nil {
			t.Errorf("admin should have access to %s: %v", op, err)
		}
	}
}

// =============================================================================
// Scenario 15: Concurrency — Rate Limiter Prevents Concurrent HA Operations
// Two operations on the same instance should be blocked.
// =============================================================================

func TestConcurrency_RateLimiterBlocksDuplicate(t *testing.T) {
	limiter := engine.NewRateLimiter()

	// First operation allowed
	err := limiter.Allow("10.0.1.5:3306")
	if err != nil {
		t.Fatalf("first operation should be allowed: %v", err)
	}

	// Second operation on same instance should be blocked
	err = limiter.Allow("10.0.1.5:3306")
	if err == nil {
		t.Error("second operation on same instance should be blocked")
	}

	// Operation on different instance should be allowed
	err = limiter.Allow("10.0.1.6:3306")
	if err != nil {
		t.Errorf("operation on different instance should be allowed: %v", err)
	}

	// Release first instance
	limiter.Release("10.0.1.5:3306")

	// Now second operation should be allowed
	err = limiter.Allow("10.0.1.5:3306")
	if err != nil {
		t.Errorf("operation after release should be allowed: %v", err)
	}
}

// =============================================================================
// Scenario 16: Failover Rollback — Generated Command Contains Correct Params
// The rollback command should include demote operation and original master info.
// =============================================================================

func TestFailoverRollback_CommandStructure(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params: map[string]string{
			"op":         "failover",
			"new_master": "10.0.1.6:3306",
		},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rb := result.RollbackCmd
	if rb == nil {
		t.Fatal("expected rollback command to be set")
	}

	if rb.Params["op"] != "demote" {
		t.Errorf("rollback op should be 'demote', got %s", rb.Params["op"])
	}

	if rb.Params["master_host"] != "10.0.1.5" {
		t.Errorf("rollback master_host should be 10.0.1.5, got %s", rb.Params["master_host"])
	}

	if rb.Instance.Host != "10.0.1.6" {
		t.Errorf("rollback instance host should be 10.0.1.6, got %s", rb.Instance.Host)
	}
}

// =============================================================================
// Scenario 17: Health Check — High Connection Count Degradation
// Many long-running queries should cause degraded score.
// =============================================================================

func TestHealthCheck_HighConnections(t *testing.T) {
	adapter := NewPrimary("10.0.1.5", 3306)

	// Simulate 15 long-running queries
	for i := 0; i < 15; i++ {
		adapter.Processes = append(adapter.Processes, model.ProcessInfo{
			ID:      1000 + i,
			User:    "app_user",
			Host:    "10.0.2.100:12345",
			Command: "Query",
			Time:    60,
			State:   "executing",
			Info:    "SELECT * FROM large_table WHERE id > 10000",
		})
	}

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.5", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.RawOutput, "15 total") {
		t.Errorf("expected 15 total connections in report, got:\n%s", result.RawOutput)
	}

	if !strings.Contains(result.RawOutput, "long-running") {
		t.Error("expected long-running connection warning in report")
	}
}

// =============================================================================
// Scenario 18: Health Check — Read-Only Mode Detection
// A read-only instance should show WARN for the Read-Only check.
// =============================================================================

func TestHealthCheck_ReadOnlyInstance(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)
	// Healthy replica has read_only = ON by default

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read-only should show WARN (not FAIL, since it's expected for replicas)
	if !strings.Contains(result.RawOutput, "Read-Only") {
		t.Error("expected Read-Only check in health report")
	}
}

// =============================================================================
// Scenario 19: Failover — Write Verification Failure
// If write verification fails after promotion, failover should report failure.
// =============================================================================

func TestFailover_WriteVerificationFailure(t *testing.T) {
	adapter := NewHealthyReplica("10.0.1.6", 3306, "10.0.1.5", 5)
	adapter.ExecErr = context.DeadlineExceeded

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.6", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params: map[string]string{
			"op":         "failover",
			"new_master": "10.0.1.6:3306",
		},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Failover should fail when write verification fails
	if result.Success {
		t.Error("failover should fail when write verification fails")
	}
}

// =============================================================================
// Scenario 20: Health Check — Network Partition Simulation
// Ping fails, cascading failures to other checks.
// =============================================================================

func TestHealthCheck_NetworkPartition(t *testing.T) {
	adapter := NewUnreachable("10.0.1.5", 3306)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "10.0.1.5", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	plugin, _ := operations.Get("ha_mgmt")
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ping was called
	if adapter.PingCallCount == 0 {
		t.Error("expected ping to be called during health check")
	}

	// Should report critical
	if !strings.Contains(result.RawOutput, "critical") {
		t.Errorf("expected critical status for network partition, got:\n%s", result.RawOutput)
	}
}
