package testcase

import (
	"context"
	"sync"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

// MockAdapter is a configurable mock for HA scenario testing.
// It simulates different MySQL states by allowing test code to
// configure return values and error conditions.
type MockAdapter struct {
	cfg model.InstanceConfig

	// Ping control
	PingErr      error
	PingCallCount int

	// Version control
	ServerVersion    string
	VersionErr error

	// Replication status control
	ReplicationStatus model.ReplicationStatus
	ReplicationErr    error

	// Variables control
	Variables map[string]string

	// Process list control
	Processes []model.ProcessInfo
	ProcessErr error

	// Exec control
	ExecErr error
	ExecLog []string // records executed SQL statements

	// State tracking
	mu       sync.Mutex
	closed   bool
	connected bool
}

func (m *MockAdapter) Type() model.DBType { return m.cfg.Type }

func (m *MockAdapter) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = true
	return nil
}

func (m *MockAdapter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *MockAdapter) Ping(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PingCallCount++
	return m.PingErr
}

func (m *MockAdapter) Version(ctx context.Context) (string, error) {
	return m.ServerVersion, m.VersionErr
}

func (m *MockAdapter) Exec(ctx context.Context, sql string, args ...any) (model.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExecLog = append(m.ExecLog, sql)
	return model.Result{RowsAffected: 1}, m.ExecErr
}

func (m *MockAdapter) Query(ctx context.Context, sql string, args ...any) (model.Rows, error) {
	return model.Rows{
		Columns: []string{"ID", "USER", "HOST", "DB", "COMMAND", "TIME", "STATE", "INFO"},
		Data:    nil,
	}, nil
}

func (m *MockAdapter) GetVariables(ctx context.Context, pattern string) (map[string]string, error) {
	if m.Variables == nil {
		return map[string]string{"read_only": "OFF"}, nil
	}
	if pattern != "" {
		result := make(map[string]string)
		for k, v := range m.Variables {
			if matchPattern(k, pattern) {
				result[k] = v
			}
		}
		return result, nil
	}
	return m.Variables, nil
}

func (m *MockAdapter) SetVariable(ctx context.Context, name, value string, scope model.VariableScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Variables == nil {
		m.Variables = make(map[string]string)
	}
	m.Variables[name] = value
	return nil
}

func (m *MockAdapter) GetProcessList(ctx context.Context) ([]model.ProcessInfo, error) {
	return m.Processes, m.ProcessErr
}

func (m *MockAdapter) KillProcess(ctx context.Context, processID int) error {
	return nil
}

func (m *MockAdapter) StartBackup(ctx context.Context, opts model.BackupOptions) (model.BackupTask, error) {
	return model.BackupTask{}, nil
}

func (m *MockAdapter) GetReplicationStatus(ctx context.Context) (model.ReplicationStatus, error) {
	return m.ReplicationStatus, m.ReplicationErr
}

func matchPattern(s, pattern string) bool {
	if pattern == "" {
		return true
	}
	return s == pattern
}

// Ensure MockAdapter implements DatabaseAdapter
var _ adapters.DatabaseAdapter = (*MockAdapter)(nil)

// Helper: create a healthy replica mock adapter
func NewHealthyReplica(host string, port int, sourceHost string, lagSeconds int64) *MockAdapter {
	return &MockAdapter{
		cfg: model.InstanceConfig{
			Host: host,
			Port: port,
			Type: model.MySQL,
		},
		ServerVersion: "8.0.35",
		ReplicationStatus: model.ReplicationStatus{
			IsReplica:     true,
			SourceHost:    sourceHost,
			SourcePort:    3306,
			IOState:       "Yes",
			SQLState:      "Yes",
			SecondsBehind: lagSeconds,
		},
		Variables: map[string]string{
			"read_only": "ON",
		},
	}
}

// Helper: create a primary mock adapter
func NewPrimary(host string, port int) *MockAdapter {
	return &MockAdapter{
		cfg: model.InstanceConfig{
			Host: host,
			Port: port,
			Type: model.MySQL,
		},
		ServerVersion: "8.0.35",
		ReplicationStatus: model.ReplicationStatus{
			IsReplica: false,
		},
		Variables: map[string]string{
			"read_only": "OFF",
		},
	}
}

// Helper: create an unreachable mock adapter
func NewUnreachable(host string, port int) *MockAdapter {
	return &MockAdapter{
		cfg: model.InstanceConfig{
			Host: host,
			Port: port,
			Type: model.MySQL,
		},
		PingErr:     context.DeadlineExceeded,
		VersionErr:  context.DeadlineExceeded,
		ProcessErr:  context.DeadlineExceeded,
	}
}

// Helper: create a replica with broken replication
func NewBrokenReplica(host string, port int, sourceHost string) *MockAdapter {
	return &MockAdapter{
		cfg: model.InstanceConfig{
			Host: host,
			Port: port,
			Type: model.MySQL,
		},
		ServerVersion: "8.0.35",
		ReplicationStatus: model.ReplicationStatus{
			IsReplica:     true,
			SourceHost:    sourceHost,
			SourcePort:    3306,
			IOState:       "No",
			SQLState:      "No",
			SecondsBehind: 0,
			LastError:     "Got fatal error during DML, last error: ...",
		},
		Variables: map[string]string{
			"read_only": "ON",
		},
	}
}
