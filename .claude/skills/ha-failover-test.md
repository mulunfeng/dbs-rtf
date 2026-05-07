---
name: ha-failover-test
description: Standardized HA failover testing workflow for MySQL primary-replica clusters. Tests primary failover, failback, and replica-only failure scenarios with RTO/RPO measurement.
---

# HA Failover Testing Workflow

Run this skill to execute standardized HA failover tests.

## Prerequisites

- MySQL containers running: `mysql-primary` (3306) and `mysql-replica` (3307)
- Replication healthy: IO=Yes, SQL=Yes, lag=0
- HA agent running on port 8080
- Proxy running on port 3309 with management on 8081

## Setup Phase

```bash
# 1. Verify containers
docker ps --filter "name=mysql-" --format "table {{.Names}}\t{{.Status}}"

# 2. Verify replication
docker exec mysql-replica mysql -u root -prootpass123 -e "SHOW SLAVE STATUS\G" \
  | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"

# 3. Verify HA + Proxy
curl -s http://127.0.0.1:8080/monitor/status
curl -s http://127.0.0.1:8081/status

# 4. Start RTO monitor (clean old logs first)
rm -f tools/rto-monitor/rto_log_*.dat 2>/dev/null
taskkill //F //IM python.exe 2>/dev/null
sleep 1
python tools/rto-monitor/rto_monitor.py --host 127.0.0.1 --port 3309 --interval 0.05 \
  > /tmp/rto_test.log 2>&1 &
sleep 8
```

## Test 1: Primary Failover

```bash
# Kill primary
docker stop mysql-primary

# Wait for failover (HA detects at 3 consecutive failures ~15s)
sleep 25

# Check HA logs
cat /tmp/agent.log | tail -15

# Check RTO
cat /tmp/rto_test.log | grep -E "FAIL|RTO|missed"

# Verify RPO (data consistency)
python tools/rto-monitor/rto_monitor.py --verify
```

**Expected**: RTO ~12-15s, RPO=0, proxy auto-switched to replica

## Test 2: Failback (new primary fails)

```bash
# Restart old primary and set up as replica
docker start mysql-primary
sleep 15
# On old primary: RESET MASTER to clear GTID, then CHANGE MASTER TO new master
MSYS_NO_PATHCONV=1 docker exec mysql-primary mysql -u root -prootpass123 -e "
  STOP SLAVE; RESET SLAVE ALL; RESET MASTER;
  SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;
  CHANGE MASTER TO MASTER_HOST='mysql-replica', MASTER_USER='root',
    MASTER_PASSWORD='rootpass123', MASTER_AUTO_POSITION=1;
  START SLAVE;
  SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;
"
sleep 5
# Verify replication catches up
MSYS_NO_PATHCONV=1 docker exec mysql-primary mysql -u root -prootpass123 \
  -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"

# Start fresh RTO monitor
rm -f tools/rto-monitor/rto_log_*.dat 2>/dev/null
taskkill //F //IM python.exe 2>/dev/null
sleep 1
python tools/rto-monitor/rto_monitor.py --host 127.0.0.1 --port 3309 --interval 0.05 \
  > /tmp/rto_test2.log 2>&1 &
sleep 8

# Kill current master (3307)
docker stop mysql-replica
sleep 25

# Check results
cat /tmp/agent.log | tail -15
cat /tmp/rto_test2.log | grep -E "FAIL|RTO|missed"
python tools/rto-monitor/rto_monitor.py --verify
```

**Expected**: RTO ~12-15s, RPO=0, proxy auto-switched to 3306

## Test 3: Replica-Only Failure

```bash
# Restart 3307 and set up replication again
docker start mysql-replica
sleep 15
MSYS_NO_PATHCONV=1 docker exec mysql-replica mysql -u root -prootpass123 -e "
  SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;
  STOP SLAVE; RESET SLAVE ALL; RESET MASTER;
"
docker exec mysql-primary mysqldump --single-transaction --all-databases \
  --triggers --routines --events --set-gtid-purged=ON \
  -u root -prootpass123 2>/dev/null \
  | docker exec -i mysql-replica mysql -u root -prootpass123 2>/dev/null
MSYS_NO_PATHCONV=1 docker exec mysql-replica mysql -u root -prootpass123 -e "
  CHANGE MASTER TO MASTER_HOST='mysql-primary', MASTER_USER='root',
    MASTER_PASSWORD='rootpass123', MASTER_AUTO_POSITION=1;
  START SLAVE;
  SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;
"
sleep 5
# Verify replication
MSYS_NO_PATHCONV=1 docker exec mysql-replica mysql -u root -prootpass123 \
  -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind"

# Start fresh RTO monitor
rm -f tools/rto-monitor/rto_log_*.dat 2>/dev/null
taskkill //F //IM python.exe 2>/dev/null
sleep 1
python tools/rto-monitor/rto_monitor.py --host 127.0.0.1 --port 3309 --interval 0.05 \
  > /tmp/rto_test3.log 2>&1 &
sleep 8

# Kill replica only
docker stop mysql-replica
sleep 15

# Verify primary still serves traffic (no failover should trigger)
cat /tmp/rto_test3.log | tail -10
python tools/rto-monitor/rto_monitor.py --verify
```

**Expected**: RTO ~2s (single probe miss), RPO=0, primary business unaffected, no failover triggered

## Success Criteria

| Test | Max RTO | Max RPO | Proxy Switch |
|------|---------|---------|--------------|
| Primary Failover | ≤ 20s | 0 | ✅ |
| Failback | ≤ 20s | 0 | ✅ |
| Replica Only | ≤ 5s | 0 | N/A |
