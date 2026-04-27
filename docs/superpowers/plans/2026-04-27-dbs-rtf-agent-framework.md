# DBS-RTF Agent Framework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a multi-database operations agent framework with plugin architecture, supporting CLI and API server modes, NLP + structured command parsing, and pluggable database adapters.

**Architecture:** Layered monolith (Go) — Interface layer (NLP + command parser) → Core engine (orchestrator, auth, audit, workflow, rollback) → Operations plugins → Database adapter plugins. Two entry points (CLI, Server) share internal code.

**Tech Stack:** Go 1.21+, cobra (CLI), gin (API server), go-yaml (config), go-sql-driver/mysql, lib/pq, go-redis, mongo-driver, anthropic-sdk (NLP intent)

---

## File Map

| File | Responsibility |
|------|---------------|
| `go.mod` | Module definition, dependencies |
| `configs/dbs-agent.example.yaml` | Example config with all options |
| `pkg/model/instance.go` | DBType, InstanceConfig, TLSConfig |
| `pkg/model/session.go` | ProcessInfo, SessionInfo structs |
| `pkg/model/query.go` | Result, Rows, VariableScope, BackupOptions, etc. |
| `pkg/errors/errors.go` | DBSError, ErrorCode, error helpers |
| `pkg/config/config.go` | YAML config loading, env var expansion |
| `internal/adapters/registry.go` | Adapter registry, Get/List/Register |
| `internal/adapters/mysql/adapter.go` | MySQL DatabaseAdapter implementation |
| `internal/adapters/mysql/conn.go` | MySQL connection handling |
| `internal/adapters/mysql/adapter_test.go` | MySQL adapter unit tests |
| `internal/operations/registry.go` | OperationPlugin interface, plugin registry |
| `internal/operations/sql_diag/plugin.go` | SQL diag plugin registration & Execute dispatch |
| `internal/operations/sql_diag/slow_query.go` | Slow query analysis |
| `internal/operations/sql_diag/topsql.go` | TopSQL analysis & kill |
| `internal/operations/session_mgmt/plugin.go` | Session mgmt plugin registration & dispatch |
| `internal/operations/session_mgmt/list.go` | List sessions |
| `internal/operations/session_mgmt/kill.go` | Kill sessions |
| `internal/engine/orchestrator.go` | Command routing, auth check, plugin execution |
| `internal/engine/auth.go` | Role-based permission checks |
| `internal/engine/audit.go` | Audit logging (file-based) |
| `internal/engine/rate_limiter.go` | Per-instance operation rate limiting |
| `internal/engine/workflow.go` | Multi-step workflow engine |
| `internal/engine/rollback.go` | Rollback command execution |
| `internal/interface/command/router.go` | CLI command registration & dispatch |
| `internal/interface/command/parser.go` | Flag parsing → Command |
| `internal/interface/nlp/intent.go` | LLM-based intent parsing |
| `internal/interface/nlp/prompts.go` | Prompt template loading |
| `internal/interface/nlp/templates/intent.yaml` | NLP system prompt |
| `cmd/cli/main.go` | CLI entry point, cobra root command |
| `cmd/server/main.go` | API server entry point, HTTP routes |

---

### Task 1: Project Bootstrap — go.mod, config, models, errors

**Files:**
- Create: `go.mod`
- Create: `configs/dbs-agent.example.yaml`
- Create: `pkg/errors/errors.go`
- Create: `pkg/model/instance.go`
- Create: `pkg/model/session.go`
- Create: `pkg/model/query.go`
- Create: `pkg/config/config.go`
- Create: `pkg/config/config_test.go`

- [ ] **Step 1: Initialize Go module**

```bash
cd D:\ai\dbs-rtf && go mod init github.com/dbs-rtf/agent
```

- [ ] **Step 2: Create error types**

Create `pkg/errors/errors.go`:

```go
package errors

import "fmt"

type ErrorCode string

const (
	ErrConnectionFailed ErrorCode = "CONNECTION_FAILED"
	ErrPermissionDenied ErrorCode = "PERMISSION_DENIED"
	ErrTimeout          ErrorCode = "OPERATION_TIMEOUT"
	ErrAdapterNotFound  ErrorCode = "ADAPTER_NOT_FOUND"
	ErrPluginNotFound   ErrorCode = "PLUGIN_NOT_FOUND"
	ErrInvalidParam     ErrorCode = "INVALID_PARAMETER"
	ErrDatabaseError    ErrorCode = "DATABASE_ERROR"
	ErrWorkflowFailed   ErrorCode = "WORKFLOW_FAILED"
)

type DBSError struct {
	Code    ErrorCode
	Message string
	Detail  string
	Retry   bool
}

func (e *DBSError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func New(code ErrorCode, msg string) *DBSError {
	return &DBSError{Code: code, Message: msg}
}

func Wrap(code ErrorCode, msg string, detail error) *DBSError {
	d := ""
	if detail != nil {
		d = detail.Error()
	}
	return &DBSError{Code: code, Message: msg, Detail: d}
}

func IsDBSError(err error) bool {
	_, ok := err.(*DBSError)
	return ok
}
```

- [ ] **Step 3: Create instance model**

Create `pkg/model/instance.go`:

```go
package model

type DBType string

const (
	MySQL      DBType = "mysql"
	PostgreSQL DBType = "postgresql"
	Redis      DBType = "redis"
	MongoDB    DBType = "mongodb"
)

type TLSConfig struct {
	Enabled            bool
	CACertPath         string
	ClientCertPath     string
	ClientKeyPath      string
	SkipVerify         bool
}

type InstanceConfig struct {
	Name     string            `yaml:"name"`
	Host     string            `yaml:"host"`
	Port     int               `yaml:"port"`
	Type     DBType            `yaml:"type"`
	User     string            `yaml:"user"`
	Password string            `yaml:"password"`
	Database string            `yaml:"database"`
	Options  map[string]string `yaml:"options"`
	TLS      *TLSConfig        `yaml:"tls"`
}

func (i InstanceConfig) Address() string {
	return fmt.Sprintf("%s:%d", i.Host, i.Port)
}
```

Add `import "fmt"` at top.

- [ ] **Step 4: Create session model**

Create `pkg/model/session.go`:

```go
package model

import "time"

type ProcessInfo struct {
	ID       int
	User     string
	Host     string
	Database string
	Command  string
	Time     int64
	State    string
	Info     string
}

type SessionInfo struct {
	ProcessInfo
	StartTime  time.Time
	Duration   time.Duration
	BlockedBy  int
	Blocking   []int
}
```

Add `import "fmt"` to instance.go if not already present.

- [ ] **Step 5: Create query/result model**

Create `pkg/model/query.go`:

```go
package model

import "time"

type VariableScope string

const (
	ScopeGlobal  VariableScope = "global"
	ScopeSession VariableScope = "session"
	ScopeBoth    VariableScope = "both"
)

type Result struct {
	RowsAffected int64
	LastInsertID int64
}

type Row map[string]interface{}

type Rows struct {
	Columns []string
	Data    []Row
}

type BackupOptions struct {
	OutputPath string
	Database   string
	Tables     []string
	Compress   bool
}

type BackupTask struct {
	ID        string
	Status    string
	StartTime time.Time
	EndTime   time.Time
	FilePath  string
	Error     string
}

type ReplicationStatus struct {
	IsReplica      bool
	SourceHost     string
	SourcePort     int
	IOState        string
	SQLState       string
	SecondsBehind  int64
	LastError      string
}

type OperationRequest struct {
	Instance InstanceConfig
	Adapter  interface{} // adapters.DatabaseAdapter, set by orchestrator
	Params   map[string]string
}

type OperationResult struct {
	Success     bool
	Data        interface{}
	RawOutput   string
	Duration    time.Duration
	Warnings    []string
	RollbackCmd *Command
}

type Command struct {
	Plugin     string
	Operation  string
	Params     map[string]string
	Instance   InstanceConfig
	DryRun     bool
	Timeout    time.Duration
}
```

- [ ] **Step 6: Create config loader**

Create `pkg/config/config.go`:

```go
package config

import (
	"os"
	"regexp"
	"strings"

	"github.com/dbs-rtf/agent/pkg/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Audit    AuditConfig    `yaml:"audit"`
	Security SecurityConfig `yaml:"security"`
	NLP      NLPConfig      `yaml:"nlp"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Instances []model.InstanceConfig `yaml:"instances"`
}

type AuditConfig struct {
	Mode string `yaml:"mode"`
	Path string `yaml:"path"`
}

type SecurityConfig struct {
	DefaultTimeout            string `yaml:"default_timeout"`
	DangerousOpsConfirm       bool   `yaml:"dangerous_ops_confirm"`
	MaxConcurrentOpsPerInst   int    `yaml:"max_concurrent_ops_per_instance"`
}

type NLPConfig struct {
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
}

var envRegex = regexp.MustCompile(`\$\{(\w+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	resolved := envRegex.ReplaceAllStringFunc(string(data), func(match string) string {
		envVar := match[2 : len(match)-1]
		if val := os.Getenv(envVar); val != "" {
			return val
		}
		return match
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return nil, err
	}

	if cfg.Security.DefaultTimeout == "" {
		cfg.Security.DefaultTimeout = "30s"
	}
	if cfg.Security.MaxConcurrentOpsPerInst == 0 {
		cfg.Security.MaxConcurrentOpsPerInst = 1
	}
	if cfg.NLP.Model == "" {
		cfg.NLP.Model = "claude-sonnet-4-6"
	}

	return &cfg, nil
}
```

- [ ] **Step 7: Create example config**

Create `configs/dbs-agent.example.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  instances:
    - name: "prod-mysql-1"
      host: "10.0.1.5"
      port: 3306
      type: "mysql"
      user: "${DB_USER}"
      password: "${DB_PASSWORD}"
      tls: true

audit:
  mode: "file"
  path: "./logs/audit/"

security:
  default_timeout: "30s"
  dangerous_ops_confirm: true
  max_concurrent_ops_per_instance: 1

nlp:
  provider: "claude"
  model: "claude-sonnet-4-6"
  temperature: 0.1
```

- [ ] **Step 8: Write config tests**

Create `pkg/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	cfgContent := `
server:
  host: "127.0.0.1"
  port: 9090
database:
  instances: []
audit:
  mode: "file"
  path: "/tmp/audit/"
security:
  default_timeout: "60s"
  dangerous_ops_confirm: false
  max_concurrent_ops_per_instance: 2
nlp:
  provider: "claude"
  model: "test-model"
`
	tmp := filepath.Join(t.TempDir(), "test.yaml")
	os.WriteFile(tmp, []byte(cfgContent), 0644)

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Security.DangerousOpsConfirm != false {
		t.Error("expected dangerous_ops_confirm false")
	}
}

func TestEnvVarExpansion(t *testing.T) {
	os.Setenv("TEST_DB_PASS", "secret123")
	defer os.Unsetenv("TEST_DB_PASS")

	cfgContent := `
server:
  host: "localhost"
  port: 8080
database:
  instances:
    - name: "test"
      host: "localhost"
      port: 3306
      type: "mysql"
      user: "root"
      password: "${TEST_DB_PASS}"
audit:
  mode: "file"
security:
nlp:
`
	tmp := filepath.Join(t.TempDir(), "envtest.yaml")
	os.WriteFile(tmp, []byte(cfgContent), 0644)

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Instances[0].Password != "secret123" {
		t.Errorf("expected password secret123, got %s", cfg.Database.Instances[0].Password)
	}
}

func TestDefaults(t *testing.T) {
	cfgContent := `
server:
  host: "localhost"
  port: 8080
database:
  instances: []
audit:
  mode: "file"
security:
nlp:
`
	tmp := filepath.Join(t.TempDir(), "defaults.yaml")
	os.WriteFile(tmp, []byte(cfgContent), 0644)

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Security.DefaultTimeout != "30s" {
		t.Errorf("expected default timeout 30s, got %s", cfg.Security.DefaultTimeout)
	}
	if cfg.Security.MaxConcurrentOpsPerInst != 1 {
		t.Errorf("expected default max concurrent 1, got %d", cfg.Security.MaxConcurrentOpsPerInst)
	}
}
```

- [ ] **Step 9: Run tests and commit**

```bash
go mod tidy
go test ./pkg/config/ -v
go test ./pkg/errors/ -v
```

Expected: All tests pass.

```bash
git add go.mod go.sum pkg/ configs/
git commit -m "feat: bootstrap project with models, errors, config"
```

---

### Task 2: Database Adapter Registry + MySQL Adapter

**Files:**
- Create: `internal/adapters/registry.go`
- Create: `internal/adapters/mysql/adapter.go`
- Create: `internal/adapters/mysql/conn.go`
- Create: `internal/adapters/mysql/adapter_test.go`

- [ ] **Step 1: Create adapter registry**

Create `internal/adapters/registry.go`:

```go
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
}
```

- [ ] **Step 2: Create MySQL adapter**

Create `internal/adapters/mysql/adapter.go`:

```go
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	adapters.Register(model.MySQL, NewAdapter)
}

type MySQLAdapter struct {
	cfg  model.InstanceConfig
	db   *sql.DB
	mu   sync.RWMutex
}

func NewAdapter(cfg model.InstanceConfig) (adapters.DatabaseAdapter, error) {
	a := &MySQLAdapter{cfg: cfg}
	return a, nil
}

func (a *MySQLAdapter) Type() model.DBType { return model.MySQL }

func (a *MySQLAdapter) Connect(ctx context.Context) error {
	dsn := buildDSN(a.cfg)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("mysql ping: %w", err)
	}
	a.mu.Lock()
	a.db = db
	a.mu.Unlock()
	return nil
}

func (a *MySQLAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *MySQLAdapter) Ping(ctx context.Context) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("not connected")
	}
	return a.db.PingContext(ctx)
}

func (a *MySQLAdapter) Version(ctx context.Context) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	return version, err
}

func (a *MySQLAdapter) Exec(ctx context.Context, sqlStr string, args ...any) (model.Result, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res, err := a.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return model.Result{}, err
	}
	rows, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	return model.Result{RowsAffected: rows, LastInsertID: id}, nil
}

func (a *MySQLAdapter) Query(ctx context.Context, sqlStr string, args ...any) (model.Rows, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	rows, err := a.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return model.Rows{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return model.Rows{}, err
	}

	var result model.Rows
	result.Columns = cols

	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return model.Rows{}, err
		}
		row := make(model.Row)
		for i, col := range cols {
			row[col] = vals[i]
		}
		result.Data = append(result.Data, row)
	}
	return result, nil
}

func (a *MySQLAdapter) GetVariables(ctx context.Context, pattern string) (map[string]string, error) {
	query := "SHOW VARIABLES"
	if pattern != "" {
		query += fmt.Sprintf(" LIKE '%s'", pattern)
	}
	rows, err := a.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, row := range rows.Data {
		name, _ := row["Variable_name"].(string)
		value, _ := row["Value"].(string)
		result[name] = value
	}
	return result, nil
}

func (a *MySQLAdapter) SetVariable(ctx context.Context, name, value string, scope model.VariableScope) error {
	var keyword string
	switch scope {
	case model.ScopeGlobal:
		keyword = "GLOBAL"
	case model.ScopeSession:
		keyword = "SESSION"
	case model.ScopeBoth:
		keyword = "GLOBAL"
	default:
		keyword = "SESSION"
	}
	sqlStr := fmt.Sprintf("SET %s %s = ?", keyword, name)
	_, err := a.Exec(ctx, sqlStr, value)
	if err != nil {
		return err
	}
	if scope == model.ScopeBoth {
		sqlStr = fmt.Sprintf("SET SESSION %s = ?", name)
		_, err = a.Exec(ctx, sqlStr, value)
	}
	return err
}

func (a *MySQLAdapter) GetProcessList(ctx context.Context) ([]model.ProcessInfo, error) {
	rows, err := a.Query(ctx, "SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.PROCESSLIST")
	if err != nil {
		return nil, err
	}
	var processes []model.ProcessInfo
	for _, row := range rows.Data {
		p := model.ProcessInfo{}
		if v, ok := row["ID"].(int64); ok {
			p.ID = int(v)
		}
		if v, ok := row["USER"].(string); ok {
			p.User = v
		}
		if v, ok := row["HOST"].(string); ok {
			p.Host = v
		}
		if v, ok := row["DB"].(string); ok {
			p.Database = v
		}
		if v, ok := row["COMMAND"].(string); ok {
			p.Command = v
		}
		if v, ok := row["TIME"].(int64); ok {
			p.Time = v
		}
		if v, ok := row["STATE"].(string); ok {
			p.State = v
		}
		if v, ok := row["INFO"].(string); ok {
			p.Info = v
		}
		processes = append(processes, p)
	}
	return processes, nil
}

func (a *MySQLAdapter) KillProcess(ctx context.Context, processID int) error {
	_, err := a.Exec(ctx, fmt.Sprintf("KILL %d", processID))
	return err
}

func (a *MySQLAdapter) StartBackup(ctx context.Context, opts model.BackupOptions) (model.BackupTask, error) {
	return model.BackupTask{}, fmt.Errorf("backup not yet implemented")
}

func (a *MySQLAdapter) GetReplicationStatus(ctx context.Context) (model.ReplicationStatus, error) {
	rows, err := a.Query(ctx, "SHOW SLAVE STATUS")
	if err != nil {
		return model.ReplicationStatus{}, err
	}
	if len(rows.Data) == 0 {
		return model.ReplicationStatus{}, nil
	}
	row := rows.Data[0]
	status := model.ReplicationStatus{
		IsReplica: true,
	}
	if v, ok := row["Master_Host"].(string); ok {
		status.SourceHost = v
	}
	if v, ok := row["Slave_IO_Running"].(string); ok {
		status.IOState = v
	}
	if v, ok := row["Slave_SQL_Running"].(string); ok {
		status.SQLState = v
	}
	if v, ok := row["Seconds_Behind_Master"].(string); ok {
		fmt.Sscanf(v, "%d", &status.SecondsBehind)
	}
	if v, ok := row["Last_Error"].(string); ok {
		status.LastError = v
	}
	return status, nil
}
```

- [ ] **Step 3: Create MySQL connection helpers**

Create `internal/adapters/mysql/conn.go`:

```go
package mysql

import (
	"fmt"
	"net/url"

	"github.com/dbs-rtf/agent/pkg/model"
)

func buildDSN(cfg model.InstanceConfig) string {
	params := url.Values{}
	if cfg.Database != "" {
		params.Set("database", cfg.Database)
	}
	if cfg.TLS != nil && cfg.TLS.Enabled {
		params.Set("tls", "true")
	}
	for k, v := range cfg.Options {
		params.Set(k, v)
	}

	query := params.Encode()
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	if query != "" {
		dsn += "?" + query
	}
	return dsn
}
```

- [ ] **Step 4: Write adapter tests**

Create `internal/adapters/mysql/adapter_test.go`:

```go
package mysql

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func TestMySQLAdapterRegistration(t *testing.T) {
	cfg := model.InstanceConfig{
		Name:     "test",
		Host:     "localhost",
		Port:     3306,
		Type:     model.MySQL,
		User:     "root",
		Password: "",
	}
	adapter, err := adapters.New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.Type() != model.MySQL {
		t.Errorf("expected type mysql, got %s", adapter.Type())
	}
}

func TestMySQLAdapterConnectFail(t *testing.T) {
	cfg := model.InstanceConfig{
		Host:     "127.0.0.1",
		Port:     19999,
		Type:     model.MySQL,
		User:     "root",
		Password: "wrong",
	}
	adapter, _ := adapters.New(cfg)
	err := adapter.Connect(context.Background())
	if err == nil {
		t.Error("expected connection error on unreachable host")
		adapter.Close()
	}
}

func TestMySQLAdapterType(t *testing.T) {
	cfg := model.InstanceConfig{Type: model.MySQL}
	a, _ := NewAdapter(cfg)
	if a.Type() != model.MySQL {
		t.Errorf("expected mysql, got %s", a.Type())
	}
}
```

- [ ] **Step 5: Run tests and commit**

```bash
go get github.com/go-sql-driver/mysql
go mod tidy
go test ./internal/adapters/... -v
```

Expected: registration test passes, connection test fails gracefully (no MySQL running), type test passes.

```bash
git add internal/adapters/
git commit -m "feat: add adapter registry and MySQL adapter with tests"
```

---

### Task 3: Operations Plugin Registry + SQL Diag + Session Mgmt Plugins

**Files:**
- Create: `internal/operations/registry.go`
- Create: `internal/operations/sql_diag/plugin.go`
- Create: `internal/operations/sql_diag/slow_query.go`
- Create: `internal/operations/sql_diag/topsql.go`
- Create: `internal/operations/sql_diag/plugin_test.go`
- Create: `internal/operations/session_mgmt/plugin.go`
- Create: `internal/operations/session_mgmt/list.go`
- Create: `internal/operations/session_mgmt/kill.go`
- Create: `internal/operations/session_mgmt/plugin_test.go`

- [ ] **Step 1: Create operations registry**

Create `internal/operations/registry.go`:

```go
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
```

- [ ] **Step 2: Create SQL diag plugin**

Create `internal/operations/sql_diag/plugin.go`:

```go
package sql_diag

import (
	"context"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	operations.Register(&SQLDiagPlugin{})
}

type SQLDiagPlugin struct{}

func (p *SQLDiagPlugin) Name() string           { return "sql_diag" }
func (p *SQLDiagPlugin) Category() operations.OpCategory { return operations.OpSQLDiag }
func (p *SQLDiagPlugin) Description() string     { return "SQL diagnostics: EXPLAIN, slow query, TopSQL" }
func (p *SQLDiagPlugin) SupportedOps() []string  { return []string{"explain", "slow_query", "topsql", "lock_analysis"} }
func (p *SQLDiagPlugin) RequiredRole(op string) operations.Role {
	return operations.RoleOperator
}

func (p *SQLDiagPlugin) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	op := req.Params["op"]
	switch op {
	case "explain":
		return explainSQL(ctx, req)
	case "slow_query":
		return slowQuery(ctx, req)
	case "topsql":
		return topSQL(ctx, req)
	case "lock_analysis":
		return lockAnalysis(ctx, req)
	default:
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "unknown sql_diag operation: " + op
		return result, nil
	}
}
```

Create `internal/operations/sql_diag/slow_query.go`:

```go
package sql_diag

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dbs-rtf/agent/pkg/model"
)

func slowQuery(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	processes, err := adapter.GetProcessList(ctx)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("failed to get process list: %v", err)
		return result, nil
	}

	limit := 10
	if l := req.Params["limit"]; l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].Time > processes[j].Time
	})

	if len(processes) > limit {
		processes = processes[:limit]
	}

	var result model.OperationResult
	result.Success = true
	result.Data = processes

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Top %d longest running processes:\n\n", len(processes)))
	sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %-10s %s\n", "ID", "User", "Host", "Time(s)", "State", "Query"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, p := range processes {
		info := p.Info
		if len(info) > 60 {
			info = info[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-6d %-15s %-20s %-8d %-10s %s\n", p.ID, p.User, p.Host, p.Time, p.State, info))
	}
	result.RawOutput = sb.String()
	return result, nil
}
```

Create `internal/operations/sql_diag/topsql.go`:

```go
package sql_diag

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func topSQL(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	limit := req.Params["limit"]
	if limit == "" {
		limit = "10"
	}

	query := fmt.Sprintf(`
		SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO
		FROM information_schema.PROCESSLIST
		WHERE COMMAND != 'Sleep' AND INFO IS NOT NULL
		ORDER BY TIME DESC
		LIMIT %s
	`, limit)

	rows, err := adapter.Query(ctx, query)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("topsql query failed: %v", err)
		return result, nil
	}

	var result model.OperationResult
	result.Success = true
	result.Data = rows

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TopSQL (limit: %s):\n\n", limit))
	sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %s\n", "ID", "User", "Host", "Time(s)", "Query"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, row := range rows.Data {
		id := fmt.Sprintf("%v", row["ID"])
		user := fmt.Sprintf("%v", row["USER"])
		host := fmt.Sprintf("%v", row["HOST"])
		t := fmt.Sprintf("%v", row["TIME"])
		info := fmt.Sprintf("%v", row["INFO"])
		if len(info) > 60 {
			info = info[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %s\n", id, user, host, t, info))
	}
	result.RawOutput = sb.String()
	return result, nil
}
```

- [ ] **Step 3: Create session mgmt plugin**

Create `internal/operations/session_mgmt/plugin.go`:

```go
package session_mgmt

import (
	"context"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	operations.Register(&SessionMgmtPlugin{})
}

type SessionMgmtPlugin struct{}

func (p *SessionMgmtPlugin) Name() string                        { return "session_mgmt" }
func (p *SessionMgmtPlugin) Category() operations.OpCategory      { return operations.OpSessionMgmt }
func (p *SessionMgmtPlugin) Description() string                  { return "Session management: list, kill, analyze" }
func (p *SessionMgmtPlugin) SupportedOps() []string               { return []string{"list", "kill", "analyze"} }
func (p *SessionMgmtPlugin) RequiredRole(op string) operations.Role {
	if op == "kill" {
		return operations.RoleAdmin
	}
	return operations.RoleOperator
}

func (p *SessionMgmtPlugin) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	op := req.Params["op"]
	switch op {
	case "list":
		return listSessions(ctx, req)
	case "kill":
		return killSession(ctx, req)
	case "analyze":
		return analyzeSession(ctx, req)
	default:
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "unknown session_mgmt operation: " + op
		return result, nil
	}
}
```

Create `internal/operations/session_mgmt/list.go`:

```go
package session_mgmt

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func listSessions(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	processes, err := adapter.GetProcessList(ctx)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("failed to list sessions: %v", err)
		return result, nil
	}

	filter := req.Params["filter"]
	if filter == "long_running" {
		threshold := int64(60)
		if t := req.Params["threshold"]; t != "" {
			if parsed, err := strconv.ParseInt(t, 10, 64); err == nil {
				threshold = parsed
			}
		}
		var filtered []model.ProcessInfo
		for _, p := range processes {
			if p.Time >= threshold {
				filtered = append(filtered, p)
			}
		}
		processes = filtered
	}

	var result model.OperationResult
	result.Success = true
	result.Data = processes

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Sessions (%d total):\n\n", len(processes)))
	sb.WriteString(fmt.Sprintf("%-6s %-15s %-20s %-8s %-10s %s\n", "ID", "User", "Host", "Time(s)", "State", "Query"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, p := range processes {
		info := p.Info
		if len(info) > 60 {
			info = info[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-6d %-15s %-20s %-8d %-10s %s\n", p.ID, p.User, p.Host, p.Time, p.State, info))
	}
	result.RawOutput = sb.String()
	return result, nil
}
```

Create `internal/operations/session_mgmt/kill.go`:

```go
package session_mgmt

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func killSession(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	processIDStr := req.Params["process_id"]
	if processIDStr == "" {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "process_id parameter required"
		return result, nil
	}

	processID, err := strconv.Atoi(processIDStr)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "invalid process_id: " + processIDStr
		return result, nil
	}

	err = adapter.KillProcess(ctx, processID)
	var result model.OperationResult
	if err != nil {
		result.Success = false
		result.RawOutput = fmt.Sprintf("failed to kill process %d: %v", processID, err)
		return result, nil
	}

	result.Success = true
	result.RawOutput = fmt.Sprintf("Successfully killed process %d", processID)
	return result, nil
}
```

Create `internal/operations/session_mgmt/analyze.go`:

```go
package session_mgmt

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func analyzeSession(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	adapter, ok := req.Adapter.(adapters.DatabaseAdapter)
	if !ok {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = "adapter not available"
		return result, nil
	}

	processes, err := adapter.GetProcessList(ctx)
	if err != nil {
		var result model.OperationResult
		result.Success = false
		result.RawOutput = fmt.Sprintf("failed to get process list: %v", err)
		return result, nil
	}

	total := len(processes)
	sleeping := 0
	running := 0
	querying := 0
	longRunning := 0

	for _, p := range processes {
		switch p.Command {
		case "Sleep":
			sleeping++
		case "Query":
			querying++
			if p.Time > 60 {
				longRunning++
			}
		default:
			running++
		}
	}

	var result model.OperationResult
	result.Success = true

	var sb strings.Builder
	sb.WriteString("Session Analysis:\n\n")
	sb.WriteString(fmt.Sprintf("  Total sessions:    %d\n", total))
	sb.WriteString(fmt.Sprintf("  Sleeping:          %d\n", sleeping))
	sb.WriteString(fmt.Sprintf("  Running/Query:     %d\n", running+querying))
	sb.WriteString(fmt.Sprintf("  Long running (>60s): %d\n", longRunning))

	if longRunning > 0 {
		sb.WriteString("\nLong running sessions:\n")
		sb.WriteString(fmt.Sprintf("%-6s %-15s %-8s %s\n", "ID", "User", "Time(s)", "Query"))
		sb.WriteString(strings.Repeat("-", 80) + "\n")
		for _, p := range processes {
			if p.Time > 60 && p.Command == "Query" {
				info := p.Info
				if len(info) > 40 {
					info = info[:37] + "..."
				}
				sb.WriteString(fmt.Sprintf("%-6d %-15s %-8d %s\n", p.ID, p.User, p.Time, info))
			}
		}
	}
	result.RawOutput = sb.String()
	return result, nil
}
```

- [ ] **Step 4: Write plugin tests**

Create `internal/operations/sql_diag/plugin_test.go`:

```go
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
```

Create `internal/operations/session_mgmt/plugin_test.go`:

```go
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
```

- [ ] **Step 5: Run tests and commit**

```bash
go mod tidy
go test ./internal/operations/... -v
```

Expected: All registration and unit tests pass. Plugin execution tests for unknown op and missing params return appropriate failures.

```bash
git add internal/operations/
git commit -m "feat: add operations registry, sql_diag and session_mgmt plugins with tests"
```

---

### Task 4: Core Engine — Orchestrator, Auth, Audit, Rate Limiter

**Files:**
- Create: `internal/engine/orchestrator.go`
- Create: `internal/engine/auth.go`
- Create: `internal/engine/audit.go`
- Create: `internal/engine/rate_limiter.go`
- Create: `internal/engine/orchestrator_test.go`

- [ ] **Step 1: Create auth module**

Create `internal/engine/auth.go`:

```go
package engine

import (
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/errors"
)

type User struct {
	Name  string
	Role  operations.Role
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
```

Add `import "fmt"` to the file.

- [ ] **Step 2: Create audit logger**

Create `internal/engine/audit.go`:

```go
package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dbs-rtf/agent/pkg/model"
)

type AuditEntry struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	User      string            `json:"user"`
	Source    string            `json:"source"`
	Instance  string            `json:"instance"`
	Plugin    string            `json:"plugin"`
	Operation string            `json:"operation"`
	Params    map[string]string `json:"params,omitempty"`
	Result    string            `json:"result"`
	Duration  string            `json:"duration"`
}

type AuditLogger struct {
	mu   sync.Mutex
	path string
}

func NewAuditLogger(basePath string) (*AuditLogger, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create audit log dir: %w", err)
	}
	return &AuditLogger{path: basePath}, nil
}

func (l *AuditLogger) Log(entry AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry.ID == "" {
		entry.ID = fmt.Sprintf("audit-%d", time.Now().UnixNano())
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}

	filename := filepath.Join(l.path, fmt.Sprintf("audit-%s.log", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

func (l *AuditLogger) BuildEntry(user, source string, cmd model.Command, result model.OperationResult) AuditEntry {
	params := make(map[string]string)
	for k, v := range cmd.Params {
		params[k] = v
	}
	if _, ok := params["password"]; ok {
		params["password"] = "***"
	}

	res := "success"
	if !result.Success {
		res = "failed"
	}

	return AuditEntry{
		User:      user,
		Source:    source,
		Instance:  cmd.Instance.Address(),
		Plugin:    cmd.Plugin,
		Operation: cmd.Operation,
		Params:    params,
		Result:    res,
		Duration:  result.Duration.String(),
	}
}
```

- [ ] **Step 3: Create rate limiter**

Create `internal/engine/rate_limiter.go`:

```go
package engine

import (
	"sync"
	"time"

	"github.com/dbs-rtf/agent/pkg/errors"
)

type RateLimiter struct {
	mu       sync.Mutex
	ops      map[string]time.Time
	maxConcurrent int
}

func NewRateLimiter(maxConcurrent int) *RateLimiter {
	return &RateLimiter{
		ops:           make(map[string]time.Time),
		maxConcurrent: maxConcurrent,
	}
}

func (r *RateLimiter) Allow(instanceKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.ops[instanceKey]; exists {
		return errors.New(errors.ErrTimeout,
			fmt.Sprintf("operation already in progress on %s", instanceKey))
	}

	r.ops[instanceKey] = time.Now()
	return nil
}

func (r *RateLimiter) Release(instanceKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ops, instanceKey)
}
```

Add `import "fmt"` to rate_limiter.go.

- [ ] **Step 4: Create orchestrator**

Create `internal/engine/orchestrator.go`:

```go
package engine

import (
	"context"
	"time"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/errors"
	"github.com/dbs-rtf/agent/pkg/model"
)

type Orchestrator struct {
	adapterRegistry *adapters.Registry
	pluginRegistry  *operations.Registry
	auth            *Auth
	audit           *AuditLogger
	rateLimiter     *RateLimiter
}

func NewOrchestrator(
	auth *Auth,
	audit *AuditLogger,
	rateLimiter *RateLimiter,
) *Orchestrator {
	return &Orchestrator{
		auth:        auth,
		audit:       audit,
		rateLimiter: rateLimiter,
	}
}

func (o *Orchestrator) Execute(ctx context.Context, cmd model.Command, user User, source string) (model.OperationResult, error) {
	plugin, err := operations.Get(cmd.Plugin)
	if err != nil {
		return model.OperationResult{}, err
	}

	if err := o.auth.CheckPermission(user, plugin, cmd.Operation); err != nil {
		return model.OperationResult{}, err
	}

	instanceKey := cmd.Instance.Address()
	if err := o.rateLimiter.Allow(instanceKey); err != nil {
		return model.OperationResult{}, err
	}
	defer o.rateLimiter.Release(instanceKey)

	adapter, err := adapters.New(cmd.Instance)
	if err != nil {
		return model.OperationResult{}, err
	}

	if err := adapter.Connect(ctx); err != nil {
		return model.OperationResult{}, errors.Wrap(errors.ErrConnectionFailed, "connect failed", err)
	}
	defer adapter.Close()

	req := model.OperationRequest{
		Instance: cmd.Instance,
		Adapter:  adapter,
		Params:   cmd.Params,
	}

	start := time.Now()
	result, err := plugin.Execute(ctx, req)
	result.Duration = time.Since(start)

	if o.audit != nil {
		entry := o.audit.BuildEntry(user.Name, source, cmd, result)
		o.audit.Log(entry)
	}

	return result, err
}
```

- [ ] **Step 5: Write orchestrator tests**

Create `internal/engine/orchestrator_test.go`:

```go
package engine

import (
	"testing"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/errors"
)

func TestAuthAdminAllAllowed(t *testing.T) {
	auth := &Auth{}
	user := User{Name: "admin", Role: operations.RoleAdmin}
	plugin := &operations.SQLDiagPluginStub{}

	err := auth.CheckPermission(user, plugin, "explain")
	if err != nil {
		t.Errorf("admin should have all permissions: %v", err)
	}
}

func TestAuthViewerDenied(t *testing.T) {
	auth := &Auth{}
	user := User{Name: "viewer", Role: operations.RoleViewer}
	plugin := &operations.SQLDiagPluginStub{}

	err := auth.CheckPermission(user, plugin, "explain")
	if err == nil {
		t.Error("viewer should be denied for non-read operations")
	}
}

func TestRateLimiterBlocksDuplicate(t *testing.T) {
	limiter := NewRateLimiter(1)

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
```

We need a stub plugin for testing. Add to `internal/operations/registry.go` at the end:

```go
// SQLDiagPluginStub for testing
type SQLDiagPluginStub struct{}

func (p *SQLDiagPluginStub) Name() string                        { return "sql_diag" }
func (p *SQLDiagPluginStub) Category() OpCategory                 { return OpSQLDiag }
func (p *SQLDiagPluginStub) Description() string                  { return "stub" }
func (p *SQLDiagPluginStub) SupportedOps() []string               { return []string{"explain"} }
func (p *SQLDiagPluginStub) Execute(ctx context.Context, req model.OperationRequest) (model.OperationResult, error) {
	var result model.OperationResult
	result.Success = true
	return result, nil
}
func (p *SQLDiagPluginStub) RequiredRole(op string) Role          { return RoleOperator }
```

Add `import "context"` to registry.go if not present.

- [ ] **Step 6: Run tests and commit**

```bash
go mod tidy
go test ./internal/engine/... -v
```

Expected: Auth tests pass (admin allowed, viewer denied), rate limiter blocks duplicate operations.

```bash
git add internal/engine/
git commit -m "feat: add core engine: orchestrator, auth, audit, rate limiter"
```

---

### Task 5: Engine — Workflow + Rollback

**Files:**
- Create: `internal/engine/workflow.go`
- Create: `internal/engine/rollback.go`
- Create: `internal/engine/workflow_test.go`

- [ ] **Step 1: Create workflow engine**

Create `internal/engine/workflow.go`:

```go
package engine

import (
	"context"
	"fmt"

	"github.com/dbs-rtf/agent/pkg/errors"
	"github.com/dbs-rtf/agent/pkg/model"
)

type WorkflowStep struct {
	Name    string
	Execute func(ctx context.Context) (model.OperationResult, error)
	Rollback func(ctx context.Context) error
}

type WorkflowDef struct {
	Name   string
	Steps  []WorkflowStep
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
			rollbackResults := w.rollback(ctx, def.Steps[:i], results)
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
```

- [ ] **Step 2: Create rollback module**

Create `internal/engine/rollback.go`:

```go
package engine

import (
	"context"
	"fmt"

	"github.com/dbs-rtf/agent/pkg/model"
)

type RollbackManager struct {
	commands []*model.Command
}

func NewRollbackManager() *RollbackManager {
	return &RollbackManager{}
}

func (r *RollbackManager) Add(cmd *model.Command) {
	if cmd != nil {
		r.commands = append(r.commands, cmd)
	}
}

func (r *RollbackManager) HasRollback() bool {
	return len(r.commands) > 0
}

func (r *RollbackManager) Execute(ctx context.Context, orchestrator *Orchestrator, user User) ([]model.OperationResult, error) {
	var results []model.OperationResult
	for i := len(r.commands) - 1; i >= 0; i-- {
		cmd := r.commands[i]
		result, err := orchestrator.Execute(ctx, *cmd, user, "rollback")
		if err != nil {
			results = append(results, model.OperationResult{
				Success:   false,
				RawOutput: fmt.Sprintf("rollback failed: %s.%s: %v", cmd.Plugin, cmd.Operation, err),
			})
		} else {
			results = append(results, result)
		}
	}
	return results, nil
}
```

- [ ] **Step 3: Write workflow tests**

Create `internal/engine/workflow_test.go`:

```go
package engine

import (
	"context"
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
```

- [ ] **Step 4: Run tests and commit**

```bash
go mod tidy
go test ./internal/engine/... -v
```

Expected: All workflow and rollback tests pass.

```bash
git add internal/engine/workflow.go internal/engine/rollback.go internal/engine/workflow_test.go
git commit -m "feat: add workflow engine and rollback manager with tests"
```

---

### Task 6: CLI Entry Point + Command Router

**Files:**
- Create: `internal/interface/command/router.go`
- Create: `internal/interface/command/parser.go`
- Create: `cmd/cli/main.go`
- Create: `cmd/cli/main_test.go`

- [ ] **Step 1: Create command router**

Create `internal/interface/command/router.go`:

```go
package command

import (
	"github.com/dbs-rtf/agent/pkg/model"
	"github.com/spf13/cobra"
)

type Router struct {
	rootCmd *cobra.Command
}

func NewRouter() *Router {
	root := &cobra.Command{
		Use:   "dbs-agent",
		Short: "Database Operations Agent",
	}
	return &Router{rootCmd: root}
}

func (r *Router) RootCmd() *cobra.Command {
	return r.rootCmd
}

func (r *Router) RegisterCommand(name, description string, run func(cmd *model.Command) error) {
	cobraCmd := &cobra.Command{
		Use:   name,
		Short: description,
		RunE: func(c *cobra.Command, args []string) error {
			params := make(map[string]string)
			for k, v := range c.Flags().Lookup {
				params[k] = v.Value.String()
			}

			modelCmd := &model.Command{
				Plugin:    c.Flag("plugin").Value.String(),
				Operation: c.Flag("operation").Value.String(),
				Params:    params,
				DryRun:    c.Flag("dry-run").Value.String() == "true",
			}

			return run(modelCmd)
		},
	}

	r.rootCmd.AddCommand(cobraCmd)
}
```

- [ ] **Step 2: Create CLI main**

Create `cmd/cli/main.go`:

```go
package main

import (
	"os"

	// Import plugins to trigger init() registration
	_ "github.com/dbs-rtf/agent/internal/operations/session_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/sql_diag"

	"github.com/dbs-rtf/agent/internal/interface/command"
)

func main() {
	router := command.NewRouter()

	if err := router.RootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Write CLI test**

Create `cmd/cli/main_test.go`:

```go
package main

import (
	"bytes"
	"testing"

	"github.com/dbs-rtf/agent/internal/interface/command"
)

func TestListPluginsCommand(t *testing.T) {
	router := command.NewRouter()

	buf := new(bytes.Buffer)
	router.RootCmd().SetOut(buf)
	router.RootCmd().SetArgs([]string{"list-plugins"})

	err := router.RootCmd().Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("expected plugin list output")
	}
}
```

- [ ] **Step 4: Run tests and commit**

```bash
go get github.com/spf13/cobra
go mod tidy
go test ./cmd/cli/... -v
go build ./cmd/cli/
```

Expected: Build succeeds, `./cli list-plugins` shows registered plugins.

```bash
git add cmd/cli/ internal/interface/command/
git commit -m "feat: add CLI entry point with cobra router and list-plugins command"
```

---

### Task 7: NLP Intent Parser

**Files:**
- Create: `internal/interface/nlp/templates/intent.yaml`
- Create: `internal/interface/nlp/prompts.go`
- Create: `internal/interface/nlp/intent.go`
- Create: `internal/interface/nlp/intent_test.go`

- [ ] **Step 1: Create NLP prompt template**

Create `internal/interface/nlp/templates/intent.yaml`:

```yaml
system_prompt: |
  You are a database operations assistant. Users describe operations they want to perform.
  Your task:
  1. Identify intent (choose from supported operations list)
  2. Extract key parameters (instance host, port, db_type, operation params)
  3. If info is incomplete, return missing_fields list
  4. Output strictly in JSON

  Supported operations:
  - sql_diag.explain: SQL execution plan analysis
  - sql_diag.slow_query: Slow query analysis
  - sql_diag.topsql: TopSQL analysis & kill
  - session_mgmt.list: List sessions
  - session_mgmt.kill: Terminate sessions
  - backup_restore.dump: Backup
  - backup_restore.restore: Restore
  - ha_mgmt.failover: Failover
  - ha_mgmt.health: Health check
  - param_mgmt.show: View parameters
  - param_mgmt.set: Modify parameters
  - log_analysis.collect: Log collection
  - log_analysis.analyze: Log analysis

examples:
  - user: "帮我看看 10.0.1.5:3306 上跑了很久的查询"
    output: |
      {
        "plugin": "session_mgmt",
        "operation": "list",
        "instance": {"host": "10.0.1.5", "port": 3306, "db_type": "mysql"},
        "params": {"filter": "long_running"},
        "missing_fields": []
      }
  - user: "杀掉数据库 10.0.1.5:3306 上 ID 为 12345 的会话"
    output: |
      {
        "plugin": "session_mgmt",
        "operation": "kill",
        "instance": {"host": "10.0.1.5", "port": 3306, "db_type": "mysql"},
        "params": {"process_id": "12345"},
        "missing_fields": []
      }
```

- [ ] **Step 2: Create prompt loader**

Create `internal/interface/nlp/prompts.go`:

```go
package nlp

import (
	_ "embed"
	"gopkg.in/yaml.v3"
)

//go:embed templates/intent.yaml
var intentTemplate []byte

type IntentTemplate struct {
	SystemPrompt string     `yaml:"system_prompt"`
	Examples     []Example  `yaml:"examples"`
}

type Example struct {
	User   string `yaml:"user"`
	Output string `yaml:"output"`
}

func LoadIntentTemplate() (*IntentTemplate, error) {
	var t IntentTemplate
	if err := yaml.Unmarshal(intentTemplate, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func BuildPrompt(template *IntentTemplate, userInput string) string {
	prompt := template.SystemPrompt + "\n\n"
	prompt += "User input: " + userInput + "\n"
	prompt += "Output your response in JSON format."
	return prompt
}
```

- [ ] **Step 3: Create intent parser**

Create `internal/interface/nlp/intent.go`:

```go
package nlp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dbs-rtf/agent/pkg/model"
)

type ParsedIntent struct {
	Plugin        string                    `json:"plugin"`
	Operation     string                    `json:"operation"`
	Instance      model.InstanceConfig      `json:"instance"`
	Params        map[string]string         `json:"params"`
	MissingFields []string                  `json:"missing_fields"`
}

type IntentParser struct {
	template *IntentTemplate
}

func NewIntentParser() (*IntentParser, error) {
	template, err := LoadIntentTemplate()
	if err != nil {
		return nil, fmt.Errorf("load intent template: %w", err)
	}
	return &IntentParser{template: template}, nil
}

func (p *IntentParser) Parse(ctx context.Context, userInput string) (*ParsedIntent, error) {
	prompt := BuildPrompt(p.template, userInput)

	// In production, this calls the LLM API (Claude/OpenAI etc.)
	// For now, return a placeholder error indicating LLM not configured
	return nil, fmt.Errorf("LLM provider not configured — set ANTHROPIC_API_KEY to enable NLP parsing")
}

func (p *IntentParser) ParseWithProvider(ctx context.Context, userInput string, llmCall func(ctx context.Context, prompt string) (string, error)) (*ParsedIntent, error) {
	prompt := BuildPrompt(p.template, userInput)

	response, err := llmCall(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var intent ParsedIntent
	if err := json.Unmarshal([]byte(response), &intent); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w, response: %s", err, response)
	}

	if len(intent.MissingFields) > 0 {
		return &intent, fmt.Errorf("missing fields: %v", intent.MissingFields)
	}

	return &intent, nil
}

func (p *IntentParser) ToCommand(intent *ParsedIntent) model.Command {
	return model.Command{
		Plugin:     intent.Plugin,
		Operation:  intent.Operation,
		Instance:   intent.Instance,
		Params:     intent.Params,
	}
}
```

- [ ] **Step 4: Write NLP tests**

Create `internal/interface/nlp/intent_test.go`:

```go
package nlp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestLoadIntentTemplate(t *testing.T) {
	template, err := LoadIntentTemplate()
	if err != nil {
		t.Fatalf("failed to load template: %v", err)
	}
	if template.SystemPrompt == "" {
		t.Error("system prompt should not be empty")
	}
	if len(template.Examples) == 0 {
		t.Error("template should have examples")
	}
}

func TestBuildPrompt(t *testing.T) {
	template := &IntentTemplate{
		SystemPrompt: "You are a DBA assistant.",
	}
	prompt := BuildPrompt(template, "show me slow queries")
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestIntentParserToCommand(t *testing.T) {
	parser := &IntentParser{}
	intent := &ParsedIntent{
		Plugin:    "session_mgmt",
		Operation: "list",
		Instance:  model.InstanceConfig{Host: "10.0.1.5", Port: 3306},
		Params:    map[string]string{"filter": "long_running"},
	}
	cmd := parser.ToCommand(intent)
	if cmd.Plugin != "session_mgmt" {
		t.Errorf("expected plugin session_mgmt, got %s", cmd.Plugin)
	}
	if cmd.Operation != "list" {
		t.Errorf("expected operation list, got %s", cmd.Operation)
	}
	if cmd.Instance.Host != "10.0.1.5" {
		t.Errorf("expected host 10.0.1.5, got %s", cmd.Instance.Host)
	}
}

func TestParseWithProvider(t *testing.T) {
	parser := &IntentParser{
		template: &IntentTemplate{SystemPrompt: "test"},
	}

	mockResponse := `{
		"plugin": "session_mgmt",
		"operation": "list",
		"instance": {"host": "10.0.1.5", "port": 3306},
		"params": {"filter": "long_running"},
		"missing_fields": []
	}`

	mockLLM := func(ctx context.Context, prompt string) (string, error) {
		return mockResponse, nil
	}

	intent, err := parser.ParseWithProvider(context.Background(), "test", mockLLM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if intent.Plugin != "session_mgmt" {
		t.Errorf("expected session_mgmt, got %s", intent.Plugin)
	}
}
```

- [ ] **Step 5: Run tests and commit**

```bash
go mod tidy
go test ./internal/interface/nlp/... -v
```

Expected: Template loads, prompt builds, intent→command conversion works, mock LLM parsing works.

```bash
git add internal/interface/nlp/
git commit -m "feat: add NLP intent parser with YAML template and tests"
```

---

### Task 8: API Server Entry Point

**Files:**
- Create: `internal/interface/api/handler.go`
- Create: `cmd/server/main.go`

- [ ] **Step 1: Create API handler**

Create `internal/interface/api/handler.go`:

```go
package api

import (
	"net/http"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/interface/nlp"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
	"github.com/gin-gonic/gin"
)

type Server struct {
	router       *gin.Engine
	orchestrator *engine.Orchestrator
	nlpParser    *nlp.IntentParser
	defaultUser  engine.User
}

func NewServer(orchestrator *engine.Orchestrator) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	s := &Server{
		router:       r,
		orchestrator: orchestrator,
		defaultUser:  engine.User{Name: "api-user", Role: operations.RoleAdmin},
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.router.POST("/execute", s.handleExecute)
	s.router.POST("/nlp", s.handleNLP)
	s.router.GET("/plugins", s.handleListPlugins)
	s.router.GET("/health", s.handleHealth)
}

type ExecuteRequest struct {
	Plugin     string            `json:"plugin" binding:"required"`
	Operation  string            `json:"operation" binding:"required"`
	Instance   model.InstanceConfig `json:"instance" binding:"required"`
	Params     map[string]string `json:"params"`
	DryRun     bool              `json:"dry_run"`
}

type NLRequest struct {
	Query    string `json:"query" binding:"required"`
	Instance model.InstanceConfig `json:"instance"`
}

func (s *Server) handleExecute(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cmd := model.Command{
		Plugin:    req.Plugin,
		Operation: req.Operation,
		Instance:  req.Instance,
		Params:    req.Params,
		DryRun:    req.DryRun,
	}

	result, err := s.orchestrator.Execute(c.Request.Context(), cmd, s.defaultUser, "api")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  result.Success,
		"data":     result.Data,
		"output":   result.RawOutput,
		"warnings": result.Warnings,
		"duration": result.Duration.String(),
	})
}

func (s *Server) handleNLP(c *gin.Context) {
	var req NLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if s.nlpParser == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "NLP not configured"})
		return
	}

	intent, err := s.nlpParser.Parse(c.Request.Context(), req.Query)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cmd := s.nlpParser.ToCommand(intent)
	if req.Instance.Host != "" {
		cmd.Instance = req.Instance
	}

	result, err := s.orchestrator.Execute(c.Request.Context(), cmd, s.defaultUser, "api")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"intent":   intent,
		"success":  result.Success,
		"data":     result.Data,
		"output":   result.RawOutput,
		"warnings": result.Warnings,
		"duration": result.Duration.String(),
	})
}

func (s *Server) handleListPlugins(c *gin.Context) {
	plugins := []gin.H{}
	for _, p := range operations.List() {
		plugins = append(plugins, gin.H{
			"name":        p.Name(),
			"description": p.Description(),
			"operations":  p.SupportedOps(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"plugins": plugins})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}
```

- [ ] **Step 2: Create server main**

Create `cmd/server/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"

	// Import plugins
	_ "github.com/dbs-rtf/agent/internal/operations/session_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/sql_diag"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/interface/api"
	"github.com/dbs-rtf/agent/pkg/config"
)

func main() {
	configPath := os.Getenv("DBS_CONFIG")
	if configPath == "" {
		configPath = "configs/dbs-agent.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("warning: could not load config: %v", err)
	}

	audit, err := engine.NewAuditLogger("./logs/audit/")
	if err != nil {
		log.Fatalf("failed to init audit: %v", err)
	}

	auth := &engine.Auth{}
	limiter := engine.NewRateLimiter(1)
	orchestrator := engine.NewOrchestrator(auth, audit, limiter)

	server := api.NewServer(orchestrator)

	addr := ":8080"
	if cfg != nil {
		addr = fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	log.Printf("starting dbs-agent server on %s", addr)
	if err := server.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
```

- [ ] **Step 3: Build and test**

```bash
go get github.com/gin-gonic/gin
go mod tidy
go build ./cmd/server/
go build ./cmd/cli/
```

Expected: Both CLI and server build successfully.

```bash
git add cmd/server/ internal/interface/api/
git commit -m "feat: add API server with gin, routes for execute/nlp/plugins/health"
```

---

### Task 9: Integration — Wire Everything Together + Example Config

**Files:**
- Modify: `configs/dbs-agent.example.yaml` (already created in Task 1)
- Create: `Makefile`
- Create: `README.md`

- [ ] **Step 1: Create Makefile**

Create `Makefile`:

```makefile
.PHONY: build test clean run-cli run-server

build:
	go build -o bin/dbs-agent-cli ./cmd/cli/
	go build -o bin/dbs-agent-server ./cmd/server/

test:
	go test ./... -v -count=1

clean:
	rm -rf bin/

run-cli: build
	./bin/dbs-agent-cli list-plugins

run-server: build
	./bin/dbs-agent-server
```

- [ ] **Step 2: Create README**

Create `README.md`:

```markdown
# DBS-RTF: Database Operations Agent

Multi-database operations agent framework with plugin architecture.

## Features

- **Multi-database support**: MySQL, PostgreSQL, Redis, MongoDB (plugin-based)
- **Dual interaction**: CLI + API server, with NLP intent parsing
- **Plugin architecture**: Add new operations or databases without modifying core code
- **Built-in safety**: Auth, audit logging, rate limiting, workflow engine with rollback

## Quick Start

```bash
# Build
make build

# List available plugins
./bin/dbs-agent-cli list-plugins

# Execute an operation
./bin/dbs-agent-cli exec -p session_mgmt -o list -i "10.0.1.5:3306" -P "filter=long_running"

# Start API server
./bin/dbs-agent-server

# API: execute
curl -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"plugin":"session_mgmt","operation":"list","instance":{"host":"10.0.1.5","port":3306,"type":"mysql"},"params":{"filter":"long_running"}}'
```

## Architecture

```
Interface Layer (CLI/API/NLP)
    ↓
Core Engine (Orchestrator/Auth/Audit/Workflow)
    ↓
Operations Plugins (sql_diag/session_mgmt/...)
    ↓
Database Adapters (MySQL/PostgreSQL/Redis/MongoDB)
```

## Adding a Plugin

1. Create `internal/operations/myplugin/plugin.go`
2. Implement `OperationPlugin` interface
3. Call `operations.Register(&MyPlugin{})` in `init()`

## Adding a Database Adapter

1. Create `internal/adapters/mydb/adapter.go`
2. Implement `DatabaseAdapter` interface
3. Call `adapters.Register(model.MyDB, NewAdapter)` in `init()`
```

- [ ] **Step 3: Final build and test**

```bash
make build
make test
```

Expected: All packages build, all tests pass.

```bash
git add Makefile README.md
git commit -m "docs: add Makefile and README, wire all components together"
```

---

## Summary

| Task | Component | Key Files | Tests |
|------|-----------|-----------|-------|
| 1 | Bootstrap | models, errors, config | config_test.go |
| 2 | DB Adapters | adapter registry, MySQL adapter | adapter_test.go |
| 3 | Operations Plugins | registry, sql_diag, session_mgmt | plugin tests |
| 4 | Core Engine | orchestrator, auth, audit, rate limiter | orchestrator_test.go |
| 5 | Workflow | workflow engine, rollback manager | workflow_test.go |
| 6 | CLI | cobra router, main entry | main_test.go |
| 7 | NLP | intent parser, YAML templates | intent_test.go |
| 8 | API Server | gin routes, server entry | — |
| 9 | Integration | Makefile, README | build verification |
