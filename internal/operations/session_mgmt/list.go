package session_mgmt

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func listSessions(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	processes, err := adapter.GetProcessList(ctx)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("failed to list sessions: %v", err)
		return result, nil
	}

	filter := req.Params["filter"]
	if filter == "long_running" {
		threshold := int64(60)
		if t := req.Params["threshold"]; t != "" {
			if parsed, err := strconv.ParseInt(t, 10, 64); err == nil {
				threshold = parsed
			}
		}
		var filtered []model.ProcessInfo
		for _, p := range processes {
			if p.Time >= threshold {
				filtered = append(filtered, p)
			}
		}
		processes = filtered
	}

	var result model.OperationResult
	result.Success = true
	result.Data = processes

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Sessions (%d total):\n\n", len(processes)))
	sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %-10s %s\n", "ID", "User", "Host", "Time(s)", "State", "Query"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, p := range processes {
		info := p.Info
		if len(info) > 60 {
			info = info[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-6d %-15s %-20s %-8d %-10s %s\n", p.ID, p.User, p.Host, p.Time, p.State, info))
	}
	result.RawOutput = sb.String()
	return result, nil
}
