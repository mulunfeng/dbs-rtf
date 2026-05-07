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
- **高可用**：自动故障切换 + 旧主库自动降为备库，RTO/RPO 测量
  **High Availability**: Automatic failover with old-primary auto-demotion, RTO/RPO measurement
- **压力测试**：反复注入故障，长期验证 HA 切换稳定性
  **Stress Testing**: Repeated fault injection for long-term HA stability validation
- **安全机制**：认证、审计日志、速率限制、可回滚工作流
  **Built-in Safety**: Authentication, audit logging, rate limiting, rollback workflows

## 快速开始 / Quick Start

### 环境要求 / Prerequisites

- Go 1.21+
- Python 3.10+（RTO 监控 + 压力测试 / for RTO monitoring & stress testing）
- Docker（MySQL HA 环境 / for MySQL HA environment）

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
应用 Application ──→ :3309 (Proxy) ──→ 主库 Primary  (:3306)
                                         ↓ 故障切换 / failover
                                     备库 Replica  (:3307) → 提升为主库 / promoted
                                         ↓ 自动恢复 / auto-recovery
                                   旧主库 Old Primary → 降为备库 / demoted
```

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

HA 子系统为 MySQL 主从架构提供自动故障检测、切换与恢复：
The HA subsystem provides automatic failover detection, promotion, and recovery for MySQL primary-replica pairs:

- **自动检测**：Supervisor 探测主库故障，自动提升备库
  **Auto-detection**: Supervisor probes detect primary failure and auto-promote replica
- **自动跟随**：旧主库重启后自动降为备库并重新加入复制
  **Auto-follow**: Old primary auto-demoted to replica and rejoins replication after restart
- **复制健康检查**：主动检测备库复制线程状态，防止复制中断导致无法跟随
  **Replication health check**: Proactively monitors replica thread status to prevent replication drift
- **数据一致性**：从新主库 GTID 一致快照恢复降节点
  **Data consistency**: Re-provision demoted primary from new primary's GTID-consistent snapshot
- **代理切换**：HA Supervisor 通知代理在故障切换后重定向流量
  **Proxy switching**: HA Supervisor notifies proxy to redirect traffic after failover

### 测试场景 / Test Scenarios

| 测试 | 描述 | 最大 RTO | RPO 目标 |
|------|------|----------|----------|
| 主库故障 Primary Failover | 杀掉主库，验证自动切换 | ≤ 20s | 0 |
| 回切 Failback | 新主库故障，验证再次切换 | ≤ 20s | 0 |
| 仅备库故障 Replica Only | 杀掉备库，验证主库不受影响 | ≤ 5s | 0 |

标准化测试流程详见 `.claude/skills/ha-failover-test.md`。
See `.claude/skills/ha-failover-test.md` for the standardized test workflow.

## 压力测试 / Stress Testing

`tools/ha-stress-test/ha_stress_test.py` 支持反复注入故障，长期验证 HA 切换稳定性。每轮自动测量 RTO 和 RPO，任何一轮超过阈值则立即停止并输出报告。

`tools/ha-stress-test/ha_stress_test.py` supports repeated fault injection for long-term HA stability validation. Each round auto-measures RTO and RPO; any round exceeding thresholds stops immediately with a report.

```bash
pip install pymysql

# 无限轮次运行 / Run indefinitely
python tools/ha-stress-test/ha_stress_test.py

# 指定轮次 / Specify rounds
python tools/ha-stress-test/ha_stress_test.py --rounds 50

# 仅测试备库故障 / Only test replica failure
python tools/ha-stress-test/ha_stress_test.py --scenario replica-only

# 自定义间隔 / Custom interval
python tools/ha-stress-test/ha_stress_test.py --interval 30
```

### 监控工具容错 / Monitor Fault Tolerance

- RTO 监控进程崩溃时自动重启，避免数据断裂
  Auto-restarts the RTO monitor if it crashes, preventing data gaps
- 时间戳过滤防止读到上一轮的脏数据
  Timestamp filtering prevents reading stale values from previous rounds
- 外层异常安全网确保单个 probe 错误不会杀死整个监控
  Outer exception safety net ensures a single probe error doesn't kill the entire monitor

## RTO 测量 / RTO Measurement

`tools/rto-monitor/rto_monitor.py` 高频探针对 MySQL 进行持续写入，故障切换后验证数据一致性。

High-frequency probe tool that continuously writes to MySQL via the proxy, then verifies data consistency after failover.

```bash
# 监控模式（默认 10 次/秒）/ Monitor mode (default 10 probes/sec)
python tools/rto-monitor/rto_monitor.py

# 更高频率 / Higher frequency
python tools/rto-monitor/rto_monitor.py --interval 0.05  # 20 次/sec

# 验证数据一致性 / Verify data consistency
python tools/rto-monitor/rto_monitor.py --verify
```

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
