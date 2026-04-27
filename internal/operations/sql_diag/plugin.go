package sql_diag

import (
	"context"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	operations.Register(&SQLDiagPlugin{})
}

type SQLDiagPlugin struct{}

func (p *SQLDiagPlugin) Name() string                      { return "sql_diag" }
func (p *SQLDiagPlugin) Category() operations.OpCategory   { return operations.OpSQLDiag }
func (p *SQLDiagPlugin) Description() string               { return "SQL diagnostics: EXPLAIN, slow query, TopSQL" }
func (p *SQLDiagPlugin) SupportedOps() []string            { return []string{"explain", "slow_query", "topsql", "lock_analysis"} }
func (p *SQLDiagPlugin) RequiredRole(op string) operations.Role {
	return operations.RoleOperator
}

func (p *SQLDiagPlugin) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	op := req.Params["op"]
	switch op {
	case "explain":
		return explainSQL(ctx, req)
	case "slow_query":
		return slowQuery(ctx, req)
	case "topsql":
		return topSQL(ctx, req)
	case "lock_analysis":
		return lockAnalysis(ctx, req)
	default:
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "unknown sql_diag operation: " + op
		return result, nil
	}
}
