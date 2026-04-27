package engine

import (
	"context"
	"fmt"

	"github.com/dbs-rtf/agent/pkg/model"
)

type RollbackManager struct {
	commands []*model.Command
}

func NewRollbackManager() *RollbackManager {
	return &RollbackManager{}
}

func (r *RollbackManager) Add(cmd *model.Command) {
	if cmd != nil {
		r.commands = append(r.commands, cmd)
	}
}

func (r *RollbackManager) HasRollback() bool {
	return len(r.commands) > 0
}

func (r *RollbackManager) Execute(ctx context.Context, orchestrator *Orchestrator, user User) ([]model.OperationResult, error) {
	var results []model.OperationResult
	for i := len(r.commands) - 1; i >= 0; i-- {
		cmd := r.commands[i]
		result, err := orchestrator.Execute(ctx, *cmd, user, "rollback")
		if err != nil {
			results = append(results, model.OperationResult{
				Success:   false,
				RawOutput: fmt.Sprintf("rollback failed: %s.%s: %v", cmd.Plugin, cmd.Operation, err),
			})
		} else {
			results = append(results, result)
		}
	}
	return results, nil
}
