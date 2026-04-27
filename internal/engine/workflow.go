package engine

import (
	"context"
	"fmt"

	"github.com/dbs-rtf/agent/pkg/errors"
	"github.com/dbs-rtf/agent/pkg/model"
)

type WorkflowStep struct {
	Name     string
	Execute  func(ctx context.Context) (model.OperationResult, error)
	Rollback func(ctx context.Context) error
}

type WorkflowDef struct {
	Name  string
	Steps []WorkflowStep
}

type WorkflowEngine struct {
	orchestrator *Orchestrator
}

func NewWorkflowEngine(orchestrator *Orchestrator) *WorkflowEngine {
	return &WorkflowEngine{orchestrator: orchestrator}
}

func (w *WorkflowEngine) Run(ctx context.Context, def WorkflowDef, user User, source string) ([]model.OperationResult, error) {
	var results []model.OperationResult

	for i, step := range def.Steps {
		result, err := step.Execute(ctx)
		if err != nil {
			w.rollback(ctx, def.Steps[:i], results)
			return results, errors.Wrap(errors.ErrWorkflowFailed,
				fmt.Sprintf("step %q failed: %v", step.Name, err), err)
		}
		results = append(results, result)
	}

	return results, nil
}

func (w *WorkflowEngine) rollback(ctx context.Context, steps []WorkflowStep, results []model.OperationResult) []model.OperationResult {
	var rollbackResults []model.OperationResult
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Rollback != nil {
			err := steps[i].Rollback(ctx)
			if err != nil {
				rollbackResults = append(rollbackResults, model.OperationResult{
					Success:   false,
					RawOutput: fmt.Sprintf("rollback step %q failed: %v", steps[i].Name, err),
				})
			} else {
				rollbackResults = append(rollbackResults, model.OperationResult{
					Success:   true,
					RawOutput: fmt.Sprintf("rollback step %q completed", steps[i].Name),
				})
			}
		}
	}
	return rollbackResults
}
