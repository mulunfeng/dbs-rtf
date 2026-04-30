package ha_mgmt

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func TestHAMgmtRegistration(t *testing.T) {
	plugin, err := operations.Get("ha_mgmt")
	if err != nil {
		t.Fatalf("plugin not registered: %v", err)
	}
	if plugin.Name() != "ha_mgmt" {
		t.Errorf("expected name ha_mgmt, got %s", plugin.Name())
	}
	if plugin.Category() != operations.OpHAMgmt {
		t.Errorf("expected category ha_mgmt, got %s", plugin.Category())
	}
}

func TestHAMgmtSupportedOps(t *testing.T) {
	plugin := &HAMgmtPlugin{}
	ops := plugin.SupportedOps()
	expected := []string{"health", "failover", "replication"}
	if len(ops) != len(expected) {
		t.Fatalf("expected %d ops, got %d", len(expected), len(ops))
	}
	for i, op := range expected {
		if ops[i] != op {
			t.Errorf("expected op %s at index %d, got %s", op, i, ops[i])
		}
	}
}

func TestHAMgmtRequiredRole(t *testing.T) {
	plugin := &HAMgmtPlugin{}
	if plugin.RequiredRole("health") != operations.RoleOperator {
		t.Error("expected RoleOperator for health")
	}
	if plugin.RequiredRole("replication") != operations.RoleOperator {
		t.Error("expected RoleOperator for replication")
	}
	if plugin.RequiredRole("failover") != operations.RoleAdmin {
		t.Error("expected RoleAdmin for failover")
	}
}

func TestHAMgmtUnknownOp(t *testing.T) {
	plugin := &HAMgmtPlugin{}
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

func TestHAMgmtHealthMissingAdapter(t *testing.T) {
	plugin := &HAMgmtPlugin{}
	req := model.OperationRequest{
		Params: map[string]string{"op": "health"},
	}
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure when adapter is missing")
	}
}

func TestHAMgmtFailoverMissingNewMaster(t *testing.T) {
	plugin := &HAMgmtPlugin{}
	req := model.OperationRequest{
		Params: map[string]string{"op": "failover"},
	}
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure when new_master param is missing")
	}
}

func TestHAMgmtFailoverMissingAdapter(t *testing.T) {
	plugin := &HAMgmtPlugin{}
	req := model.OperationRequest{
		Params: map[string]string{"op": "failover", "new_master": "10.0.1.6:3306"},
	}
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure when adapter is missing")
	}
}

func TestHAMgmtReplicationMissingAdapter(t *testing.T) {
	plugin := &HAMgmtPlugin{}
	req := model.OperationRequest{
		Params: map[string]string{"op": "replication"},
	}
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure when adapter is missing")
	}
}
