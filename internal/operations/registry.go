package operations

import (
	"context"
	"sync"

	"github.com/dbs-rtf/agent/pkg/errors"
	"github.com/dbs-rtf/agent/pkg/model"
)

type OpCategory string

const (
	OpSQLDiag       OpCategory = "sql_diag"
	OpSessionMgmt   OpCategory = "session_mgmt"
	OpBackupRestore OpCategory = "backup_restore"
	OpHAMgmt        OpCategory = "ha_mgmt"
	OpParamMgmt     OpCategory = "param_mgmt"
	OpLogAnalysis   OpCategory = "log_analysis"
	OpUserPerm      OpCategory = "user_perm"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

type OperationPlugin interface {
	Name() string
	Category() OpCategory
	Description() string
	SupportedOps() []string
	Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error)
	RequiredRole(op string) Role
}

var (
	pluginMu sync.RWMutex
	plugins  = make(map[string]OperationPlugin)
)

func Register(plugin OperationPlugin) {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	plugins[plugin.Name()] = plugin
}

func Get(name string) (OperationPlugin, error) {
	pluginMu.RLock()
	defer pluginMu.RUnlock()
	p, ok := plugins[name]
	if !ok {
		return nil, errors.New(errors.ErrPluginNotFound, "plugin not found: "+name)
	}
	return p, nil
}

func List() []OperationPlugin {
	pluginMu.RLock()
	defer pluginMu.RUnlock()
	result := make([]OperationPlugin, 0, len(plugins))
	for _, p := range plugins {
		result = append(result, p)
	}
	return result
}

func ListByCategory(cat OpCategory) []OperationPlugin {
	all := List()
	var result []OperationPlugin
	for _, p := range all {
		if p.Category() == cat {
			result = append(result, p)
		}
	}
	return result
}
