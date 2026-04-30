# DBS-RTF Agent

A plugin-based database operations agent framework for MySQL (with extensibility for PostgreSQL, Redis, MongoDB).

## Features

- **Plugin Architecture**: Extend operations without modifying core code
- **Dual Interface**: CLI + REST API server with NLP intent parsing
- **High Availability**: Automatic failover with RTO measurement and data loss verification
- **Built-in Safety**: Authentication, audit logging, rate limiting, rollback workflows

## Quick Start

### Prerequisites

- Go 1.21+
- Python 3.10+ (for RTO monitoring)

### Build

```bash
make build            # CLI + Server
make build-proxy      # MySQL Proxy (optional)
```

### CLI Usage

```bash
# List available plugins
./bin/dbs-agent-cli list-plugins

# Execute an operation
./bin/dbs-agent-cli exec -p session_mgmt -o list \
  -i "10.0.1.5" -P 3306 --db-type mysql --filter=long_running
```

### API Server

```bash
./bin/dbs-agent-server
```

Execute operations via API:

```bash
curl -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{
    "plugin": "session_mgmt",
    "operation": "list",
    "instance": {"host": "10.0.1.5", "port": 3306, "type": "mysql"},
    "params": {"filter": "long_running"}
  }'
```

## Architecture

```
Interface Layer (CLI / API / NLP)
    ↓
Core Engine (Orchestrator / Auth / Audit / Workflow)
    ↓
Operation Plugins (sql_diag / session_mgmt / ha_mgmt / ...)
    ↓
Database Adapters (MySQL / PostgreSQL / Redis / MongoDB)
```

## HA Failover

The HA subsystem provides automatic failover detection and recovery for MySQL primary-replica pairs:

```
Application ──→ :3309 (Proxy) ──→ Primary  (:3306)
                                   ↓ failover
                               Replica  (:3307) → promoted to Primary
```

- **Auto-detection**: Supervisor probes detect primary failure and auto-promote replica
- **Data consistency**: Re-provision demoted primary from new primary's GTID-consistent snapshot
- **Proxy switching**: HA Supervisor notifies proxy to redirect traffic after failover
- **RTO measurement**: `infra/rto_monitor.py` measures failover duration and verifies zero data loss

See `infra/README.md` for proxy and monitoring setup.

## Plugin Development

### Add an Operation Plugin

1. Create `internal/operations/myplugin/plugin.go`
2. Implement the `OperationPlugin` interface
3. Register in `init()`: `operations.Register(&MyPlugin{})`

See `internal/operations/ha_mgmt/plugin.go` as a reference.

### Add a Database Adapter

1. Create `internal/adapters/mydb/adapter.go`
2. Implement the `DatabaseAdapter` interface
3. Register in `init()`: `adapters.Register(model.MyDB, NewAdapter)`

See `internal/adapters/mysql/adapter.go` as a reference.

## Configuration

Copy and edit the example config:

```bash
cp configs/dbs-agent.example.yaml configs/dbs-agent.yaml
```

Set `DBS_CONFIG` env var or place at `configs/dbs-agent.yaml`.

## Testing

```bash
make test
```
