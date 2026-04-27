package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/dbs-rtf/agent/pkg/model"
)

func TestWorkflowRunAllSteps(t *testing.T) {
	var executed []string

	def := WorkflowDef{
		Name: "test",
		Steps: []WorkflowStep{
			{
				Name: "step1",
				Execute: func(ctx context.Context) (model.OperationResult, error) {
					executed = append(executed, "step1")
					return model.OperationResult{Success: true}, nil
				},
			},
			{
				Name: "step2",
				Execute: func(ctx context.Context) (model.OperationResult, error) {
					executed = append(executed, "step2")
					return model.OperationResult{Success: true}, nil
				},
			},
		},
	}

	engine := &WorkflowEngine{}
	results, err := engine.Run(context.Background(), def, User{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if len(executed) != 2 {
		t.Errorf("expected 2 steps executed, got %d", len(executed))
	}
}

func TestWorkflowRollbackOnFailure(t *testing.T) {
	var rolledBack bool

	def := WorkflowDef{
		Name: "test-rollback",
		Steps: []WorkflowStep{
			{
				Name: "step1",
				Execute: func(ctx context.Context) (model.OperationResult, error) {
					return model.OperationResult{Success: true}, nil
				},
				Rollback: func(ctx context.Context) error {
					rolledBack = true
					return nil
				},
			},
			{
				Name: "step2-fail",
				Execute: func(ctx context.Context) (model.OperationResult, error) {
					return model.OperationResult{Success: false},
						fmt.Errorf("step2 failed")
				},
			},
		},
	}

	engine := &WorkflowEngine{}
	_, err := engine.Run(context.Background(), def, User{}, "")
	if err == nil {
		t.Fatal("expected error from failed step")
	}
	if !rolledBack {
		t.Error("expected rollback to have been called")
	}
}

func TestRollbackManagerExecute(t *testing.T) {
	mgr := NewRollbackManager()
	if mgr.HasRollback() {
		t.Error("empty manager should not have rollback")
	}

	cmd := &model.Command{Plugin: "test", Operation: "rollback"}
	mgr.Add(cmd)
	if !mgr.HasRollback() {
		t.Error("manager should have rollback after add")
	}
}
