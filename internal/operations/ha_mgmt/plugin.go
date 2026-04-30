package ha_mgmt

import (
	"context"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	operations.Register(&HAMgmtPlugin{})
}

type HAMgmtPlugin struct{}

func (p *HAMgmtPlugin) Name() string           { return "ha_mgmt" }
func (p *HAMgmtPlugin) Category() operations.OpCategory { return operations.OpHAMgmt }
func (p *HAMgmtPlugin) Description() string    { return "HA management: health check, failover, replication status" }
func (p *HAMgmtPlugin) SupportedOps() []string { return []string{"health", "failover", "replication"} }
func (p *HAMgmtPlugin) RequiredRole(op string) operations.Role {
	if op == "failover" {
		return operations.RoleAdmin
	}
	return operations.RoleOperator
}

func (p *HAMgmtPlugin) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	op := req.Params["op"]
	switch op {
	case "health":
		return healthCheck(ctx, req)
	case "failover":
		return failover(ctx, req)
	case "replication":
		return replicationStatus(ctx, req)
	default:
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "unknown ha_mgmt operation: " + op
		return result, nil
	}
}

func getAdapter(req model.OperationRequest) (adapters.DatabaseAdapter, bool) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	return adapter, ok
}
