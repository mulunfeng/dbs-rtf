# RTO Monitor

> MySQL HA RTO + Data Loss Measurement Tool
>
> 独立脚本，与主项目松耦合。可直接运行，无需依赖项目构建。
>
> Standalone script, loosely coupled with the main project. No build required.

## Setup

```bash
pip install pymysql
```

## Monitor Mode / 监控模式

```bash
# Default: connect to 127.0.0.1:3309, 10 probes/sec
python rto_monitor.py

# Custom target
python rto_monitor.py --host 127.0.0.1 --port 3309 --interval 0.05
```

Output: real-time dot-based progress (`.` = committed, `x` = failed), auto-wrapping to terminal width.

## Verify Mode / 验证模式

After failover, compare local committed records against the database to detect data loss.

```bash
# Verify latest log file
python rto_monitor.py --verify

# Verify specific log file
python rto_monitor.py --verify --file rto_log_20260430_165000.dat
```

## Log Format / 日志格式

Each log file (`rto_log_*.dat`) contains one line per committed probe:

```
seq,insert_id,timestamp
```
