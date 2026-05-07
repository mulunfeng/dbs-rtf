# MySQL 8.0 持久化配置指南

> GTID 半同步主备环境下，哪些参数必须写入配置文件才能避免重启后断裂。

## 核心问题

MySQL 8.0 下有两类参数生命周期不同：

| 类型 | 示例 | 生命周期 | 重启后 |
|------|------|----------|--------|
| 配置文件参数 | `server-id`, `gtid_mode` | 持久化 | ✅ 保留 |
| `SET GLOBAL` | `gtid_mode`, `super_read_only` | 仅运行时 | ❌ 丢失 |

**痛点**：如果 `gtid_mode` 只通过 `SET GLOBAL` 设置，备库重启后 GTID 模式变为 OFF，`CHANGE MASTER TO ... AUTO_POSITION=1` 虽然持久化了但无法生效，复制无法自动恢复。

---

## 持久化配置文件

### 主库 (`mysql-primary`, 3306)

文件路径：`/etc/mysql/conf.d/custom.cnf`

```ini
[mysqld]
server-id=1
log-bin=mysql-bin
gtid_mode=ON
enforce_gtid_consistency=ON
binlog-format=ROW
plugin-load-add=semisync_master.so
plugin-load-add=semisync_slave.so
rpl_semi_sync_master_enabled=ON
rpl_semi_sync_master_timeout=5000
rpl_semi_sync_slave_enabled=ON
```

### 备库 (`mysql-replica`, 3307)

文件路径：`/etc/mysql/conf.d/custom.cnf`

```ini
[mysqld]
server-id=2
log-bin=mysql-bin
relay-log=relay-bin
read-only=ON
super-read-only=ON
gtid_mode=ON
enforce_gtid_consistency=ON
plugin-load-add=semisync_master.so
plugin-load-add=semisync_slave.so
rpl_semi_sync_master_enabled=ON
rpl_semi_sync_master_timeout=5000
rpl_semi_sync_slave_enabled=ON
```

---

## 各参数解决的问题

### 1. `gtid_mode=ON` + `enforce_gtid_consistency=ON`

| 解决什么问题 | 具体表现 |
|---|---|
| **备库重启后复制断裂** | `CHANGE MASTER TO ... AUTO_POSITION=1` 需要 GTID 模式为 ON 才能工作。如果 GTID 是运行时通过 `SET GLOBAL` 设置的，重启后变为 OFF，复制线程无法启动，IO 和 SQL 线程都为 No |

> **注意**：MySQL 8.0 初始化阶段（`--initialize`）会忽略 `gtid_mode` 参数。所以容器首次启动时仍需先初始化，再写配置文件，最后重启容器。

### 2. `server-id=N`

| 解决什么问题 | 具体表现 |
|---|---|
| **主备节点 ID 冲突** | 两个容器使用相同 server-id 会导致复制报 Fatal Error 13117（"slave has same UUID as master"） |

### 3. `log-bin=mysql-bin`

| 解决什么问题 | 具体表现 |
|---|---|
| **二进制日志未启用** | GTID 复制依赖 binlog。不启用则备库无法获取主库变更 |

### 4. `plugin-load-add=semisync_master.so` + `semisync_slave.so`

| 解决什么问题 | 具体表现 |
|---|---|
| **重启后半同步断裂** | MySQL 重启后插件不会自动加载。如果不持久化，重启后 `Rpl_semi_sync_master_status` 变为 OFF，半同步退化为异步复制，可能导致数据丢失 |

### 5. **双节点都加载两个插件**

| 解决什么问题 | 具体表现 |
|---|---|
| **故障切换后半同步失效** | 如果备库只加载 `semisync_slave.so`，故障切换后变为主库时无法立即接受半同步连接，需要额外重启才能加载主插件 |

### 6. `rpl_semi_sync_master_enabled=ON` + `rpl_semi_sync_master_timeout=5000`

| 解决什么问题 | 具体表现 |
|---|---|
| **半同步超时行为不可控** | 默认超时时间为 10 秒，设置为 5 秒可在响应速度和可用性之间取得平衡。超过 5 秒备库无响应则自动降级为异步复制 |

### 7. `read-only=ON` + `super-read-only=ON`（仅备库）

| 解决什么问题 | 具体表现 |
|---|---|
| **备库重启后失去只读保护** | `SET GLOBAL` 是运行时设置，重启后备库恢复为读写模式。如果用户误写数据到备库，会导致主备数据不一致 |

### 8. `binlog-format=ROW`（仅主库）

| 解决什么问题 | 具体表现 |
|---|---|
| **基于语句的复制不安全** | STATEMENT 格式下某些函数（如 `UUID()`, `NOW()`）会导致主备数据不一致。ROW 格式最安全 |

### 9. `relay-log=relay-bin`（仅备库）

| 解决什么问题 | 具体表现 |
|---|---|
| **中继日志未启用** | 备库需要 relay-log 来接收和重放主库的 binlog 事件 |

---

## 写入时机

MySQL 8.0 的初始化行为特殊，**不能在 `docker run` 时直接传入配置参数**。正确流程：

```
1. docker run -e MYSQL_ROOT_PASSWORD=xxx mysql:8.0
   → 容器初始化（忽略 --plugin-load-add、--gtid-mode 等参数）

2. 等待初始化完成（~15 秒）

3. docker exec 写入配置文件到 /etc/mysql/conf.d/custom.cnf

4. docker restart 容器使配置生效
```

---

## 验证清单

重启后执行以下命令验证配置是否全部生效：

```bash
# 主库
docker exec mysql-primary mysql -u root -prootpass123 -e "
SELECT @@global.gtid_mode,
       @@global.enforce_gtid_consistency,
       @@global.server_id,
       @@global.read_only,
       @@global.super_read_only,
       @@global.rpl_semi_sync_master_enabled;
"

# 备库（额外验证复制和只读）
docker exec mysql-replica mysql -u root -prootpass123 -e "
SELECT @@global.gtid_mode,
       @@global.enforce_gtid_consistency,
       @@global.server_id,
       @@global.read_only,
       @@global.super_read_only,
       @@global.rpl_semi_sync_master_enabled;
"

# 验证复制状态
docker exec mysql-replica mysql -u root -prootpass123 -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"
```

预期结果：
- 主库：`gtid_mode=ON`, `read_only=OFF`, `semi_sync=ON`
- 备库：`gtid_mode=ON`, `read_only=ON`, `semi_sync=ON`
- 复制：`IO=Yes`, `SQL=Yes`, `lag=0`
