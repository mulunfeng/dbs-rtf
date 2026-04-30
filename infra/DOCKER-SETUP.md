# MySQL HA Test Environment — Docker Setup

> 使用 Docker 快速搭建 MySQL 主备（GTID 半同步）测试环境。
>
> Quick Docker-based MySQL primary-replica HA test environment with semi-sync replication.

## Quick Start

```bash
# 1. Create Docker network
docker network create ha-test

# 2. Start node 1 (initialize first, then add config)
docker run -d --name mysql-primary --network ha-test -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=rootpass123 mysql:8.0

sleep 15  # wait for initialization

docker exec mysql-primary sh -c "cat > /etc/mysql/conf.d/custom.cnf << 'CNF'
[mysqld]
server-id=1
log-bin=mysql-bin
plugin-load-add=semisync_master.so
plugin-load-add=semisync_slave.so
rpl_semi_sync_master_enabled=ON
rpl_semi_sync_master_timeout=5000
rpl_semi_sync_slave_enabled=ON
CNF
"
docker restart mysql-primary && sleep 10

# 3. Start node 2 (same pattern)
docker run -d --name mysql-replica --network ha-test -p 3307:3306 \
  -e MYSQL_ROOT_PASSWORD=rootpass123 mysql:8.0

sleep 15

docker exec mysql-replica sh -c "cat > /etc/mysql/conf.d/custom.cnf << 'CNF'
[mysqld]
server-id=2
log-bin=mysql-bin
relay-log=relay-bin
read-only=ON
plugin-load-add=semisync_master.so
plugin-load-add=semisync_slave.so
rpl_semi_sync_master_enabled=ON
rpl_semi_sync_master_timeout=5000
rpl_semi_sync_slave_enabled=ON
CNF
"
docker restart mysql-replica && sleep 10

# 4. Enable GTID mode (must be done at runtime for MySQL 8.0)
docker exec mysql-primary mysql -u root -prootpass123 -e "
  SET GLOBAL enforce_gtid_consistency=ON;
  SET GLOBAL gtid_mode=OFF_PERMISSIVE;
  SET GLOBAL gtid_mode=ON_PERMISSIVE;
  SET GLOBAL gtid_mode=ON;
"

docker exec mysql-replica mysql -u root -prootpass123 -e "
  SET GLOBAL super_read_only=OFF;
  SET GLOBAL enforce_gtid_consistency=ON;
  SET GLOBAL gtid_mode=OFF_PERMISSIVE;
  SET GLOBAL gtid_mode=ON_PERMISSIVE;
  SET GLOBAL gtid_mode=ON;
  SET GLOBAL super_read_only=ON;
  SET GLOBAL read_only=ON;
"

# 5. Setup replication on node 2
docker exec mysql-replica mysql -u root -prootpass123 -e "
  CHANGE MASTER TO
    MASTER_HOST='mysql-primary',
    MASTER_USER='root',
    MASTER_PASSWORD='rootpass123',
    MASTER_AUTO_POSITION=1;
  START SLAVE;
"

# 6. Verify
docker exec mysql-replica mysql -u root -prootpass123 -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"
docker exec mysql-primary mysql -u root -prootpass123 -e "SHOW STATUS LIKE 'Rpl_semi_sync%';"
```

## Verify Setup

```bash
# Check replication status (should show IO=Yes, SQL=Yes)
docker exec mysql-replica mysql -u root -prootpass123 -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"

# Check semi-sync status on primary
docker exec mysql-primary mysql -u root -prootpass123 -e "SHOW STATUS LIKE 'Rpl_semi_sync%';"

# Check read-only on replica
docker exec mysql-replica mysql -u root -prootpass123 -e "SELECT @@global.read_only, @@global.super_read_only;"
```

## Configuration Reference

| 项目 / Item | 值 / Value |
|-------------|-----------|
| MySQL Version | 8.0 |
| Docker Network | `ha-test` |
| Node 1 Port | `3306` |
| Node 2 Port | `3307` |
| Root Password | `rootpass123` |
| GTID Mode | `ON` |
| Replication | GTID auto-position |
| Semi-sync | master + slave on both nodes |

## Important Notes

### GTID Mode in MySQL 8.0

MySQL 8.0 does not support `--gtid-mode=ON` as a command-line argument during initialization. GTID must be enabled at runtime through the gradual transition sequence:

```sql
SET GLOBAL gtid_mode=OFF_PERMISSIVE;
SET GLOBAL gtid_mode=ON_PERMISSIVE;
SET GLOBAL gtid_mode=ON;
```

### Semi-sync Plugins

Both nodes load **both** plugins (`semisync_master.so` + `semisync_slave.so`) so that after failover, the new primary can immediately accept semi-sync connections without restarting.

### Why Not docker run --parameter?

MySQL 8.0 ignores `--plugin-load-add` and `--rpl-semi-sync-*` during the database initialization phase. The correct approach is:
1. Start container with just `MYSQL_ROOT_PASSWORD`
2. Wait for initialization to complete
3. Write config to `/etc/mysql/conf.d/custom.cnf`
4. Restart container to load the config

### Both Nodes (双节点通用)

**关键点**：两个节点都加载 master 和 slave 两个插件，这样故障切换后角色互换时半同步不会断裂。

**Key**: Both nodes load both plugins so semi-sync works regardless of which role each node takes after failover.

| 参数 | 说明 |
|------|------|
| `--server-id=N` | Unique server ID (must differ between nodes) |
| `--log-bin=mysql-bin` | Enable binary logging |
| `--gtid-mode=ON` | Enable GTID-based replication |
| `--enforce-gtid-consistency=ON` | Prevent non-GTD-safe operations |
| `--binlog-format=ROW` | Row-based binlog (safest for replication) |
| `--relay-log=relay-bin` | Enable relay log (needed for replica role) |
| `--plugin-load-add=semisync_master.so` | Load semi-sync master plugin |
| `--plugin-load-add=semisync_slave.so` | Load semi-sync slave plugin |
| `--rpl-semi-sync-master-enabled=ON` | Enable semi-sync master side |
| `--rpl-semi-sync-master-timeout=5000` | 5s timeout before fallback to async |
| `--rpl-semi-sync-slave-enabled=ON` | Enable semi-sync slave side |

### Primary (主库) — 运行时动态设置

```sql
SET GLOBAL read_only=ON;
SET GLOBAL super_read_only=OFF;
```

### Replica (备库) — 运行时动态设置

```sql
SET GLOBAL read_only=ON;
SET GLOBAL super_read_only=ON;
```

## Start MySQL Proxy

业务通过 3309 端口连接，HA 切换时自动跟随。
Applications connect via port 3309, auto-follow on HA switch.

```bash
# Build proxy
make build-proxy

# Run proxy (requires configs/mysql-proxy.yaml)
./bin/mysql-proxy
```

## Start RTO Monitor

高频率探针，测量故障切换时间和数据丢失。
High-frequency probe for measuring failover duration and data loss.

```bash
pip install pymysql

# Monitor via proxy
python tools/rto-monitor/rto_monitor.py

# Verify data consistency
python tools/rto-monitor/rto_monitor.py --verify
```

## Common Operations

### Restart a container (config persists via cmd args)

```bash
docker restart mysql-primary
docker restart mysql-replica
```

### Failover test

```bash
# Kill primary — HA Supervisor should auto-promote replica
docker stop mysql-primary

# After failover, rejoin old primary as replica
docker start mysql-primary
# HA Supervisor handles demotion automatically
```

### Cleanup

```bash
docker stop mysql-primary mysql-replica
docker rm mysql-primary mysql-replica
docker network rm ha-test
```

## Persistent Data (Optional)

To preserve data across container recreation, mount a volume:

```bash
docker run -d \
  --name mysql-primary \
  --network ha-test \
  -p 3306:3306 \
  -v mysql-primary-data:/var/lib/mysql \
  -e MYSQL_ROOT_PASSWORD=rootpass123 \
  mysql:8.0 \
  --server-id=1 \
  --log-bin=mysql-bin \
  --gtid-mode=ON \
  --enforce-gtid-consistency=ON \
  --binlog-format=ROW \
  --plugin-load-add=semisync_master.so \
  --plugin-load-add=semisync_slave.so \
  --rpl-semi-sync-master-enabled=ON \
  --rpl-semi-sync-master-timeout=5000 \
  --rpl-semi-sync-slave-enabled=ON
```
