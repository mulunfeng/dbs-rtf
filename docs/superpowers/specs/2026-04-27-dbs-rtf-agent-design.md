---
name: Database Operations Agent Framework Design
date: 2026-04-27
status: draft
---

# DBS-RTF: Database Operations Agent Framework

## Overview

A multi-database operations agent supporting MySQL, PostgreSQL, Redis, and MongoDB through a plugin architecture. Provides both CLI and API server interaction modes, with natural language (NLP) and structured command parsing.

### Supported Operations

- **SQL Diagnostics** — EXPLAIN analysis, slow query detection, lock analysis, TopSQL analysis & kill
- **Session Management** — List sessions, kill sessions, session analysis
- **Backup & Restore** — Logical backup, scheduled backup, restore, restore drill
- **HA Management** — Failover, replication management, health checks
- **Parameter Management** — View/set/diff parameters, scope-aware changes
- **Log Analysis** — Log collection, pattern analysis
- **User & Permission Management** — Grant/revoke, role management

### Architecture: Layered Monolith + Plugins

```
┌─────────────────────────────────────────┐
│          Interface Layer                 │
│  ┌────────────┐    ┌──────────────────┐ │
│  │ NLP Parser │    │ Command Parser   │ │
│  │ (intent)   │    │ (structured)     │ │
│  └─────┬──────┘    └────────┬─────────┘ │
├────────┼────────────────────┼───────────┤
│        ▼       Core Engine  ▼           │
│  ┌───────────────────────────────────┐  │
│  │  Orchestrator                     │  │
│  │  - Auth  - Audit  - RateLimit    │  │
│  │  - Workflow  - Rollback           │  │
│  └──────────────┬────────────────────┘  │
│                 ▼                       │
│  ┌───────────────────────────────────┐  │
│  │  Operations Modules (plugins)      │  │
│  │  SQL│Session│Backup│HA│Param│Log  │  │
│  └──────────────┬────────────────────┘  │
│                 ▼                       │
│  ┌───────────────────────────────────┐  │
│  │  Database Adapters (plugins)       │  │
│  │  MySQL│PostgreSQL│Redis│MongoDB   │  │
│  └───────────────────────────────────┘  │
└─────────────────────────────────────────┘
```

## Directory Structure

```
dbs-rtf/
├── cmd/
│   ├── cli/main.go              # CLI entry point
│   └── server/main.go           # API Server entry point
│
├── internal/
│   ├── interface/
│   │   ├── nlp/
│   │   │   ├── intent.go        # LLM-based intent extraction
│   │   │   ├── prompts.go
│   │   │   └── templates/       # NLP prompt templates (YAML)
│   │   └── command/
│   │       ├── router.go        # CLI command routing
│   │       └── parser.go        # Flag parsing
│   │
│   ├── engine/
│   │   ├── orchestrator.go      # Task dispatch & aggregation
│   │   ├── auth.go              # Role-based permission check
│   │   ├── audit.go             # Audit logging
│   │   ├── rate_limiter.go      # Rate limiting / anti-thundering-herd
│   │   ├── workflow.go          # Multi-step operation workflow
│   │   └── rollback.go          # Rollback mechanism
│   │
│   ├── operations/
│   │   ├── registry.go          # Plugin registry
│   │   ├── sql_diag/
│   │   │   ├── plugin.go
│   │   │   ├── explain.go
│   │   │   ├── slow_query.go
│   │   │   ├── lock_analysis.go
│   │   │   └── topsql.go
│   │   ├── session_mgmt/
│   │   │   ├── plugin.go
│   │   │   ├── list.go
│   │   │   ├── kill.go
│   │   │   └── analyze.go
│   │   ├── backup_restore/
│   │   │   ├── plugin.go
│   │   │   ├── dump.go
│   │   │   ├── schedule.go
│   │   │   └── restore.go
│   │   ├── ha_mgmt/
│   │   │   ├── plugin.go
│   │   │   ├── failover.go
│   │   │   ├── replication.go
│   │   │   └── health_check.go
│   │   ├── param_mgmt/
│   │   │   ├── plugin.go
│   │   │   ├── show.go
│   │   │   ├── set.go
│   │   │   └── diff.go
│   │   ├── log_analysis/
│   │   │   ├── plugin.go
│   │   │   ├── collector.go
│   │   │   └── analyzer.go
│   │   └── user_perm/
│   │       ├── plugin.go
│   │       ├── grant.go
│   │       └── revoke.go
│   │
│   └── adapters/
│       ├── registry.go          # Adapter registry
│       ├── mysql/
│       │   ├── adapter.go
│       │   ├── conn.go
│       │   └── queries/
│       ├── postgresql/
│       │   ├── adapter.go
│       │   ├── conn.go
│       │   └── queries/
│       ├── redis/
│       │   ├── adapter.go
│       │   └── conn.go
│       └── mongodb/
│           ├── adapter.go
│           └── conn.go
│
├── pkg/
│   ├── model/
│   │   ├── instance.go          # InstanceConfig, DBType
│   │   ├── session.go           # Session, ProcessInfo
│   │   └── query.go             # Query, Result, Rows
│   ├── errors/
│   │   └── errors.go            # Unified error types
│   └── config/
│       └── config.go            # YAML/ENV config loading
│
├── configs/
│   └── dbs-agent.example.yaml   # Example configuration
│
├── docs/
│   └── superpowers/specs/
│       └── 2026-04-27-dbs-rtf-agent-design.md
│
└── go.mod
```

## Core Interfaces

### Database Adapter Interface

```go
type DBType string

const (
    MySQL      DBType = "mysql"
    PostgreSQL DBType = "postgresql"
    Redis      DBType = "redis"
    MongoDB    DBType = "mongodb"
)

type InstanceConfig struct {
    Host     string
    Port     int
    User     string
    Password string
    Database string
    Options  map[string]string
    TLS      *TLSConfig
}

type DatabaseAdapter interface {
    Type() DBType
    Version(ctx context.Context) (string, error)

    Connect(ctx context.Context) error
    Close() error
    Ping(ctx context.Context) error

    Exec(ctx context.Context, sql string, args ...any) (Result, error)
    Query(ctx context.Context, sql string, args ...any) (Rows, error)

    GetVariables(ctx context.Context, pattern string) (map[string]string, error)
    SetVariable(ctx context.Context, name, value string, scope VariableScope) error
    GetProcessList(ctx context.Context) ([]ProcessInfo, error)
    KillProcess(ctx context.Context, processID int) error

    StartBackup(ctx context.Context, opts BackupOptions) (BackupTask, error)
    GetReplicationStatus(ctx context.Context) (ReplicationStatus, error)
}
```

### Operation Plugin Interface

```go
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

type OperationPlugin interface {
    Name() string
    Category() OpCategory
    Description() string
    SupportedOps() []string
    Execute(ctx context.Context, req OperationRequest) (OperationResult, error)
    RequiredRole(op string) Role
}
```

### Orchestrator

```go
type Orchestrator struct {
    adapterRegistry  *adapters.Registry
    pluginRegistry   *operations.Registry
    auth             *Auth
    audit            *AuditLogger
    rateLimiter      *RateLimiter
}

func (o *Orchestrator) Execute(ctx context.Context, cmd Command) (Result, error)
func (o *Orchestrator) ExecuteWorkflow(ctx context.Context, steps []Command) ([]Result, error)
```

### Unified Command & Result

```go
type Command struct {
    Plugin     string
    Operation  string
    Params     map[string]string
    Instance   InstanceConfig
    DryRun     bool
    Timeout    time.Duration
}

type OperationResult struct {
    Success     bool
    Data        any
    RawOutput   string
    Duration    time.Duration
    Warnings    []string
    RollbackCmd *Command
}
```

### Auth Roles

```go
type Role string

const (
    RoleAdmin    Role = "admin"     // all operations
    RoleOperator Role = "operator"  // diagnostics, queries, session management
    RoleViewer   Role = "viewer"    // read-only
)
```

## Key Data Flows

### Flow 1: NLP SQL Diagnostic Request

```
User: "帮我查下 10.0.1.5:3306 上最慢的 10 条 SQL"

1. NLP Parser → intent: sql_diag.top_slow_queries, params: {host, port=3306, limit=10}
2. Orchestrator → Auth check → Rate limit check → resolve MySQL adapter + sql_diag plugin
3. sql_diag Plugin → adapter queries slow_query log, sorts by time, returns Top 10
4. Output → CLI: table / API: JSON
```

### Flow 2: Session Kill (Workflow)

```
User: "杀掉 10.0.1.5:3306 上运行超过 60 秒的查询"

Workflow steps:
1. PreCheck  — query ProcessList, filter > 60s
2. Confirm   — show matching sessions, wait for user approval
3. Kill      — call adapter.KillProcess() per session, log audit
4. Verify    — re-query ProcessList to confirm termination
5. Rollback  — not applicable (KILL is irreversible)
```

### Flow 3: Parameter Change (with Rollback)

```
User: "把 10.0.1.5:3306 的 max_connections 改成 2000"

Workflow steps:
1. PreCheck  — get current value, validate new value, check if dynamic or restart-required
2. Confirm   — show old→new transition, confirm
3. Apply     — adapter.SetVariable(), log audit
4. Verify    — adapter.GetVariables() to confirm change
5. Rollback  — auto-generated: SET GLOBAL max_connections = 1000
```

## Error Handling

### Error Types

```go
type DBSError struct {
    Code    ErrorCode
    Message string
    Detail  string
    Retry   bool
}

type ErrorCode string

const (
    ErrConnectionFailed  ErrorCode = "CONNECTION_FAILED"
    ErrPermissionDenied  ErrorCode = "PERMISSION_DENIED"
    ErrTimeout           ErrorCode = "OPERATION_TIMEOUT"
    ErrAdapterNotFound   ErrorCode = "ADAPTER_NOT_FOUND"
    ErrPluginNotFound    ErrorCode = "PLUGIN_NOT_FOUND"
    ErrInvalidParam      ErrorCode = "INVALID_PARAMETER"
    ErrDatabaseError     ErrorCode = "DATABASE_ERROR"
    ErrWorkflowFailed    ErrorCode = "WORKFLOW_FAILED"
)
```

### Retry Strategy

| Error              | Retry | Detail                                       |
|--------------------|-------|----------------------------------------------|
| ConnectionFailed   | 1     | Distinguish network vs auth failure           |
| PermissionDenied   | No    | Immediate reject, no DB operation             |
| Timeout            | Configurable | Engine-level default 30s, configurable  |
| DatabaseError      | No    | Pass through original error + troubleshooting hint |

## Audit Logging

```go
type AuditEntry struct {
    ID          string
    Timestamp   time.Time
    User        string
    Source      string              // "cli" | "api" | "nlp"
    Instance    InstanceConfig      // password redacted
    Plugin      string
    Operation   string
    Params      map[string]string   // sensitive fields redacted
    Result      string              // success/failed/partial
    Duration    time.Duration
    RollbackCmd *Command
}
```

**Storage modes:**
- CLI/local → JSON file (configurable path)
- Server/API → database table or external log system (ELK/Loki)

## Security Design

1. **No plaintext passwords** — config supports env var references (`${DB_PASSWORD}`), clear from memory ASAP
2. **Dangerous operation confirmation** — KILL, DROP, parameter changes require confirmation by default, skippable with `--force`
3. **Operation rate limiting** — one destructive operation per instance at a time (prevent concurrent param change conflicts)
4. **SQL injection protection** — NLP-generated SQL uses parameterized queries + whitelist validation, never raw user input
5. **TLS support** — all database connections support TLS encryption

## NLP Intent Template

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

example:
  user: "帮我看看 10.0.1.5:3306 上跑了很久的查询"
  output: |
    {
      "plugin": "session_mgmt",
      "operation": "list",
      "instance": {"host": "10.0.1.5", "port": 3306, "db_type": "mysql"},
      "params": {"filter": "long_running"},
      "missing_fields": []
    }
```

## Configuration

```yaml
# configs/dbs-agent.example.yaml
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
  mode: "file"             # file | db | elk
  path: "/var/log/dbs-audit/"

security:
  default_timeout: "30s"
  dangerous_ops_confirm: true
  max_concurrent_ops_per_instance: 1

nlp:
  provider: "claude"       # LLM provider for intent parsing
  model: "claude-sonnet-4-6"
  temperature: 0.1
```
