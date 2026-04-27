package sql_diag

import (
	"context"
	"fmt"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func explainSQL(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	sql := req.Params["sql"]
	if sql == "" {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "sql parameter required for explain"
		return result, nil
	}

	rows, err := adapter.Query(ctx, "EXPLAIN "+sql)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("explain failed: %v", err)
		return result, nil
	}

	var result model.OperationResult
	result.Success = true
	result.Data = rows
	result.RawOutput = fmt.Sprintf("EXPLAIN result: %d rows", len(rows.Data))
	return result, nil
}
