# Infrastructure — MySQL HA Proxy + Monitoring

## Architecture

```
Application ──→ 127.0.0.1:3309 (MySQL Proxy) ──→ mysql-primary  (127.0.0.1:3306)
                                                      ↓ failover
                                                  mysql-replica  (127.0.0.1:3307)
```

Business applications connect to port **3309** (proxy entry point). The proxy automatically routes to the current master. HA Supervisor triggers proxy switch on failover.

## Components

| Component | Port | Description |
|-----------|------|-------------|
| mysql-proxy | 3309 | TCP proxy forwarding MySQL connections |
| management API | 8081 | `/status` — current target, `/switch` — manual switch |

## MySQL Proxy

### Build

```bash
make build-proxy
```

### Run

```bash
./bin/mysql-proxy
```

### Usage

```bash
# Check current target
curl http://localhost:8081/status

# Manual switch
curl -X POST http://localhost:8081/switch \
  -d '{"host":"mysql-replica","port":"3306"}'
```

### Configuration

See `configs/mysql-proxy.yaml`:

```yaml
listen_port: 3309
management_port: 8081
initial_master: "127.0.0.1:3306"
hostname_map:
  "mysql-primary": "127.0.0.1:3306"
  "mysql-replica": "127.0.0.1:3307"
```

The `hostname_map` translates container names to host addresses. This allows the HA Supervisor to send container names while the proxy (running on the host) resolves to actual network addresses.

### Failover Behavior

- Proxy performs health checks every 5 seconds on active target
- Proxy does **not** auto-switch on failure — the active target must be promoted to read-only first
- HA Supervisor completes failover (promotes replica, sets read_only), then calls `/switch`
- Switch takes effect for new connections only; existing connections are unaffected

## RTO Monitor

> Moved to `tools/rto-monitor/` — a standalone script loosely coupled with this project.
> See `tools/rto-monitor/README.md` for usage.

High-frequency probe tool for measuring MySQL HA failover RTO and data loss.

### Setup

```bash
pip install pymysql
```

### Monitor Mode

```bash
# Default: connect to 127.0.0.1:3309, 10 probes/sec
python tools/rto-monitor/rto_monitor.py

# Custom target and interval
python tools/rto-monitor/rto_monitor.py --host 127.0.0.1 --port 3309 --interval 0.05
```

Output: real-time dot-based progress (`.` = committed, `x` = failed), auto-wrapping to terminal width.

### Verify Mode

After failover, compare local committed records against the database to detect data loss:

```bash
# Verify latest log file
python tools/rto-monitor/rto_monitor.py --verify

# Verify specific log file
python tools/rto-monitor/rto_monitor.py --verify --file tools/rto-monitor/rto_log_20260430_165000.dat
```

### Log Format

Each log file (`rto_log_*.dat`) contains one line per committed probe:

```
seq,insert_id,timestamp
```

## Production Deployment Options

### Option A: Dedicated Proxy Server

```
VIP: 10.0.1.100:3306
       ↓
  mysql-proxy (HAProxy / ProxySQL)
       ↓
  ┌────┴────┐
  primary   replica
```

Deploy proxy on a separate server. Use **ProxySQL** or **HAProxy** in production.

### Option B: keepalived + VIP

```
keepalived-master (priority 100) ──→ holds VIP 10.0.1.100
keepalived-backup  (priority 90)
```

Run keepalived on each MySQL host. VRRP protocol manages VIP failover. See `keepalived/` for Docker examples.

### Option C: Cloud Managed

- AWS: RDS Multi-AZ + automatic DNS failover
- Alibaba Cloud: RDS HA + internal domain
- Tencent Cloud: CDB HA + proxy address

## Directory Structure

```
infra/
├── mysql-proxy/
│   ├── main.go              # Go MySQL proxy with HTTP switch API
│   ├── Dockerfile           # Docker image
│   └── docker-compose.yml   # Docker Compose
├── keepalived/
│   ├── Dockerfile
│   ├── keepalived-master.conf
│   ├── keepalived-replica.conf
│   ├── check_mysql.sh       # Health check script
│   └── notify.sh            # State change notification
└── vip-manager.sh           # VIP management script
```
