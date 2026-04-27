package sql_diag

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func topSQL(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	limit := req.Params["limit"]
	if limit == "" {
		limit = "10"
	}

	query := fmt.Sprintf("SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.PROCESSLIST WHERE COMMAND != 'Sleep' AND INFO IS NOT NULL ORDER BY TIME DESC LIMIT %s", limit)

	rows, err := adapter.Query(ctx, query)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("topsql query failed: %v", err)
		return result, nil
	}

	var result model.OperationResult
	result.Success = true
	result.Data = rows

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TopSQL (limit: %s):\n\n", limit))
	sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %s\n", "ID", "User", "Host", "Time(s)", "Query"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, row := range rows.Data {
		id := fmt.Sprintf("%v", row["ID"])
		user := fmt.Sprintf("%v", row["USER"])
		host := fmt.Sprintf("%v", row["HOST"])
		t := fmt.Sprintf("%v", row["TIME"])
		info := fmt.Sprintf("%v", row["INFO"])
		if len(info) > 60 {
			info = info[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %s\n", id, user, host, t, info))
	}
	result.RawOutput = sb.String()
	return result, nil
}
