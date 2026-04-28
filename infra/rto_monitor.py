#!/usr/bin/env python3
"""
MySQL HA RTO Measurement Tool
=============================
Uses Docker mysql CLI inside the MySQL containers for reliable protocol handling.
Every probe creates a brand-new connection → INSERT → close.

Usage:
  python rto_monitor.py                    # via Docker mysql container
  python rto_monitor.py --host 127.0.0.1 --port 3308
  python rto_monitor.py --no-color
  python rto_monitor.py --help

Requires: Python 3.10+, Docker, and running MySQL containers.
"""

import argparse
import atexit
import os
import signal
import subprocess
import sys
import time
from datetime import datetime


# ── Defaults ────────────────────────────────────────────────────────────
DEFAULT_HOST = os.environ.get("MYSQL_HOST", "127.0.0.1")
DEFAULT_PORT = int(os.environ.get("MYSQL_PORT", "3306"))
DEFAULT_USER = os.environ.get("MYSQL_USER", "root")
DEFAULT_PASS = os.environ.get("MYSQL_PASSWORD", "rootpass123")
DEFAULT_DB  = os.environ.get("MYSQL_DB", "test")
CONNECT_TIMEOUT = 2          # seconds
PROBE_INTERVAL  = 1          # seconds
TABLE_NAME      = "rto_probe"
MYSQL_CONTAINER = "mysql-primary"  # container that has mysql CLI


# ── Colours ─────────────────────────────────────────────────────────────
class C:
    G="\033[92m"; R="\033[91m"; Y="\033[93m"; C="\033[96m"
    B="\033[1m"; N="\033[0m"
    @classmethod
    def off(cls):
        for k in ("G","R","Y","C","B","N"):
            setattr(cls, k, "")


# ── Reporter ────────────────────────────────────────────────────────────
class Reporter:
    def __init__(self):
        self.seq = 0
        self._fs: float | None = None
        self._fc = 0
        self.ok = 0
        self.fail = 0
        self._lat: list[float] = []
        self.events: list[dict] = []

    def record(self, ok: bool, ms: float, err: str | None = None):
        self.seq += 1
        ts = datetime.now().strftime("%H:%M:%S.%f")[:-3]

        if ok:
            self.ok += 1
            self._lat.append(ms)
            line = f"  {ts}  #{self.seq:>4}  {C.G}OK  {ms:7.1f}ms{C.N}"
            if self._fs is not None:
                rto = time.monotonic() - self._fs
                self.events.append({"at": ts, "rto": round(rto, 3), "missed": self._fc})
                line += f"\n  {C.B}{C.C}  >>> RECOVERED  RTO={rto:.3f}s (missed {self._fc} probes){C.N}"
                self._fs = None
                self._fc = 0
        else:
            self.fail += 1
            if self._fs is None:
                self._fs = time.monotonic()
            self._fc += 1
            line = f"  {ts}  #{self.seq:>4}  {C.R}FAIL {ms:6.1f}ms{C.N}"
            if err:
                line += f"  {C.R}← {err}{C.N}"

        print(line)

    def report(self):
        s = f"{C.B}{'='*72}{C.N}"
        print(f"\n{s}")
        print(f"  {C.B}RTO MEASUREMENT REPORT{C.N}")
        print(s)
        print(f"  Probes total:     {self.seq}")
        print(f"  Succeeded:        {C.G}{self.ok}{C.N}")
        print(f"  Failed:           {C.R}{self.fail}{C.N}")
        if self._lat:
            sl = sorted(self._lat)
            n = len(sl)
            print(f"  Latency (ms)      min={sl[0]:.1f}  avg={sum(sl)/n:.1f}  "
                  f"p50={sl[n//2]:.1f}  p99={sl[int(n*0.99)]:.1f}  max={sl[-1]:.1f}")
        if self.events:
            print(f"\n  {C.B}Failover events: {len(self.events)}{C.N}")
            print(f"  {'#':>3}  {'Recovered':>14}  {'RTO (s)':>9}  {'Missed':>8}")
            print(f"  {'─'*42}")
            for i, e in enumerate(self.events, 1):
                print(f"  {i:>3}  {e['at']:>14}  {e['rto']:>9.3f}  {e['missed']:>8}")
            v = [e["rto"] for e in self.events]
            print(f"\n  Avg RTO: {sum(v)/len(v):.3f}s  |  Min: {min(v):.3f}s  |  Max: {max(v):.3f}s")
        else:
            print(f"\n  {C.Y}No failover detected during this run.{C.N}")
        if self._fs is not None:
            print(f"\n  {C.R}⚠ STILL DOWN ({time.monotonic()-self._fs:.1f}s){C.N}")
        print(s)
        sys.stdout.flush()


# ── MySQL probe via Docker ───────────────────────────────────────────── ─────────────────────────────────────────────
def do_probe(container: str, host: str, port: int, user: str, password: str, database: str,
             seq: int, ts: str, timeout: int) -> tuple[bool, str]:
    """Execute one INSERT via docker exec mysql CLI.
    Returns (success, error_message).
    """
    sql = (
        f"INSERT INTO {TABLE_NAME} (seq, ts, latency_ms, host_info) "
        f"VALUES ({seq}, '{ts}', 0, @@hostname); "
        f"SELECT 'OK';"
    )
    cmd = [
        "docker", "exec", container,
        "mysql",
        "-h", host,
        "-P", str(port),
        "-u", user,
        f"-p{password}",
        "--connect-timeout", str(timeout),
        database,
        "-e", sql,
    ]

    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=timeout + 2,
        )
        if result.returncode == 0 and "OK" in result.stdout:
            return True, ""
        err = (result.stderr.strip() or result.stdout.strip())[:100]
        return False, err
    except subprocess.TimeoutExpired:
        return False, f"timeout after {timeout+2}s"
    except Exception as e:
        return False, str(e)[:100]


def setup_table(container: str, user: str, password: str):
    """Create test table using docker exec."""
    cmd = [
        "docker", "exec", container,
        "mysql", "-u", user, f"-p{password}",
        "-e", f"""
            CREATE DATABASE IF NOT EXISTS test;
            USE test;
            CREATE TABLE IF NOT EXISTS {TABLE_NAME} (
                id INT AUTO_INCREMENT PRIMARY KEY,
                seq INT NOT NULL,
                ts DATETIME(3) NOT NULL,
                latency_ms FLOAT NOT NULL,
                host_info VARCHAR(100)
            ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
            TRUNCATE TABLE {TABLE_NAME};
        """,
    ]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip()[:200])


def run(cfg):
    rpt = Reporter()
    alive = True
    atexit.register(rpt.report)

    def stop(sig, frame):
        nonlocal alive
        alive = False
        print(f"\n  {C.Y}Signal {sig.name} — stopping…{C.N}")
        sys.stdout.flush()

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)

    print(f"  {C.B}MySQL RTO Monitor (Docker mode){C.N}")
    print(f"  Target: {cfg.host}:{cfg.port}  |  Container: {cfg.container}")
    print(f"  Connect timeout: {cfg.connect_timeout}s  |  Interval: {cfg.interval}s")
    print(f"  Ctrl+C to stop\n")
    print(f"  {'Timestamp':>16}  {'Seq':>5}  {'Result':>10}")
    print(f"  {'─'*60}")

    seq = 0
    while alive:
        seq += 1
        ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")[:-3]
        t0 = time.monotonic()

        ok, err = do_probe(
            cfg.container,
            cfg.host, cfg.port, cfg.user, cfg.password, cfg.database,
            seq, ts, int(cfg.connect_timeout),
        )
        ms = (time.monotonic() - t0) * 1000
        rpt.record(ok, ms, err)

        # Interruptible sleep
        elapsed = time.monotonic() - t0
        left = max(0, cfg.interval - elapsed)
        end = time.monotonic() + left
        while time.monotonic() < end and alive:
            time.sleep(min(0.05, end - time.monotonic()))

    rpt.report()


def main():
    p = argparse.ArgumentParser(description="MySQL HA RTO Monitor — Docker mode")
    p.add_argument("--host", default=DEFAULT_HOST)
    p.add_argument("--port", type=int, default=DEFAULT_PORT)
    p.add_argument("--user", default=DEFAULT_USER)
    p.add_argument("--password", default=DEFAULT_PASS)
    p.add_argument("--database", default=DEFAULT_DB)
    p.add_argument("--connect-timeout", type=float, default=CONNECT_TIMEOUT,
                   help="MySQL --connect-timeout in seconds")
    p.add_argument("--interval", type=float, default=PROBE_INTERVAL)
    p.add_argument("--container", default=MYSQL_CONTAINER,
                   help=f"Docker container with mysql CLI (default: {MYSQL_CONTAINER})")
    p.add_argument("--no-color", action="store_true")
    a = p.parse_args()

    if a.no_color:
        C.off()

    class Cfg:
        pass
    cfg = Cfg()
    cfg.host = a.host
    cfg.port = a.port
    cfg.user = a.user
    cfg.password = a.password
    cfg.database = a.database
    cfg.connect_timeout = a.connect_timeout
    cfg.interval = a.interval
    cfg.container = a.container

    try:
        setup_table(cfg.container, cfg.user, cfg.password)
    except Exception as e:
        print(f"  {C.R}Setup failed: {e}{C.N}")
        sys.exit(1)

    run(cfg)


if __name__ == "__main__":
    main()
