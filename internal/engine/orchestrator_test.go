package engine

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func TestAuthAdminAllAllowed(t *testing.T) {
	auth := &Auth{}
	user := User{Name: "admin", Role: operations.RoleAdmin}
	plugin := &stubPlugin{requiredRole: operations.RoleAdmin}

	err := auth.CheckPermission(user, plugin, "test")
	if err != nil {
		t.Errorf("admin should have all permissions: %v", err)
	}
}

func TestAuthViewerDenied(t *testing.T) {
	auth := &Auth{}
	user := User{Name: "viewer", Role: operations.RoleViewer}
	plugin := &stubPlugin{requiredRole: operations.RoleAdmin}

	err := auth.CheckPermission(user, plugin, "test")
	if err == nil {
		t.Error("viewer should be denied for admin operations")
	}
}

func TestAuthOperatorCanDoOperator(t *testing.T) {
	auth := &Auth{}
	user := User{Name: "operator", Role: operations.RoleOperator}
	plugin := &stubPlugin{requiredRole: operations.RoleOperator}

	err := auth.CheckPermission(user, plugin, "test")
	if err != nil {
		t.Errorf("operator should have operator permissions: %v", err)
	}
}

func TestRateLimiterBlocksDuplicate(t *testing.T) {
	limiter := NewRateLimiter()

	err := limiter.Allow("10.0.1.5:3306")
	if err != nil {
		t.Fatalf("first allow should succeed: %v", err)
	}

	err = limiter.Allow("10.0.1.5:3306")
	if err == nil {
		t.Error("second allow on same instance should fail")
	}

	limiter.Release("10.0.1.5:3306")

	err = limiter.Allow("10.0.1.5:3306")
	if err != nil {
		t.Errorf("allow after release should succeed: %v", err)
	}
}

type stubPlugin struct {
	requiredRole operations.Role
}

func (s *stubPlugin) Name() string { return "stub" }
func (s *stubPlugin) Category() operations.OpCategory { return operations.OpSQLDiag }
func (s *stubPlugin) Description() string { return "stub" }
func (s *stubPlugin) SupportedOps() []string { return []string{"test"} }
func (s *stubPlugin) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	return model.OperationResult{Success: true}, nil
}
func (s *stubPlugin) RequiredRole(op string) operations.Role { return s.requiredRole }
