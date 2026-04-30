#!/usr/bin/env python3
"""
MySQL HA RTO + Data Loss Measurement Tool
==========================================
Direct connection via Python MySQL driver (pymysql).
Records every committed INSERT to a local file for post-failover
data-consistency verification.

Usage:
  # Install dependency
  pip install pymysql

  # Monitor (high-frequency probes, writes results to file)
  python rto_monitor.py
  python rto_monitor.py --host 127.0.0.1 --port 3309 --interval 0.1

  # Verify: compare local file vs DB to detect lost rows
  python rto_monitor.py --verify
  python rto_monitor.py --verify --file rto_log_20260430_165000.dat

Requires: Python 3.10+, pymysql
"""

import argparse
import os
import signal
import sys
import time
from datetime import datetime

try:
    import pymysql
except ImportError:
    print("ERROR: pymysql is required. Install with: pip install pymysql", file=sys.stderr)
    sys.exit(1)

# ── Defaults ────────────────────────────────────────────────────────────
DEFAULT_HOST = os.environ.get("MYSQL_HOST", "127.0.0.1")
DEFAULT_PORT = int(os.environ.get("MYSQL_PORT", "3309"))
DEFAULT_USER = os.environ.get("MYSQL_USER", "root")
DEFAULT_PASS = os.environ.get("MYSQL_PASSWORD", "rootpass123")
DEFAULT_DB  = os.environ.get("MYSQL_DB", "test")
CONNECT_TIMEOUT = 1          # seconds
PROBE_INTERVAL  = 0.1        # seconds — high frequency
TABLE_NAME      = "rto_probe"
LOG_DIR         = os.path.dirname(os.path.abspath(__file__))

# ── Colours ─────────────────────────────────────────────────────────────
class C:
    G = "\033[92m"
    R = "\033[91m"
    Y = "\033[93m"
    C = "\033[96m"
    B = "\033[1m"
    N = "\033[0m"
    @classmethod
    def off(cls):
        for k in ("G", "R", "Y", "C", "B", "N"):
            setattr(cls, k, "")


# ── Connection pool (reuse for fast probes) ─────────────────────────────
class DBConn:
    """Reusable MySQL connection that auto-reconnects on failure."""

    def __init__(self, host: str, port: int, user: str, password: str, database: str, timeout: float):
        self.host = host
        self.port = port
        self.user = user
        self.password = password
        self.database = database
        self.timeout = timeout
        self.conn: pymysql.Connection | None = None

    def connect(self) -> pymysql.Connection:
        self.conn = pymysql.connect(
            host=self.host,
            port=self.port,
            user=self.user,
            password=self.password,
            database=self.database,
            connect_timeout=self.timeout,
            read_timeout=self.timeout * 3,
            write_timeout=self.timeout * 3,
            cursorclass=pymysql.cursors.Cursor,
        )
        return self.conn

    def get(self) -> pymysql.Connection:
        if self.conn is None or not self.conn.open:
            self.conn = None
            return self.connect()
        return self.conn

    def reset(self):
        """Force reconnect (e.g. after failover)."""
        if self.conn:
            try:
                self.conn.close()
            except Exception:
                pass
        self.conn = None

    def close(self):
        if self.conn:
            try:
                self.conn.close()
            except Exception:
                pass
        self.conn = None


# ── Data file ───────────────────────────────────────────────────────────
class DataFile:
    """Append-only file recording every committed probe: <seq>,<id>,<ts>"""

    def __init__(self, path: str):
        self.path = path
        self.f = open(path, "a", buffering=1)  # line-buffered

    def append(self, seq: int, insert_id: int, ts: str):
        self.f.write(f"{seq},{insert_id},{ts}\n")

    def flush(self):
        self.f.flush()

    def close(self):
        self.f.close()

    def read_ids(self) -> list[tuple[int, int]]:
        """Return list of (seq, insert_id) from the file."""
        result = []
        with open(self.path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                parts = line.split(",")
                if len(parts) >= 2:
                    result.append((int(parts[0]), int(parts[1])))
        return result


# ── Reporter (dot-based) ───────────────────────────────────────────────
class Reporter:
    def __init__(self):
        self.seq = 0
        self._col = 0
        self.ok = 0
        self.fail = 0
        self._lat: list[float] = []
        self.events: list[dict] = []

        # Downtime tracking
        self._down_start: float | None = None
        self._down_seq: int = 0
        self._fc = 0

    def _term_width(self) -> int:
        """Return current terminal width, fallback to 80."""
        try:
            return os.get_terminal_size().columns
        except OSError:
            return 80

    def _nl(self):
        """Newline if current line has content."""
        if self._col > 0:
            print("", flush=True)
            self._col = 0

    def _pr(self, s: str, color: str = ""):
        print(f"{color}{s}{C.N}", end="", flush=True)
        self._col += len(s)

    def record(self, ok: bool, ms: float, err: str | None = None):
        self.seq += 1

        if ok:
            self.ok += 1
            self._lat.append(ms)

            # Recovered from downtime
            if self._down_start is not None:
                self._nl()
                end_ts = datetime.now().strftime("%H:%M:%S.%f")[:-3]
                rto = time.monotonic() - self._down_start
                self.events.append({
                    "at": datetime.now().strftime("%H:%M:%S.%f")[:-3],
                    "start": self._down_start_ts,
                    "end": end_ts,
                    "rto": round(rto, 3),
                    "missed": self._fc,
                })
                self._pr(f"[{end_ts} RTO={rto:.3f}s missed={self._fc}]", C.B + C.C)
                self._nl()
                self._down_start = None
                self._fc = 0

            self._pr(".", C.G)

        else:
            self.fail += 1
            if self._down_start is None:
                # First failure of this outage window
                self._nl()
                self._down_start = time.monotonic()
                self._down_start_ts = datetime.now().strftime("%H:%M:%S.%f")[:-3]
                self._down_seq = self.seq
                ts_str = datetime.now().strftime("%H:%M:%S.%f")[:-3]
                self._pr(f"[{ts_str} FAIL", C.B + C.R)
            self._fc += 1
            self._pr("x", C.R)

        # Wrap line based on current terminal width
        if self._col >= self._term_width():
            self._nl()

    def finalise(self):
        """Print final newline and summary."""
        self._nl()
        s = f"{C.B}{'=' * 72}{C.N}"
        print(f"\n{s}", flush=True)
        print(f"  {C.B}RTO + DATA LOSS REPORT{C.N}", flush=True)
        print(s, flush=True)
        print(f"  Probes total:     {self.seq}", flush=True)
        print(f"  Succeeded:        {C.G}{self.ok}{C.N}", flush=True)
        print(f"  Failed:           {C.R}{self.fail}{C.N}", flush=True)
        if self._lat:
            sl = sorted(self._lat)
            n = len(sl)
            print(f"  Latency (ms)      min={sl[0]:.1f}  avg={sum(sl) / n:.1f}  "
                  f"p50={sl[n // 2]:.1f}  p99={sl[int(n * 0.99)]:.1f}  max={sl[-1]:.1f}", flush=True)
        if self.events:
            print(f"\n  {C.B}Failover events: {len(self.events)}{C.N}", flush=True)
            print(f"  {'#':>3}  {'Start':>14}  {'End':>14}  {'RTO (s)':>9}  {'Missed':>8}", flush=True)
            print(f"  {'-' * 62}", flush=True)
            for i, e in enumerate(self.events, 1):
                print(f"  {i:>3}  {e['start']:>14}  {e['end']:>14}  {e['rto']:>9.3f}  {e['missed']:>8}", flush=True)
            v = [e["rto"] for e in self.events]
            print(f"\n  Avg RTO: {sum(v) / len(v):.3f}s  |  Min: {min(v):.3f}s  |  Max: {max(v):.3f}s", flush=True)
        else:
            print(f"\n  {C.Y}No failover detected during this run.{C.N}", flush=True)
        if self._down_start is not None:
            print(f"\n  {C.R}WARNING STILL DOWN ({time.monotonic() - self._down_start:.1f}s){C.N}", flush=True)
        print(s, flush=True)


# ── MySQL probe ─────────────────────────────────────────────────────────
def do_probe(db: DBConn, seq: int, ts: str) -> tuple[bool, int]:
    """INSERT one row via persistent connection and return (success, last_insert_id)."""
    sql = (
        f"INSERT INTO {TABLE_NAME} (seq, ts, latency_ms, host_info) "
        f"VALUES (%s, %s, 0, @@hostname)"
    )
    try:
        conn = db.get()
        cursor = conn.cursor()
        cursor.execute(sql, (seq, ts))
        insert_id = cursor.lastrowid
        conn.commit()
        cursor.close()
        return True, insert_id
    except Exception:
        db.reset()
        return False, 0


def setup_table(host: str, port: int, user: str, password: str, database: str, timeout: float):
    """Create probe table via direct connection."""
    conn = pymysql.connect(
        host=host, port=port, user=user, password=password,
        connect_timeout=timeout, cursorclass=pymysql.cursors.Cursor,
    )
    try:
        cur = conn.cursor()
        cur.execute(f"CREATE DATABASE IF NOT EXISTS {database}")
        cur.execute(f"USE {database}")
        cur.execute(f"""
            CREATE TABLE IF NOT EXISTS {TABLE_NAME} (
                id INT AUTO_INCREMENT PRIMARY KEY,
                seq INT NOT NULL,
                ts DATETIME(3) NOT NULL,
                latency_ms FLOAT NOT NULL,
                host_info VARCHAR(100)
            ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
        """)
        cur.execute(f"TRUNCATE TABLE {TABLE_NAME}")
        conn.commit()
        cur.close()
    finally:
        conn.close()


# ── Run monitor ─────────────────────────────────────────────────────────
def run(cfg):
    rpt = Reporter()
    alive = True

    # Data file
    stamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    log_path = os.path.join(LOG_DIR, f"rto_log_{stamp}.dat")
    df = DataFile(log_path)
    print(f"  {C.B}Data log: {log_path}{C.N}", flush=True)

    def stop(sig, frame):
        nonlocal alive
        alive = False
        print(f"\n  {C.Y}Signal {sig.name} -- stopping...{C.N}", flush=True)

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)

    print(f"  {C.B}MySQL RTO Monitor{C.N}", flush=True)
    print(f"  Target: {cfg.host}:{cfg.port}  |  Connect timeout: {cfg.connect_timeout}s")
    print(f"  Interval: {cfg.interval}s  |  Ctrl+C to stop\n", flush=True)
    print(f"  {C.G}.{C.N} = committed  {C.R}x{C.N} = failed", flush=True)
    print(f"  {'-' * 70}", flush=True)

    # Persistent connection for probes
    db = DBConn(cfg.host, cfg.port, cfg.user, cfg.password, cfg.database, cfg.connect_timeout)

    seq = 0
    while alive:
        seq += 1
        ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")[:-3]
        t0 = time.monotonic()

        ok, insert_id = do_probe(db, seq, ts)
        ms = (time.monotonic() - t0) * 1000

        if ok and insert_id > 0:
            df.append(seq, insert_id, ts)

        rpt.record(ok, ms)

        # Interruptible sleep
        elapsed = time.monotonic() - t0
        left = max(0, cfg.interval - elapsed)
        end = time.monotonic() + left
        while time.monotonic() < end and alive:
            time.sleep(min(0.02, end - time.monotonic()))

    db.close()
    df.flush()
    df.close()
    rpt.finalise()
    print(f"\n  Data log saved: {log_path}", flush=True)


# ── Verify: compare file vs DB ──────────────────────────────────────────
def verify(cfg):
    """Compare data file IDs against DB to detect lost rows."""
    print(f"  {C.B}Data Consistency Verification{C.N}", flush=True)

    # Pick file
    if cfg.file:
        log_path = cfg.file
    else:
        # Use latest file
        files = sorted(
            [f for f in os.listdir(LOG_DIR) if f.startswith("rto_log_") and f.endswith(".dat")],
            reverse=True,
        )
        if not files:
            print(f"  {C.R}No data log files found in {LOG_DIR}{C.N}", flush=True)
            sys.exit(1)
        log_path = os.path.join(LOG_DIR, files[0])

    print(f"  Log file:  {log_path}", flush=True)
    print(f"  DB target: {cfg.host}:{cfg.port}", flush=True)

    # Read file IDs
    df = DataFile(log_path)
    file_ids = df.read_ids()
    file_seq_set = {s for s, _ in file_ids}
    file_id_set = {i for _, i in file_ids}
    print(f"  File records: {len(file_ids)} rows (seq 1..{max(file_seq_set) if file_seq_set else 0})", flush=True)

    # Query DB for existing IDs
    print(f"  Querying DB for committed IDs...", flush=True)
    t0 = time.monotonic()

    try:
        conn = pymysql.connect(
            host=cfg.host, port=cfg.port, user=cfg.user, password=cfg.password,
            database=cfg.database, connect_timeout=10,
            read_timeout=60, cursorclass=pymysql.cursors.Cursor,
        )
        cur = conn.cursor()
        cur.execute(f"SELECT id, seq FROM {TABLE_NAME} ORDER BY id")
        rows = cur.fetchall()
        cur.close()
        conn.close()
    except Exception as e:
        print(f"  {C.R}DB query failed: {e}{C.N}", flush=True)
        sys.exit(1)

    db_ids: set[int] = set()
    db_seq_set: set[int] = set()
    for row_id, row_seq in rows:
        db_ids.add(row_id)
        db_seq_set.add(row_seq)

    elapsed = time.monotonic() - t0
    print(f"  DB records:   {len(db_ids)} rows ({elapsed:.1f}s query)", flush=True)

    # Compare: file records that are NOT in DB = data loss
    lost_ids = file_id_set - db_ids
    lost_seqs = file_seq_set - db_seq_set

    print(f"\n  {C.B}{'-' * 72}{C.N}", flush=True)

    if not lost_ids and not lost_seqs:
        print(f"  {C.G}OK ALL {len(file_ids)} records consistent -- no data loss detected{C.N}", flush=True)
        return

    # Data loss detected
    n_lost = len(lost_ids)
    print(f"  {C.R}FAIL DATA LOSS DETECTED: {n_lost} records committed but missing from DB{C.N}", flush=True)

    # Show lost ranges
    if lost_seqs:
        sorted_lost = sorted(lost_seqs)
        ranges = _to_ranges(sorted_lost)
        print(f"\n  Lost seq ranges:", flush=True)
        for r in ranges[:20]:
            print(f"    {C.R}{r}{C.N}", flush=True)
        if len(ranges) > 20:
            print(f"    {C.Y}... and {len(ranges) - 20} more ranges{C.N}", flush=True)

    if lost_ids:
        sorted_ids = sorted(lost_ids)
        id_ranges = _to_ranges(sorted_ids)
        print(f"\n  Lost id ranges:", flush=True)
        for r in id_ranges[:20]:
            print(f"    {C.R}{r}{C.N}", flush=True)
        if len(id_ranges) > 20:
            print(f"    {C.Y}... and {len(id_ranges) - 20} more ranges{C.N}", flush=True)

    loss_pct = n_lost / len(file_ids) * 100 if file_ids else 0
    print(f"\n  Loss rate: {C.R}{n_lost}/{len(file_ids)} ({loss_pct:.2f}%){C.N}", flush=True)


def _to_ranges(nums: list[int]) -> list[str]:
    """Compress a sorted list of ints into range strings like '1-5, 10, 15-20'."""
    if not nums:
        return []
    ranges = []
    start = nums[0]
    prev = nums[0]
    for n in nums[1:]:
        if n == prev + 1:
            prev = n
        else:
            if start == prev:
                ranges.append(str(start))
            else:
                ranges.append(f"{start}-{prev}")
            start = prev = n
    if start == prev:
        ranges.append(str(start))
    else:
        ranges.append(f"{start}-{prev}")
    return ranges


# ── Main ────────────────────────────────────────────────────────────────
def main():
    p = argparse.ArgumentParser(
        description="MySQL HA RTO + Data Loss Monitor",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Monitor with high-frequency probes (default 10 probes/sec)
  python rto_monitor.py

  # Monitor at custom interval
  python rto_monitor.py --interval 0.05  # 20 probes/sec

  # Verify data consistency (latest log file)
  python rto_monitor.py --verify

  # Verify specific log file
  python rto_monitor.py --verify --file rto_log_20260430_165000.dat
        """
    )

    # Monitor mode args
    p.add_argument("--host", default=DEFAULT_HOST)
    p.add_argument("--port", type=int, default=DEFAULT_PORT)
    p.add_argument("--user", default=DEFAULT_USER)
    p.add_argument("--password", default=DEFAULT_PASS)
    p.add_argument("--database", default=DEFAULT_DB)
    p.add_argument("--connect-timeout", type=float, default=CONNECT_TIMEOUT)
    p.add_argument("--interval", type=float, default=PROBE_INTERVAL)
    p.add_argument("--no-color", action="store_true")

    # Verify mode
    p.add_argument("--verify", action="store_true", help="Verify data consistency instead of monitoring")
    p.add_argument("--file", default="", help="Specific log file to verify (default: latest)")

    a = p.parse_args()

    if a.no_color:
        C.off()

    if a.verify:
        class Cfg:
            pass
        cfg = Cfg()
        cfg.host = a.host
        cfg.port = a.port
        cfg.user = a.user
        cfg.password = a.password
        cfg.database = a.database
        cfg.file = a.file
        verify(cfg)
    else:
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

        try:
            setup_table(cfg.host, cfg.port, cfg.user, cfg.password, cfg.database, cfg.connect_timeout)
        except Exception as e:
            print(f"  {C.R}Setup failed: {e}{C.N}", flush=True)
            sys.exit(1)

        run(cfg)


if __name__ == "__main__":
    main()
