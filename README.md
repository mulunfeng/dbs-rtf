# DBS-RTF Agent

> 基于插件化架构的数据库运维智能体框架，支持 MySQL（可扩展至 PostgreSQL、Redis、MongoDB）。
>
> A plugin-based database operations agent framework for MySQL (extensible to PostgreSQL, Redis, MongoDB).

---

## 功能特性 / Features

- **插件化架构**：无需修改核心代码即可扩展新操作
  **Plugin Architecture**: Extend operations without modifying core code
- **双模式交互**：CLI + REST API，支持 NLP 意图解析
  **Dual Interface**: CLI + REST API with NLP intent parsing
- **高可用**：自动故障切换，RTO 测量与数据丢失验证
  **High Availability**: Automatic failover with RTO measurement and data loss verification
- **安全机制**：认证、审计日志、速率限制、可回滚工作流
  **Built-in Safety**: Authentication, audit logging, rate limiting, rollback workflows

## 快速开始 / Quick Start

### 环境要求 / Prerequisites

- Go 1.21+
- Python 3.10+（RTO 监控 / for RTO monitoring）

### 构建 / Build

```bash
make build            # CLI + Server
make build-proxy      # MySQL Proxy（可选 / optional）
```

### CLI 使用 / CLI Usage

```bash
# 列出可用插件 / List available plugins
./bin/dbs-agent-cli list-plugins

# 执行操作 / Execute an operation
./bin/dbs-agent-cli exec -p session_mgmt -o list \
  -i "10.0.1.5" -P 3306 --db-type mysql --filter=long_running
```

### API 服务器 / API Server

```bash
./bin/dbs-agent-server
```

通过 API 执行操作：

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

## 架构 / Architecture

```
接口层 Interface (CLI / API / NLP)
    ↓
核心引擎 Core Engine (编排 / 认证 / 审计 / 工作流)
    ↓
操作插件 Operation Plugins (sql_diag / session_mgmt / ha_mgmt / ...)
    ↓
数据库适配器 Database Adapters (MySQL / PostgreSQL / Redis / MongoDB)
```

## 高可用故障切换 / HA Failover

HA 子系统为 MySQL 主从架构提供自动故障检测与恢复：
The HA subsystem provides automatic failover detection and recovery for MySQL primary-replica pairs:

```
应用 Application ──→ :3309 (Proxy) ──→ 主库 Primary  (:3306)
                                         ↓ 故障切换 / failover
                                     备库 Replica  (:3307) → 提升为主库 / promoted
```

- **自动检测**：Supervisor 探测主库故障，自动提升备库
  **Auto-detection**: Supervisor probes detect primary failure and auto-promote replica
- **数据一致性**：从新主库 GTID 一致快照恢复降节点
  **Data consistency**: Re-provision demoted primary from new primary's GTID-consistent snapshot
- **代理切换**：HA Supervisor 通知代理在故障切换后重定向流量
  **Proxy switching**: HA Supervisor notifies proxy to redirect traffic after failover
- **RTO 测量**：`tools/rto-monitor/rto_monitor.py` 测量故障切换时长并验证零数据丢失
  **RTO measurement**: `tools/rto-monitor/rto_monitor.py` measures failover duration and verifies zero data loss

代理与监控配置详见 `infra/README.md`。
See `infra/README.md` for proxy and monitoring setup.

## 插件开发 / Plugin Development

### 添加操作插件 / Add an Operation Plugin

1. 创建 `internal/operations/myplugin/plugin.go`
2. 实现 `OperationPlugin` 接口
3. 在 `init()` 中注册：`operations.Register(&MyPlugin{})`

参考 `internal/operations/ha_mgmt/plugin.go`。
See `internal/operations/ha_mgmt/plugin.go` as a reference.

### 添加数据库适配器 / Add a Database Adapter

1. 创建 `internal/adapters/mydb/adapter.go`
2. 实现 `DatabaseAdapter` 接口
3. 在 `init()` 中注册：`adapters.Register(model.MyDB, NewAdapter)`

参考 `internal/adapters/mysql/adapter.go`。
See `internal/adapters/mysql/adapter.go` as a reference.

## 配置 / Configuration

复制并编辑示例配置：

```bash
cp configs/dbs-agent.example.yaml configs/dbs-agent.yaml
```

设置 `DBS_CONFIG` 环境变量或放置到 `configs/dbs-agent.yaml`。
Set `DBS_CONFIG` env var or place at `configs/dbs-agent.yaml`.

## 测试 / Testing

```bash
make test
```
