package sql_diag

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func TestSQLDiagRegistration(t *testing.T) {
	plugin, err := operations.Get("sql_diag")
	if err != nil {
		t.Fatalf("plugin not registered: %v", err)
	}
	if plugin.Name() != "sql_diag" {
		t.Errorf("expected name sql_diag, got %s", plugin.Name())
	}
	if plugin.Category() != operations.OpSQLDiag {
		t.Errorf("expected category sql_diag, got %s", plugin.Category())
	}
}

func TestSQLDiagSupportedOps(t *testing.T) {
	plugin := &SQLDiagPlugin{}
	ops := plugin.SupportedOps()
	expected := []string{"explain", "slow_query", "topsql", "lock_analysis"}
	if len(ops) != len(expected) {
		t.Fatalf("expected %d ops, got %d", len(expected), len(ops))
	}
}

func TestSQLDiagUnknownOp(t *testing.T) {
	plugin := &SQLDiagPlugin{}
	req := model.OperationRequest{
		Params: map[string]string{"op": "nonexistent"},
	}
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected success=false for unknown op")
	}
}
