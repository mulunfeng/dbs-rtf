package session_mgmt

import (
	"context"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	operations.Register(&SessionMgmtPlugin{})
}

type SessionMgmtPlugin struct{}

func (p *SessionMgmtPlugin) Name() string                        { return "session_mgmt" }
func (p *SessionMgmtPlugin) Category() operations.OpCategory      { return operations.OpSessionMgmt }
func (p *SessionMgmtPlugin) Description() string                  { return "Session management: list, kill, analyze" }
func (p *SessionMgmtPlugin) SupportedOps() []string               { return []string{"list", "kill", "analyze"} }
func (p *SessionMgmtPlugin) RequiredRole(op string) operations.Role {
	if op == "kill" {
		return operations.RoleAdmin
	}
	return operations.RoleOperator
}

func (p *SessionMgmtPlugin) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	op := req.Params["op"]
	switch op {
	case "list":
		return listSessions(ctx, req)
	case "kill":
		return killSession(ctx, req)
	case "analyze":
		return analyzeSession(ctx, req)
	default:
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "unknown session_mgmt operation: " + op
		return result, nil
	}
}
