package session_mgmt

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func TestSessionMgmtRegistration(t *testing.T) {
	plugin, err := operations.Get("session_mgmt")
	if err != nil {
		t.Fatalf("plugin not registered: %v", err)
	}
	if plugin.Name() != "session_mgmt" {
		t.Errorf("expected name session_mgmt, got %s", plugin.Name())
	}
}

func TestSessionMgmtRequiredRole(t *testing.T) {
	plugin := &SessionMgmtPlugin{}
	if plugin.RequiredRole("list") != operations.RoleOperator {
		t.Error("expected RoleOperator for list")
	}
	if plugin.RequiredRole("kill") != operations.RoleAdmin {
		t.Error("expected RoleAdmin for kill")
	}
}

func TestSessionMgmtKillMissingID(t *testing.T) {
	plugin := &SessionMgmtPlugin{}
	req := model.OperationRequest{
		Params: map[string]string{"op": "kill"},
	}
	result, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure when process_id missing")
	}
}
