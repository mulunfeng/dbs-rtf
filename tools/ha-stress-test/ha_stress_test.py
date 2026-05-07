#!/usr/bin/env python3
"""
HA Failover Stress Test Tool
=============================
Repeatedly injects failures against a MySQL HA cluster, measures RTO/RPO
for each round, and auto-recovers for the next round.

Test scenarios:
  1. Primary failover (kill master, verify failover, restart old master as replica)
  2. Replica-only failure (kill replica, verify primary unaffected, restart replica)

If any round exceeds RTO thresholds or shows data loss (RPO > 0), the test
stops immediately with a failure report.

Usage:
  python ha_stress_test.py                          # run forever
  python ha_stress_test.py --rounds 50               # run 50 rounds
  python ha_stress_test.py --scenario replica-only   # only test replica failure
  python ha_stress_test.py --scenario primary-failover  # only test primary failover
  python ha_stress_test.py --interval 30             # 30s between rounds

Requires: Python 3.10+, pymysql, docker on PATH
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time
from datetime import datetime

try:
    import pymysql
except ImportError:
    print("ERROR: pymysql is required. Install with: pip install pymysql", file=sys.stderr)
    sys.exit(1)

# ── Colours ──────────────────────────────────────────────────────────────
class C:
    G = "\033[92m"
    R = "\033[91m"
    Y = "\033[93m"
    C = "\033[96m"
    B = "\033[1m"
    D = "\033[2m"
    N = "\033[0m"
    @classmethod
    def off(cls):
        for k in ("G", "R", "Y", "C", "B", "D", "N"):
            setattr(cls, k, "")

# ── RTO thresholds ───────────────────────────────────────────────────────
MAX_RTO_PRIMARY = 20.0   # seconds
MAX_RTO_REPLICA = 5.0    # seconds

# ── Paths ────────────────────────────────────────────────────────────────
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
TOOLS_DIR = os.path.dirname(SCRIPT_DIR).replace("\\", "/")  # parent of ha-stress-test = tools
RTO_MONITOR = os.path.join(TOOLS_DIR, "rto-monitor", "rto_monitor.py").replace("\\", "/")
RTO_LOG_FILE = os.path.join(SCRIPT_DIR, "rto_stress.log").replace("\\", "/")

# ── MySQL credentials ────────────────────────────────────────────────────
MYSQL_USER = "root"
MYSQL_PASS = "rootpass123"
HA_BASE = "http://127.0.0.1:8080"

# ── Container config ─────────────────────────────────────────────────────
CONTAINERS = {
    "mysql-primary": {"port": 3306, "host_label": "127.0.0.1:3306"},
    "mysql-replica":  {"port": 3307, "host_label": "127.0.0.1:3307"},
}

# ── Global stop flag ─────────────────────────────────────────────────────
alive = True

def stop(sig, frame):
    global alive
    alive = False
    print(f"\n  {C.Y}Signal received -- finishing current round then stopping...{C.N}", flush=True)

signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)

# ── Helpers ──────────────────────────────────────────────────────────────
def run_cmd(cmd, timeout=30, capture=True):
    """Run a shell command, return (returncode, stdout, stderr)."""
    result = subprocess.run(
        cmd, shell=True, capture_output=capture, timeout=timeout,
        text=True
    )
    return result.returncode, result.stdout, result.stderr

def docker_exec(container, sql, timeout=30):
    """Execute SQL inside a MySQL container."""
    cmd = f"docker exec {container} mysql -u {MYSQL_USER} -p{MYSQL_PASS} -e \"{sql}\""
    rc, out, err = run_cmd(cmd, timeout=timeout)
    return rc, out.strip(), err.strip()

def docker_exec_multi(container, sql_lines, timeout=30):
    """Execute multi-line SQL inside a container."""
    sql = "; ".join(sql_lines)
    return docker_exec(container, sql, timeout)

def container_running(name):
    rc, out, _ = run_cmd(f"docker inspect -f '{{{{.State.Running}}}}' {name}")
    return rc == 0 and out.strip().lower().strip("'") == "true"

def get_replication_status(container):
    """Return dict of replication status, or None if not a replica."""
    rc, out, err = docker_exec(container, "SHOW SLAVE STATUS", timeout=15)
    if rc != 0 or not out:
        return None
    # MySQL CLI output is tab-separated. First line is headers, second is data.
    lines = [l.strip() for l in out.split("\n") if l.strip()]
    if len(lines) < 2:
        return None
    headers = lines[0].split("\t")
    values = lines[1].split("\t")
    status = {}
    for h, v in zip(headers, values):
        status[h] = v
    return status if status else None

def is_replication_healthy(container):
    status = get_replication_status(container)
    if not status:
        return False
    io = status.get("Slave_IO_Running", "")
    sql = status.get("Slave_SQL_Running", "")
    lag = status.get("Seconds_Behind_Master", "999")
    return io == "Yes" and sql == "Yes" and lag != "NULL"

def wait_for_replication(container, timeout=60):
    """Wait until replication is healthy or timeout."""
    t0 = time.monotonic()
    while time.monotonic() - t0 < timeout:
        status = get_replication_status(container)
        if status:
            io = status.get("Slave_IO_Running", "")
            sql = status.get("Slave_SQL_Running", "")
            lag = status.get("Seconds_Behind_Master", "999")
            if io == "Yes" and sql == "Yes" and lag != "NULL":
                print(f"    {C.G}Replication healthy on {container} (lag={lag}s){C.N}", flush=True)
                return True
        time.sleep(2)
    print(f"    {C.R}Replication NOT healthy on {container} after {timeout}s timeout{C.N}", flush=True)
    return False

def set_read_only(container, value):
    """Set read_only and super_read_only on a container."""
    val = "ON" if value else "OFF"
    docker_exec_multi(container, [
        f"SET GLOBAL read_only={val}",
        f"SET GLOBAL super_read_only={val}"
    ])

def reprove_replica_from_master(master_container, replica_container):
    """Re-provision replica from master via mysqldump, then set up replication."""
    print(f"    Resetting {replica_container}...", flush=True)
    docker_exec_multi(replica_container, [
        "STOP SLAVE", "RESET SLAVE ALL", "RESET MASTER",
        "SET GLOBAL super_read_only=OFF", "SET GLOBAL read_only=OFF"
    ])

    # Dump from master and load into replica using Python subprocess pipes
    print(f"    Dumping from {master_container}...", flush=True)
    dump_cmd = [
        "docker", "exec", master_container,
        "mysqldump", "--single-transaction", "--all-databases",
        "--triggers", "--routines", "--events", "--set-gtid-purged=ON",
        "-u", MYSQL_USER, f"-p{MYSQL_PASS}"
    ]
    load_cmd = [
        "docker", "exec", "-i", replica_container,
        "mysql", "-u", MYSQL_USER, f"-p{MYSQL_PASS}"
    ]
    try:
        dump_proc = subprocess.Popen(dump_cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        load_proc = subprocess.Popen(load_cmd, stdin=dump_proc.stdout, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        dump_proc.stdout.close()
        _, load_err = load_proc.communicate(timeout=120)
        dump_ret = dump_proc.wait()
        if dump_ret != 0 or load_proc.returncode != 0:
            print(f"    {C.R}Dump/load failed: dump_rc={dump_ret} load_rc={load_proc.returncode}{C.N}", flush=True)
            if load_err:
                print(f"    {C.R}stderr: {load_err.decode(errors='replace')[:500]}{C.N}", flush=True)
            return False
    except subprocess.TimeoutExpired:
        load_proc.kill()
        dump_proc.kill()
        print(f"    {C.R}Dump/load timed out{C.N}", flush=True)
        return False

    print(f"    Setting up replication on {replica_container}...", flush=True)
    docker_exec_multi(replica_container, [
        f"CHANGE MASTER TO MASTER_HOST='{master_container}', MASTER_USER='{MYSQL_USER}', "
        f"MASTER_PASSWORD='{MYSQL_PASS}', MASTER_AUTO_POSITION=1",
        "START SLAVE",
        "SET GLOBAL read_only=ON", "SET GLOBAL super_read_only=ON"
    ])

    time.sleep(5)
    return wait_for_replication(replica_container, timeout=180)

def kill_container(name):
    print(f"    Killing {name}...", flush=True)
    run_cmd(f"docker stop {name}", timeout=30)

def start_container(name):
    print(f"    Starting {name}...", flush=True)
    run_cmd(f"docker start {name}", timeout=30)

def get_ha_events():
    """Fetch recent HA events from the agent API."""
    try:
        import urllib.request
        resp = urllib.request.urlopen(f"{HA_BASE}/monitor/status", timeout=5)
        data = json.loads(resp.read())
        return data.get("recent_events", [])
    except Exception:
        return []

def get_ha_instance_status():
    """Fetch instance statuses from HA agent."""
    try:
        import urllib.request
        resp = urllib.request.urlopen(f"{HA_BASE}/monitor/status", timeout=5)
        data = json.loads(resp.read())
        return data.get("instances", {})
    except Exception:
        return {}

def get_current_master():
    """Determine which container is the current writable master."""
    statuses = get_ha_instance_status()
    for addr, info in statuses.items():
        if info.get("State") == "healthy":
            # Check read_only
            for name, cfg in CONTAINERS.items():
                if cfg["host_label"] == addr:
                    rc, out, _ = docker_exec(name, "SELECT @@global.read_only")
                    if rc == 0 and "0" in out:
                        return name, addr
    # Fallback: check HA's active master from events
    events = get_ha_events()
    for e in reversed(events):
        if e.get("EventType") == "failover_complete":
            to_addr = e.get("To", "")
            for name, cfg in CONTAINERS.items():
                if cfg["host_label"] == to_addr:
                    return name, to_addr
    return None, None

# ── RTO Monitor wrapper ─────────────────────────────────────────────────
class RTOMonitor:
    def __init__(self):
        self.proc = None
        self._started = False
        self._log_file = None  # Track the current log file path

    def start(self):
        """Start RTO monitor if not already running."""
        if self._started:
            return self

        # Kill any existing RTO monitor process (by PID only, never all python.exe)
        self.stop()
        time.sleep(2)

        # Truncate the probe table to ensure clean state
        try:
            conn = pymysql.connect(
                host="127.0.0.1", port=3309, user=MYSQL_USER, password=MYSQL_PASS,
                database="test", connect_timeout=5, cursorclass=pymysql.cursors.Cursor,
            )
            cur = conn.cursor()
            cur.execute("TRUNCATE TABLE rto_probe")
            conn.commit()
            cur.close()
            conn.close()
        except Exception as e:
            print(f"  {C.Y}WARNING: could not truncate rto_probe table: {e}{C.N}", flush=True)

        # Clean old log files (kill processes first so Windows releases file locks)
        import glob
        log_dir = os.path.dirname(os.path.abspath(RTO_MONITOR))
        for f in glob.glob(os.path.join(log_dir, "rto_log_*.dat")):
            try:
                os.unlink(f)
            except Exception as ex:
                print(f"  {C.Y}WARNING: could not delete {f}: {ex}{C.N}", flush=True)

        # Start RTO monitor as a subprocess, redirect stdout/stderr to file
        log_out = open(RTO_LOG_FILE, "w")
        cmd = f'python "{RTO_MONITOR}" --host 127.0.0.1 --port 3309 --interval 0.05'
        self.proc = subprocess.Popen(cmd, shell=True, stdout=log_out, stderr=log_out)
        log_out.close()
        time.sleep(8)  # Let it warm up

        # Discover the log file it created
        files = sorted(glob.glob(os.path.join(log_dir, "rto_log_*.dat")))
        if files:
            self._log_file = files[-1].replace("\\", "/")
            print(f"  {C.D}RTO log file: {self._log_file}{C.N}", flush=True)

        self._started = True
        return self

    def is_alive(self):
        """Check if the RTO monitor subprocess is still running."""
        if self.proc and self.proc.poll() is None:
            return True
        return False

    def stop(self):
        if self.proc:
            try:
                run_cmd(f"taskkill //F //PID {self.proc.pid} //T 2>/dev/null", timeout=5)
            except Exception:
                pass
            try:
                self.proc.wait(timeout=3)
            except Exception:
                self.proc.kill()
            self.proc = None
        self._started = False

    def verify(self):
        """Run verification check using the tracked log file."""
        if self._log_file and os.path.exists(self._log_file):
            rc, out, err = run_cmd(f'python "{RTO_MONITOR}" --verify --file "{self._log_file}"', timeout=30)
        else:
            # Fallback: use latest file
            rc, out, err = run_cmd(f'python "{RTO_MONITOR}" --verify', timeout=30)
        return rc, out, err

# # ── Test: Primary failover ───────────────────────────────────────────────
def test_primary_failover(rto, round_num):
    """
    Kill the current master, verify failover to replica,
    then restart old master and re-provision as replica.
    """
    master_name, master_addr = get_current_master()
    if not master_name:
        return {"round": round_num, "scenario": "primary_failover", "status": "FAIL", "error": "cannot determine current master"}

    replica_name = "mysql-replica" if master_name == "mysql-primary" else "mysql-primary"

    print(f"  {C.B}Round {round_num}: Primary Failover{C.N}", flush=True)
    print(f"    Master: {master_name} ({master_addr})  |  Replica: {replica_name}", flush=True)

    # Kill master
    t0 = time.monotonic()
    kill_container(master_name)
    print(f"    Killed {master_name} at {datetime.now().strftime('%H:%M:%S')}", flush=True)

    # Record log file mtime before failover to filter stale RTO entries
    pre_failover_mtime = os.path.getmtime(RTO_LOG_FILE) if os.path.exists(RTO_LOG_FILE) else None

    # Wait for failover (3 consecutive pings + processing = ~15s)
    time.sleep(25)

    # Verify failover happened
    new_master, new_addr = get_current_master()
    if new_master != replica_name:
        return {"round": round_num, "scenario": "primary_failover", "status": "FAIL",
                "error": f"failover did not happen: expected {replica_name} as new master, got {new_master}"}

    print(f"    Failover complete: {replica_name} is now master ({new_addr})", flush=True)

    # Check if RTO monitor is still alive; restart if needed
    if not rto.is_alive():
        print(f"    {C.R}RTO monitor died mid-test — restarting...{C.N}", flush=True)
        rto._started = False  # force fresh restart
        rto.start()
        time.sleep(2)

    # Verify RPO
    rc, verify_out, _ = rto.verify()
    rpo_ok = "no data loss detected" in verify_out

    # Parse RTO from the stress log, filtering out entries written before failover
    rto_val = _parse_rto_from_log(RTO_LOG_FILE, after_timestamp=pre_failover_mtime)

    if not rpo_ok:
        return {"round": round_num, "scenario": "primary_failover", "status": "FAIL",
                "rto": rto_val, "rpo": "DATA LOSS", "error": "RPO > 0: data loss detected"}

    if rto_val is not None and rto_val > MAX_RTO_PRIMARY:
        return {"round": round_num, "scenario": "primary_failover", "status": "FAIL",
                "rto": rto_val, "rpo": 0, "error": f"RTO {rto_val:.3f}s exceeded threshold {MAX_RTO_PRIMARY}s"}

    rto_str = f"{rto_val:.3f}" if rto_val is not None else "N/A"
    print(f"    RTO={rto_str} (threshold {MAX_RTO_PRIMARY}s)  RPO=0  {C.G}PASS{C.N}", flush=True)

    # Recovery: start old master and let HA agent auto-demote it
    print(f"    Recovering: restarting {master_name} (HA will auto-demote)...", flush=True)
    start_container(master_name)
    if not wait_for_replication(master_name, timeout=180):
        return {"round": round_num, "scenario": "primary_failover", "status": "FAIL",
                "rto": rto_val, "rpo": 0, "error": f"HA did not auto-demote {master_name} as replica"}

    # Wait for stability between rounds
    time.sleep(5)

    return {"round": round_num, "scenario": "primary_failover", "status": "PASS",
            "rto": rto_val, "rpo": 0}


# ── Test: Replica-only failure ───────────────────────────────────────────
def test_replica_only(rto, round_num):
    """
    Kill only the replica, verify primary is unaffected,
    then restart replica and re-provision.
    """
    master_name, master_addr = get_current_master()
    if not master_name:
        return {"round": round_num, "scenario": "replica_only", "status": "FAIL", "error": "cannot determine current master"}

    replica_name = "mysql-replica" if master_name == "mysql-primary" else "mysql-primary"

    print(f"  {C.B}Round {round_num}: Replica-Only Failure{C.N}", flush=True)
    print(f"    Master: {master_name} ({master_addr})  |  Replica: {replica_name}", flush=True)

    # Kill replica only
    kill_container(replica_name)
    print(f"    Killed {replica_name} at {datetime.now().strftime('%H:%M:%S')}", flush=True)

    # Record log file mtime before failure to filter stale RTO entries
    pre_failover_mtime = os.path.getmtime(RTO_LOG_FILE) if os.path.exists(RTO_LOG_FILE) else None

    # Wait briefly — primary should be unaffected
    time.sleep(15)

    # Verify primary is still serving
    rc, out, _ = docker_exec(master_name, "SELECT 1")
    if rc != 0:
        return {"round": round_num, "scenario": "replica_only", "status": "FAIL",
                "error": "primary is NOT serving after replica failure"}

    # Verify no failover was triggered (master should still be master)
    cur_master, _ = get_current_master()
    if cur_master != master_name:
        return {"round": round_num, "scenario": "replica_only", "status": "FAIL",
                "error": f"unexpected failover triggered: master changed from {master_name} to {cur_master}"}

    # Check if RTO monitor is still alive; restart if needed
    if not rto.is_alive():
        print(f"    {C.R}RTO monitor died mid-test — restarting...{C.N}", flush=True)
        rto._started = False  # force fresh restart
        rto.start()
        time.sleep(2)

    # Verify RPO
    rc, verify_out, _ = rto.verify()
    rpo_ok = "no data loss detected" in verify_out

    rto_val = _parse_rto_from_log(RTO_LOG_FILE, after_timestamp=pre_failover_mtime)

    if not rpo_ok:
        return {"round": round_num, "scenario": "replica_only", "status": "FAIL",
                "rto": rto_val, "rpo": "DATA LOSS", "error": "RPO > 0: data loss detected"}

    # For replica-only, RTO should be minimal (single probe miss ~2s)
    if rto_val is not None and rto_val > MAX_RTO_REPLICA:
        return {"round": round_num, "scenario": "replica_only", "status": "FAIL",
                "rto": rto_val, "rpo": 0, "error": f"RTO {rto_val:.3f}s exceeded threshold {MAX_RTO_REPLICA}s"}

    rto_str = f"{rto_val:.3f}" if rto_val is not None else "N/A"
    print(f"    RTO={rto_str} (threshold {MAX_RTO_REPLICA}s)  RPO=0  {C.G}PASS{C.N}", flush=True)

    # Recovery: restart replica and let HA agent auto-demote if needed
    print(f"    Recovering: restarting {replica_name}...", flush=True)
    start_container(replica_name)
    if not wait_for_replication(replica_name, timeout=180):
        return {"round": round_num, "scenario": "replica_only", "status": "FAIL",
                "rto": rto_val, "rpo": 0, "error": f"failed to recover {replica_name}"}

    time.sleep(5)

    return {"round": round_num, "scenario": "replica_only", "status": "PASS",
            "rto": rto_val, "rpo": 0}


def _parse_rto_from_log(log_path, after_timestamp=None):
    """Parse the latest RTO value from the monitor stress log.

    If after_timestamp is given (as a time.struct_time), only consider
    entries written after that point, to avoid reading stale values.
    """
    try:
        if not os.path.exists(log_path):
            return None
        # If a timestamp filter is requested, check file mtime first
        if after_timestamp is not None:
            mtime = os.path.getmtime(log_path)
            if mtime < after_timestamp:
                return None
        with open(log_path, "r", encoding="utf-8", errors="replace") as f:
            content = f.read()
        import re
        matches = re.findall(r'RTO=([\d.]+)s', content)
        if matches:
            return float(matches[-1])
    except Exception:
        pass
    return None


def print_report(results):
    """Print final summary report."""
    total = len(results)
    passed = sum(1 for r in results if r["status"] == "PASS")
    failed = sum(1 for r in results if r["status"] == "FAIL")

    print(f"\n  {C.B}{'=' * 72}{C.N}", flush=True)
    print(f"  {C.B}HA STRESS TEST REPORT{C.N}", flush=True)
    print(f"  {C.B}{'=' * 72}{C.N}", flush=True)
    print(f"  Total rounds:   {total}", flush=True)
    print(f"  Passed:         {C.G}{passed}{C.N}", flush=True)
    print(f"  Failed:         {C.R}{failed}{C.N}", flush=True)

    if results:
        rto_values = [r["rto"] for r in results if r.get("rto") is not None]
        if rto_values:
            print(f"  RTO avg:        {sum(rto_values)/len(rto_values):.3f}s", flush=True)
            print(f"  RTO min:        {min(rto_values):.3f}s", flush=True)
            print(f"  RTO max:        {max(rto_values):.3f}s", flush=True)

    print(f"\n  {'Round':>5}  {'Scenario':<22}  {'RTO (s)':>9}  {'RPO':>5}  {'Status':<8}", flush=True)
    print(f"  {'-' * 62}", flush=True)
    for r in results:
        status_color = C.G if r["status"] == "PASS" else C.R
        rto_str = f"{r['rto']:.3f}" if r.get("rto") is not None else "N/A"
        rpo_str = str(r.get("rpo", "?"))
        print(f"  {r['round']:>5}  {r['scenario']:<22}  {rto_str:>9}  {rpo_str:>5}  {status_color}{r['status']:<4}{C.N}", flush=True)
        if r.get("error"):
            print(f"         {C.R}ERROR: {r['error']}{C.N}", flush=True)

    print(f"  {C.B}{'=' * 72}{C.N}", flush=True)
    return failed == 0


# ── Main ─────────────────────────────────────────────────────────────────
def main():
    global alive
    p = argparse.ArgumentParser(description="HA Failover Stress Test Tool")
    p.add_argument("--rounds", type=int, default=0, help="Number of rounds (0 = run forever)")
    p.add_argument("--interval", type=int, default=10, help="Seconds between rounds")
    p.add_argument("--scenario", choices=["primary-failover", "replica-only", "both"], default="both",
                   help="Which scenario(s) to test")
    p.add_argument("--no-color", action="store_true")
    args = p.parse_args()

    if args.no_color:
        C.off()

    print(f"\n  {C.B}HA Failover Stress Test{C.N}", flush=True)
    print(f"  Rounds:     {'unlimited' if args.rounds == 0 else args.rounds}", flush=True)
    print(f"  Interval:   {args.interval}s between rounds", flush=True)
    print(f"  Scenarios:  {args.scenario}", flush=True)
    print(f"  RTO limits: primary<={MAX_RTO_PRIMARY}s  replica<={MAX_RTO_REPLICA}s", flush=True)
    print(f"  RPO target: 0 (no data loss)\n", flush=True)

    # Verify prerequisites
    for name in CONTAINERS:
        if not container_running(name):
            print(f"  {C.R}ERROR: {name} is not running{C.N}", flush=True)
            sys.exit(1)

    # Start RTO monitor once
    print(f"  {C.B}Starting RTO monitor...{C.N}", flush=True)
    rto = RTOMonitor()
    rto.start()
    print(f"  {C.G}RTO monitor running{C.N}\n", flush=True)
    results = []
    round_num = 0

    # Build scenario list
    scenarios = []
    if args.scenario in ("primary-failover", "both"):
        scenarios.append(("primary_failover", test_primary_failover))
    if args.scenario in ("replica-only", "both"):
        scenarios.append(("replica_only", test_replica_only))

    while alive:
        for scenario_name, test_func in scenarios:
            if not alive:
                break

            round_num += 1
            if args.rounds > 0 and round_num > args.rounds:
                alive = False
                break

            # Ensure RTO monitor is alive before starting a new round
            if not rto.is_alive():
                print(f"  {C.R}RTO monitor not running before round {round_num} — restarting...{C.N}", flush=True)
                rto._started = False
                rto.start()
                time.sleep(2)

            result = test_func(rto, round_num)
            results.append(result)

            if result["status"] == "FAIL":
                print(f"\n  {C.R}FATAL: Round {round_num} FAILED — {result.get('error', 'unknown')}{C.N}", flush=True)
                print_report(results)
                rto.stop()
                sys.exit(1)

            # Inter-round delay
            if args.interval > 0 and alive:
                print(f"  {C.D}Waiting {args.interval}s before next round...{C.N}", flush=True)
                t_wait = time.monotonic()
                while time.monotonic() - t_wait < args.interval and alive:
                    time.sleep(1)

    rto.stop()
    if results:
        ok = print_report(results)
        sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
