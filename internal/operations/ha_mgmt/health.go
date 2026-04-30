package ha_mgmt

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

type healthStatus string

const (
	Healthy   healthStatus = "healthy"
	Degraded  healthStatus = "degraded"
	Critical  healthStatus = "critical"
)

type healthCheckResult struct {
	Status  healthStatus
	Checks  []checkResult
	Score   int // 0-100
}

type checkResult struct {
	Name    string
	Status  string
	Message string
}

func healthCheck(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := getAdapter(req)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	var checks []checkResult
	score := 100

	// Check 1: Ping
	checks = append(checks, checkPing(ctx, adapter, &score))

	// Check 2: Version
	checks = append(checks, checkVersion(ctx, adapter, &score))

	// Check 3: Process list - count connections
	checks = append(checks, checkConnections(ctx, adapter, &score))

	// Check 4: Replication status
	checks = append(checks, checkReplication(ctx, adapter, &score))

	// Check 5: Read-only status
	checks = append(checks, checkReadOnly(ctx, adapter, &score))

	// Determine overall status
	status := Healthy
	if score < 50 {
		status = Critical
	} else if score < 80 {
		status = Degraded
	}

	hsr := healthCheckResult{
		Status: status,
		Checks: checks,
		Score:  score,
	}

	var result model.OperationResult
	result.Success = true
	result.Data = hsr
	result.RawOutput = formatHealthReport(hsr)
	return result, nil
}

func checkPing(ctx context.Context, adapter adapters.DatabaseAdapter, score *int) checkResult {
	err := adapter.Ping(ctx)
	if err != nil {
		*score -= 40
		return checkResult{
			Name:    "Ping",
			Status:  "FAIL",
			Message: fmt.Sprintf("connection failed: %v", err),
		}
	}
	return checkResult{
		Name:    "Ping",
		Status:  "OK",
		Message: "database is reachable",
	}
}

func checkVersion(ctx context.Context, adapter adapters.DatabaseAdapter, score *int) checkResult {
	version, err := adapter.Version(ctx)
	if err != nil {
		*score -= 10
		return checkResult{
			Name:    "Version",
			Status:  "WARN",
			Message: fmt.Sprintf("unable to get version: %v", err),
		}
	}
	return checkResult{
		Name:    "Version",
		Status:  "OK",
		Message: version,
	}
}

func checkConnections(ctx context.Context, adapter adapters.DatabaseAdapter, score *int) checkResult {
	processes, err := adapter.GetProcessList(ctx)
	if err != nil {
		*score -= 20
		return checkResult{
			Name:    "Connections",
			Status:  "WARN",
			Message: fmt.Sprintf("unable to get process list: %v", err),
		}
	}

	total := len(processes)
	running := 0
	for _, p := range processes {
		if p.Command == "Query" && p.Time > 30 {
			running++
		}
	}

	if running > 10 {
		*score -= 20
		return checkResult{
			Name:    "Connections",
			Status:  "WARN",
			Message: fmt.Sprintf("%d total, %d long-running queries (>30s)", total, running),
		}
	}

	return checkResult{
		Name:    "Connections",
		Status:  "OK",
		Message: fmt.Sprintf("%d total connections, %d long-running", total, running),
	}
}

func checkReplication(ctx context.Context, adapter adapters.DatabaseAdapter, score *int) checkResult {
	status, err := adapter.GetReplicationStatus(ctx)
	if err != nil {
		return checkResult{
			Name:    "Replication",
			Status:  "SKIP",
			Message: fmt.Sprintf("unable to check: %v", err),
		}
	}

	if !status.IsReplica {
		return checkResult{
			Name:    "Replication",
			Status:  "SKIP",
			Message: "not a replica",
		}
	}

	if status.IOState != "Yes" || status.SQLState != "Yes" {
		*score -= 30
		return checkResult{
			Name:    "Replication",
			Status:  "FAIL",
			Message: fmt.Sprintf("IO=%s, SQL=%s, error: %s", status.IOState, status.SQLState, status.LastError),
		}
	}

	if status.SecondsBehind > 300 {
		*score -= 20
		return checkResult{
			Name:    "Replication",
			Status:  "FAIL",
			Message: fmt.Sprintf("replica lag: %d seconds", status.SecondsBehind),
		}
	}

	if status.SecondsBehind > 60 {
		*score -= 10
		return checkResult{
			Name:    "Replication",
			Status:  "WARN",
			Message: fmt.Sprintf("replica lag: %d seconds", status.SecondsBehind),
		}
	}

	return checkResult{
		Name:    "Replication",
		Status:  "OK",
		Message: fmt.Sprintf("lag: %d seconds from %s", status.SecondsBehind, status.SourceHost),
	}
}

func checkReadOnly(ctx context.Context, adapter adapters.DatabaseAdapter, score *int) checkResult {
	vars, err := adapter.GetVariables(ctx, "read_only")
	if err != nil {
		*score -= 10
		return checkResult{
			Name:    "Read-Only",
			Status:  "WARN",
			Message: fmt.Sprintf("unable to check: %v", err),
		}
	}

	val := vars["read_only"]
	if val == "ON" {
		return checkResult{
			Name:    "Read-Only",
			Status:  "WARN",
			Message: "instance is in read-only mode",
		}
	}

	return checkResult{
		Name:    "Read-Only",
		Status:  "OK",
		Message: "writable",
	}
}

func formatHealthReport(hsr healthCheckResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== Health Check Report ===\n"))
	sb.WriteString(fmt.Sprintf("Overall Status: %s (Score: %d/100)\n\n", hsr.Status, hsr.Score))

	sb.WriteString(fmt.Sprintf("%-15s %-6s %s\n", "Check", "Status", "Detail"))
	sb.WriteString(strings.Repeat("-", 80) + "\n")

	for _, c := range hsr.Checks {
		sb.WriteString(fmt.Sprintf("%-15s %-6s %s\n", c.Name, c.Status, c.Message))
	}

	return sb.String()
}
