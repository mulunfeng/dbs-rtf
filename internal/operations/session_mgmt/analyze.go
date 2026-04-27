package session_mgmt

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func analyzeSession(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
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
		result.RawOutput = fmt.Sprintf("failed to get process list: %v", err)
		return result, nil
	}

	total := len(processes)
	sleeping := 0
	running := 0
	querying := 0
	longRunning := 0

	for _, p := range processes {
		switch p.Command {
		case "Sleep":
			sleeping++
		case "Query":
			querying++
			if p.Time > 60 {
				longRunning++
			}
		default:
			running++
		}
	}

	var result model.OperationResult
	result.Success = true

	var sb strings.Builder
	sb.WriteString("Session Analysis:\n\n")
	sb.WriteString(fmt.Sprintf("  Total sessions:      %d\n", total))
	sb.WriteString(fmt.Sprintf("  Sleeping:            %d\n", sleeping))
	sb.WriteString(fmt.Sprintf("  Running/Query:       %d\n", running+querying))
	sb.WriteString(fmt.Sprintf("  Long running (>60s): %d\n", longRunning))

	if longRunning > 0 {
		sb.WriteString("\nLong running sessions:\n")
		sb.WriteString(fmt.Sprintf("%-6s %-15s %-8s %s\n", "ID", "User", "Time(s)", "Query"))
		sb.WriteString(strings.Repeat("-", 80) + "\n")
		for _, p := range processes {
			if p.Time > 60 && p.Command == "Query" {
				info := p.Info
				if len(info) > 40 {
					info = info[:37] + "..."
				}
				sb.WriteString(fmt.Sprintf("%-6d %-15s %-8d %s\n", p.ID, p.User, p.Time, info))
			}
		}
	}
	result.RawOutput = sb.String()
	return result, nil
}
