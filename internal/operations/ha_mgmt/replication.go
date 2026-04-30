package ha_mgmt

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func replicationStatus(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	status, err := adapter.GetReplicationStatus(ctx)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("unable to get replication status: %v", err)
		return result, nil
	}

	if !status.IsReplica {
		var result model.OperationResult
		result.Success = true
		result.Data = status
		result.RawOutput = fmt.Sprintf("Instance %s is not configured as a replica.\n", req.Instance.Address())
		return result, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Replication Status: %s ===\n\n", req.Instance.Address()))
	sb.WriteString(fmt.Sprintf("%-25s %s\n", "Source Host:", status.SourceHost))
	sb.WriteString(fmt.Sprintf("%-25s %d\n", "Source Port:", status.SourcePort))
	sb.WriteString(fmt.Sprintf("%-25s %s\n", "IO Thread:", status.IOState))
	sb.WriteString(fmt.Sprintf("%-25s %s\n", "SQL Thread:", status.SQLState))
	sb.WriteString(fmt.Sprintf("%-25s %d seconds\n", "Seconds Behind Master:", status.SecondsBehind))

	if status.LastError != "" {
		sb.WriteString(fmt.Sprintf("%-25s %s\n", "Last Error:", status.LastError))
	}

	sb.WriteString("\n")

	// Health assessment
	ioOK := status.IOState == "Yes"
	sqlOK := status.SQLState == "Yes"
	lagOK := status.SecondsBehind < 60

	if ioOK && sqlOK {
		sb.WriteString("Replication: RUNNING\n")
		if lagOK {
			sb.WriteString("Lag Status: NORMAL (< 60s)\n")
		} else {
			sb.WriteString(fmt.Sprintf("Lag Status: HIGH (>%ds)\n", status.SecondsBehind))
		}
	} else {
		sb.WriteString("Replication: STOPPED\n")
		if !ioOK {
			sb.WriteString("  - IO thread is not running\n")
		}
		if !sqlOK {
			sb.WriteString("  - SQL thread is not running\n")
		}
	}

	var result model.OperationResult
	result.Success = true
	result.Data = status
	result.RawOutput = sb.String()
	return result, nil
}
