package ha_mgmt

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbs-rtf/agent/pkg/model"
)

func failover(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := getAdapter(req)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	newMasterHost := req.Params["new_master"]
	if newMasterHost == "" {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "new_master parameter required (format: host:port)"
		return result, nil
	}

	var sb strings.Builder
	var warnings []string

	// Step 1: Check current replication status
	status, err := adapter.GetReplicationStatus(ctx)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("unable to get replication status: %v", err)
		return result, nil
	}

	if !status.IsReplica {
		warnings = append(warnings, "current instance is not a replica — verify this is the correct node to promote")
	}

	sb.WriteString("=== Failover Pre-Check ===\n")
	sb.WriteString(fmt.Sprintf("IO Thread:       %s\n", status.IOState))
	sb.WriteString(fmt.Sprintf("SQL Thread:      %s\n", status.SQLState))
	sb.WriteString(fmt.Sprintf("Seconds Behind:  %d\n", status.SecondsBehind))
	sb.WriteString(fmt.Sprintf("New Master:      %s\n", newMasterHost))
	sb.WriteString("\n")

	if status.IOState != "Yes" || status.SQLState != "Yes" {
		warnings = append(warnings, "replication threads not running — potential data loss")
		sb.WriteString(fmt.Sprintf("WARNING: %s\n\n", warnings[len(warnings)-1]))
	}

	if status.SecondsBehind > 60 {
		warnings = append(warnings, fmt.Sprintf("replication lag %d seconds — up to %d seconds of data may be lost", status.SecondsBehind, status.SecondsBehind))
		sb.WriteString(fmt.Sprintf("WARNING: %s\n\n", warnings[len(warnings)-1]))
	}

	// Dry run: return pre-check results without executing
	if req.Params["dry_run"] == "true" {
		sb.WriteString("=== Dry Run Complete ===\n")
		sb.WriteString("No changes were made. Remove dry_run=true to execute failover.\n")

		var result model.OperationResult
		result.Success = true
		result.Warnings = warnings
		result.RawOutput = sb.String()
		return result, nil
	}

	// Step 2: Stop replication
	sb.WriteString("Step 1: Stopping replication...\n")
	if _, err := adapter.Exec(ctx, "STOP SLAVE"); err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = sb.String() + fmt.Sprintf("\nFailed to STOP SLAVE: %v", err)
		return result, nil
	}
	sb.WriteString("  STOP SLAVE — OK\n")

	// Step 3: Reset slave
	sb.WriteString("Step 2: Resetting slave configuration...\n")
	if _, err := adapter.Exec(ctx, "RESET SLAVE ALL"); err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = sb.String() + fmt.Sprintf("\nFailed to RESET SLAVE ALL: %v", err)
		return result, nil
	}
	sb.WriteString("  RESET SLAVE ALL — OK\n")

	// Step 4: Disable read-only (super_read_only must go first)
	sb.WriteString("Step 3: Disabling read-only mode...\n")
	if err := adapter.SetVariable(ctx, "super_read_only", "OFF", model.ScopeGlobal); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to set super_read_only=OFF: %v", err))
		sb.WriteString(fmt.Sprintf("  SET super_read_only=OFF — FAILED: %v\n", err))
	} else {
		sb.WriteString("  SET super_read_only=OFF — OK\n")
	}
	if err := adapter.SetVariable(ctx, "read_only", "OFF", model.ScopeGlobal); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to set read_only=OFF: %v", err))
		sb.WriteString(fmt.Sprintf("  SET read_only=OFF — FAILED: %v\n", err))
	} else {
		sb.WriteString("  SET read_only=OFF — OK\n")
	}

	// Step 5: Verify write capability
	sb.WriteString("Step 4: Verifying write capability...\n")
	_, err = adapter.Exec(ctx, "SELECT 1")
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = sb.String() + fmt.Sprintf("\nWrite verification failed: %v", err)
		return result, nil
	}
	sb.WriteString("  Write verification — OK\n\n")

	// Step 6: Enable semi-sync master replication
	sb.WriteString("Step 5: Enabling semi-sync master replication...\n")
	if _, err := adapter.Exec(ctx, "INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'"); err != nil {
		warnings = append(warnings, fmt.Sprintf("rpl_semi_sync_master install (may already exist): %v", err))
	} else {
		sb.WriteString("  INSTALL PLUGIN rpl_semi_sync_master — OK\n")
	}
	if err := adapter.SetVariable(ctx, "rpl_semi_sync_master_enabled", "ON", model.ScopeGlobal); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to enable rpl_semi_sync_master_enabled: %v", err))
		sb.WriteString(fmt.Sprintf("  SET rpl_semi_sync_master_enabled=ON — FAILED: %v\n", err))
	} else {
		sb.WriteString("  SET rpl_semi_sync_master_enabled=ON — OK\n")
	}
	// Set timeout to 5s — if no slave acknowledges within 5s, fall back to async
	if _, err := adapter.Exec(ctx, "SET GLOBAL rpl_semi_sync_master_timeout=5000"); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to set rpl_semi_sync_master_timeout: %v", err))
	} else {
		sb.WriteString("  SET rpl_semi_sync_master_timeout=5000 — OK\n")
	}
	sb.WriteString("\n")

	// Build rollback command
	rollbackCmd := &model.Command{
		Plugin:    "ha_mgmt",
		Operation: "failover",
		Params: map[string]string{
			"op":             "demote",
			"master_host":    status.SourceHost,
			"master_port":    fmt.Sprintf("%d", status.SourcePort),
			"previous_master": newMasterHost,
		},
		Instance: req.Instance,
	}

	sb.WriteString("=== Failover Complete ===\n")
	sb.WriteString(fmt.Sprintf("Instance %s is now the new master.\n", req.Instance.Address()))
	sb.WriteString(fmt.Sprintf("Run 'CHANGE MASTER TO MASTER_HOST='%s'' on other replicas.\n", newMasterHost))

	if len(warnings) > 0 {
		sb.WriteString("\nWarnings:\n")
		for _, w := range warnings {
			sb.WriteString(fmt.Sprintf("  - %s\n", w))
		}
	}

	var result model.OperationResult
	result.Success = true
	result.Warnings = warnings
	result.RawOutput = sb.String()
	result.RollbackCmd = rollbackCmd
	return result, nil
}
