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
./bin/dbs-agent-cli exec -p session_mgmt -o list -i "10.0.1.5" -P 3306 --db-type mysql --filter=long_running

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
