package sql_diag

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func slowQuery(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
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

	limit := 10
	if l := req.Params["limit"]; l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].Time > processes[j].Time
	})

	if len(processes) > limit {
		processes = processes[:limit]
	}

	var result model.OperationResult
	result.Success = true
	result.Data = processes

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Top %d longest running processes:\n\n", len(processes)))
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
