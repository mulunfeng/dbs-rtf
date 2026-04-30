package adapters

import (
	"context"
	"sync"

	"github.com/dbs-rtf/agent/pkg/errors"
	"github.com/dbs-rtf/agent/pkg/model"
)

type AdapterFactory func(cfg model.InstanceConfig) (DatabaseAdapter, error)

var (
	registryMu sync.RWMutex
	factories  = make(map[model.DBType]AdapterFactory)
)

func Register(dbType model.DBType, factory AdapterFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	factories[dbType] = factory
}

func New(cfg model.InstanceConfig) (DatabaseAdapter, error) {
	registryMu.RLock()
	factory, ok := factories[cfg.Type]
	registryMu.RUnlock()
	if !ok {
		return nil, errors.New(errors.ErrAdapterNotFound, "no adapter registered for "+string(cfg.Type))
	}
	return factory(cfg)
}

type DatabaseAdapter interface {
	Type() model.DBType
	Version(ctx context.Context) (string, error)
	Connect(ctx context.Context) error
	Close() error
	Ping(ctx context.Context) error
	Exec(ctx context.Context, sql string, args ...any) (model.Result, error)
	Query(ctx context.Context, sql string, args ...any) (model.Rows, error)
	GetVariables(ctx context.Context, pattern string) (map[string]string, error)
	SetVariable(ctx context.Context, name, value string, scope model.VariableScope) error
	GetProcessList(ctx context.Context) ([]model.ProcessInfo, error)
	KillProcess(ctx context.Context, processID int) error
	StartBackup(ctx context.Context, opts model.BackupOptions) (model.BackupTask, error)
	GetReplicationStatus(ctx context.Context) (model.ReplicationStatus, error)
	GetExecutedGTIDSet(ctx context.Context) (string, error)
	SetGTIDPurged(ctx context.Context, gtidSet string) error
}
