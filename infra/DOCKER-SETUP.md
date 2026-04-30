# MySQL HA Test Environment — Docker Setup

> 使用 Docker 快速搭建 MySQL 主备（GTID 半同步）测试环境。
>
> Quick Docker-based MySQL primary-replica HA test environment with semi-sync replication.

## Quick Start

```bash
# 1. Create Docker network
docker network create ha-test

# 2. Start primary node
docker run -d \
  --name mysql-primary \
  --network ha-test \
  -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=rootpass123 \
  mysql:8.0 \
  --server-id=1 \
  --log-bin=mysql-bin \
  --gtid-mode=ON \
  --enforce-gtid-consistency=ON \
  --binlog-format=ROW \
  --plugin-load-add=semisync_master.so \
  --rpl-semi-sync-master-enabled=ON \
  --rpl-semi-sync-master-timeout=5000

# 3. Start replica node
docker run -d \
  --name mysql-replica \
  --network ha-test \
  -p 3307:3306 \
  -e MYSQL_ROOT_PASSWORD=rootpass123 \
  mysql:8.0 \
  --server-id=2 \
  --log-bin=mysql-bin \
  --gtid-mode=ON \
  --enforce-gtid-consistency=ON \
  --binlog-format=ROW \
  --read-only=ON \
  --relay-log=relay-bin \
  --plugin-load-add=semisync_slave.so \
  --rpl-semi-sync-slave-enabled=ON

# 4. Setup replication on replica
docker exec mysql-replica mysql -u root -prootpass123 -e "
  CHANGE MASTER TO
    MASTER_HOST='mysql-primary',
    MASTER_USER='root',
    MASTER_PASSWORD='rootpass123',
    MASTER_AUTO_POSITION=1;
  START SLAVE;
"

# 5. Verify replication
docker exec mysql-replica mysql -u root -prootpass123 -e "SHOW SLAVE STATUS\G"
```

## Verify Setup

```bash
# Check replication status (should show IO=Yes, SQL=Yes)
docker exec mysql-replica mysql -u root -prootpass123 -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"

# Check semi-sync status
docker exec mysql-primary mysql -u root -prootpass123 -e "SHOW STATUS LIKE 'Rpl_semi_sync%';"

# Check read-only on replica
docker exec mysql-replica mysql -u root -prootpass123 -e "SELECT @@global.read_only, @@global.super_read_only;"
```

## Configuration Reference

| 项目 / Item | 值 / Value |
|-------------|-----------|
| MySQL Version | 8.0 |
| Docker Network | `ha-test` |
| Primary Port | `3306` |
| Replica Port | `3307` |
| Root Password | `rootpass123` |
| GTID Mode | `ON` |
| Replication | GTID auto-position |
| Semi-sync | master + slave enabled |

## MySQL Parameters Explained

### Primary (主库)

| 参数 | 说明 |
|------|------|
| `--server-id=1` | Unique server ID for replication |
| `--log-bin=mysql-bin` | Enable binary logging |
| `--gtid-mode=ON` | Enable GTID-based replication |
| `--enforce-gtid-consistency=ON` | Prevent non-GTD-safe operations |
| `--binlog-format=ROW` | Row-based binlog (safest for replication) |
| `--plugin-load-add=semisync_master.so` | Load semi-sync master plugin |
| `--rpl-semi-sync-master-enabled=ON` | Enable semi-sync on primary |
| `--rpl-semi-sync-master-timeout=5000` | 5s timeout before fallback to async |

### Replica (备库)

| 参数 | 说明 |
|------|------|
| `--server-id=2` | Unique server ID (must differ from primary) |
| `--read-only=ON` | Prevent direct writes (replication exempt) |
| `--relay-log=relay-bin` | Enable relay log |
| `--plugin-load-add=semisync_slave.so` | Load semi-sync slave plugin |
| `--rpl-semi-sync-slave-enabled=ON` | Enable semi-sync on replica |

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
  --rpl-semi-sync-master-enabled=ON \
  --rpl-semi-sync-master-timeout=5000
```
