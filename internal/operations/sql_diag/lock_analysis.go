package sql_diag

import (
	"context"
	"fmt"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func lockAnalysis(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	rows, err := adapter.Query(ctx, "SELECT * FROM information_schema.INNODB_LOCKS")
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("lock analysis failed: %v", err)
		return result, nil
	}

	var result model.OperationResult
	result.Success = true
	result.Data = rows
	result.RawOutput = fmt.Sprintf("Lock analysis: %d locks found", len(rows.Data))
	return result, nil
}
