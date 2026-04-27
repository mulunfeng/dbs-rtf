package engine

import (
	"fmt"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/errors"
)

type User struct {
	Name string
	Role operations.Role
}

type Auth struct{}

func (a *Auth) CheckPermission(user User, plugin operations.OperationPlugin, op string) error {
	required := plugin.RequiredRole(op)
	switch user.Role {
	case operations.RoleAdmin:
		return nil
	case operations.RoleOperator:
		if required == operations.RoleViewer || required == operations.RoleOperator {
			return nil
		}
	case operations.RoleViewer:
		if required == operations.RoleViewer {
			return nil
		}
	}
	return errors.New(errors.ErrPermissionDenied,
		fmt.Sprintf("user %s (%s) lacks permission for %s.%s (requires %s)",
			user.Name, user.Role, plugin.Name(), op, required))
}
