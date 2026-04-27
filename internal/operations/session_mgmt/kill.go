package session_mgmt

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func killSession(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	processIDStr := req.Params["process_id"]
	if processIDStr == "" {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "process_id parameter required"
		return result, nil
	}

	processID, err := strconv.Atoi(processIDStr)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "invalid process_id: " + processIDStr
		return result, nil
	}

	err = adapter.KillProcess(ctx, processID)
	var result model.OperationResult
	if err != nil {
		result.Success = false
		result.RawOutput = fmt.Sprintf("failed to kill process %d: %v", processID, err)
		return result, nil
	}

	result.Success = true
	result.RawOutput = fmt.Sprintf("Successfully killed process %d", processID)
	return result, nil
}
