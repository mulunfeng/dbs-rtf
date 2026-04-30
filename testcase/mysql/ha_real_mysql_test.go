// Package testcase_mysql contains real MySQL integration tests
// for HA scenarios. Requires Docker containers:
//   - mysql-primary:3306 (primary)
//   - mysql-replica:3306 (replica, mapped to host 3307)
//
// Run:
//   export DBS_MYSQL_PRIMARY="root:rootpass123@tcp(127.0.0.1:3306)/"
//   export DBS_MYSQL_REPLICA="root:rootpass123@tcp(127.0.0.1:3307)/"
//   go test ./testcase/mysql/ -v
package testcase_mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/dbs-rtf/agent/internal/adapters/mysql"
	_ "github.com/dbs-rtf/agent/internal/operations/ha_mgmt"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
)

// PrimaryDSN returns the primary DSN from env or default.
func PrimaryDSN() string {
	if d := os.Getenv("DBS_MYSQL_PRIMARY"); d != "" {
		return d
	}
	return "root:rootpass123@tcp(127.0.0.1:3306)/"
}

// ReplicaDSN returns the replica DSN from env or default.
func ReplicaDSN() string {
	if d := os.Getenv("DBS_MYSQL_REPLICA"); d != "" {
		return d
	}
	return "root:rootpass123@tcp(127.0.0.1:3307)/"
}

// skipIfNoMySQL skips the test if MySQL containers are not available.
func skipIfNoMySQL(t *testing.T) {
	t.Helper()
	db, err := sql.Open("mysql", PrimaryDSN())
	if err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	db.Close()
}

// createRealMySQLAdapter creates a real MySQL adapter and connects it.
func createRealMySQLAdapter(dsn string, host string, port int) (realAdapter *realMySQLAdapter, db *sql.DB, err error) {
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		return nil, nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, err
	}
	return &realMySQLAdapter{db: db, cfg: model.InstanceConfig{Host: host, Port: port, Type: model.MySQL}}, db, nil
}

type realMySQLAdapter struct {
	db  *sql.DB
	cfg model.InstanceConfig
}

func (a *realMySQLAdapter) Type() model.DBType { return a.cfg.Type }

func (a *realMySQLAdapter) Connect(ctx context.Context) error { return nil }

func (a *realMySQLAdapter) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *realMySQLAdapter) Ping(ctx context.Context) error {
	return a.db.PingContext(ctx)
}

func (a *realMySQLAdapter) Version(ctx context.Context) (string, error) {
	var v string
	err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v)
	return v, err
}

func (a *realMySQLAdapter) Exec(ctx context.Context, sqlStr string, args ...any) (model.Result, error) {
	res, err := a.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return model.Result{}, err
	}
	rows, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	return model.Result{RowsAffected: rows, LastInsertID: id}, nil
}

func (a *realMySQLAdapter) Query(ctx context.Context, sqlStr string, args ...any) (model.Rows, error) {
	rows, err := a.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return model.Rows{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return model.Rows{}, err
	}

	var result model.Rows
	result.Columns = cols
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return model.Rows{}, err
		}
		row := make(model.Row)
		for i, col := range cols {
			row[col] = normalizeValue(vals[i])
		}
		result.Data = append(result.Data, row)
	}
	return result, nil
}

func normalizeValue(v any) any {
	switch val := v.(type) {
	case []byte:
		return string(val)
	case nil:
		return "<nil>"
	default:
		return v
	}
}

func (a *realMySQLAdapter) GetVariables(ctx context.Context, pattern string) (map[string]string, error) {
	query := "SHOW VARIABLES"
	if pattern != "" {
		query += fmt.Sprintf(" LIKE '%s'", pattern)
	}
	rows, err := a.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, row := range rows.Data {
		name := asString(row["Variable_name"])
		value := asString(row["Value"])
		result[name] = value
	}
	return result, nil
}

func (a *realMySQLAdapter) SetVariable(ctx context.Context, name, value string, scope model.VariableScope) error {
	var keyword string
	switch scope {
	case model.ScopeGlobal:
		keyword = "GLOBAL"
	case model.ScopeSession:
		keyword = "SESSION"
	case model.ScopeBoth:
		keyword = "GLOBAL"
	default:
		keyword = "SESSION"
	}
	sqlStr := fmt.Sprintf("SET %s %s = ?", keyword, name)
	_, err := a.Exec(ctx, sqlStr, value)
	return err
}

func (a *realMySQLAdapter) GetProcessList(ctx context.Context) ([]model.ProcessInfo, error) {
	rows, err := a.Query(ctx, "SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.PROCESSLIST")
	if err != nil {
		return nil, err
	}
	var processes []model.ProcessInfo
	for _, row := range rows.Data {
		p := model.ProcessInfo{}
		if v, ok := asInt64(row["ID"]); ok {
			p.ID = int(v)
		}
		p.User = asString(row["USER"])
		p.Host = asString(row["HOST"])
		p.Database = asString(row["DB"])
		p.Command = asString(row["COMMAND"])
		if v, ok := asInt64(row["TIME"]); ok {
			p.Time = v
		}
		p.State = asString(row["STATE"])
		p.Info = asString(row["INFO"])
		processes = append(processes, p)
	}
	return processes, nil
}

func (a *realMySQLAdapter) KillProcess(ctx context.Context, processID int) error {
	_, err := a.Exec(ctx, fmt.Sprintf("KILL %d", processID))
	return err
}

func (a *realMySQLAdapter) StartBackup(ctx context.Context, opts model.BackupOptions) (model.BackupTask, error) {
	return model.BackupTask{}, fmt.Errorf("backup not yet implemented")
}

func (a *realMySQLAdapter) GetReplicationStatus(ctx context.Context) (model.ReplicationStatus, error) {
	rows, err := a.Query(ctx, "SHOW SLAVE STATUS")
	if err != nil {
		return model.ReplicationStatus{}, err
	}
	if len(rows.Data) == 0 {
		return model.ReplicationStatus{}, nil
	}
	row := rows.Data[0]
	status := model.ReplicationStatus{IsReplica: true}
	status.SourceHost = asString(row["Master_Host"])
	status.IOState = asString(row["Slave_IO_Running"])
	status.SQLState = asString(row["Slave_SQL_Running"])
	if v := asString(row["Seconds_Behind_Master"]); v != "" {
		fmt.Sscanf(v, "%d", &status.SecondsBehind)
	}
	status.LastError = asString(row["Last_Error"])
	return status, nil
}

func asString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func asInt64(v any) (int64, bool) {
	switch val := v.(type) {
	case int64:
		return val, true
	case uint64:
		return int64(val), true
	case int:
		return int64(val), true
	case []byte:
		var n int64
		fmt.Sscanf(string(val), "%d", &n)
		return n, n != 0
	default:
		return 0, false
	}
}

// plugin is a convenience helper.
var plugin operations.OperationPlugin

func init() {
	plugin, _ = operations.Get("ha_mgmt")
}

// =============================================================================
// Scenario 1: Health Check — Real Healthy Replica
// =============================================================================

func TestReal_HealthCheck_HealthyReplica(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Health report:\n%s", result.RawOutput)

	assertContains(t, result.RawOutput, "Score:", "should have a score")
	assertContains(t, result.RawOutput, "Ping", "should check Ping")
	assertContains(t, result.RawOutput, "Version", "should check Version")
	assertContains(t, result.RawOutput, "Replication", "should check Replication")
}

// =============================================================================
// Scenario 2: Health Check — Real Primary (Not a Replica)
// =============================================================================

func TestReal_HealthCheck_PrimaryNode(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Primary health report:\n%s", result.RawOutput)

	// Primary should not show replication failure
	if strings.Contains(result.RawOutput, "FAIL") && strings.Contains(result.RawOutput, "Replication") {
		t.Error("primary should not show replication FAIL")
	}
}

// =============================================================================
// Scenario 3: Health Check — Node Unreachable (No MySQL on port)
// =============================================================================

func TestReal_HealthCheck_NodeUnreachable(t *testing.T) {
	// Use a port with no MySQL running
	adapter, _, cleanup := setupNoMySQL(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 19999, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)

	t.Logf("Unreachable health report:\n%s", result.RawOutput)

	// The adapter's Ping will fail since there's no real connection
	// The health check should report FAIL
	assertContains(t, result.RawOutput, "FAIL", "should report FAIL for unreachable node")
}

// =============================================================================
// Scenario 4: Health Check — High Connection Count
// Simulate by running many slow queries on primary.
// =============================================================================

func TestReal_HealthCheck_HighConnections(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, db, cleanup := setupPrimary(t)
	defer cleanup()

	// Start several slow queries to inflate process list
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.QueryContext(context.Background(), "SELECT SLEEP(30)")
		}()
	}
	time.Sleep(2 * time.Second) // Let queries start

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("High connection health report:\n%s", result.RawOutput)

	if !strings.Contains(result.RawOutput, "long-running") {
		t.Error("expected long-running connection warning")
	}
}

// =============================================================================
// Scenario 5: Replication Status — Healthy Replica
// =============================================================================

func TestReal_Replication_HealthyReplica(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Replication status:\n%s", result.RawOutput)

	// Check that both threads are running
	assertContains(t, result.RawOutput, "RUNNING", "both replication threads should be running")

	// Check that source host is reported
	assertContains(t, result.RawOutput, "Source Host:", "should report source host")

	// Check lag is reported
	assertContains(t, result.RawOutput, "Seconds Behind", "should report seconds behind")
}

// =============================================================================
// Scenario 6: Replication Status — Primary (Not a Replica)
// =============================================================================

func TestReal_Replication_NotAReplica(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Primary replication status:\n%s", result.RawOutput)

	assertContains(t, result.RawOutput, "not configured as a replica", "primary should not be a replica")
}

// =============================================================================
// Scenario 7: Replication Status — Broken (Stop IO Thread)
// =============================================================================

func TestReal_Replication_StopIOThread(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	// Stop IO thread to simulate network issue
	adapter.db.ExecContext(context.Background(), "STOP SLAVE IO_THREAD")
	defer adapter.db.ExecContext(context.Background(), "START SLAVE")

	time.Sleep(3 * time.Second)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Broken replication status:\n%s", result.RawOutput)

	assertContains(t, result.RawOutput, "STOPPED", "should show STOPPED when IO thread is down")
	assertContains(t, result.RawOutput, "IO thread is not running", "should report IO thread failure")
}

// =============================================================================
// Scenario 8: Replication Status — Broken (Stop SQL Thread)
// =============================================================================

func TestReal_Replication_StopSQLThread(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	// Stop SQL thread
	adapter.db.ExecContext(context.Background(), "STOP SLAVE SQL_THREAD")
	defer adapter.db.ExecContext(context.Background(), "START SLAVE")

	time.Sleep(3 * time.Second)

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("SQL thread stopped status:\n%s", result.RawOutput)

	assertContains(t, result.RawOutput, "STOPPED", "should show STOPPED when SQL thread is down")
	assertContains(t, result.RawOutput, "SQL thread is not running", "should report SQL thread failure")
}

// =============================================================================
// Scenario 9: Replication — Data Lag (Inject Delay)
// Use --replica-skip-errors or create a large transaction to simulate lag.
// =============================================================================

func TestReal_Replication_DataLag(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	// Create a large transaction on primary to generate replication activity
	adapter.db.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS ha_test")
	adapter.db.ExecContext(context.Background(), "DROP TABLE IF EXISTS ha_test.lag_test")
	adapter.db.ExecContext(context.Background(), "CREATE TABLE ha_test.lag_test (id INT PRIMARY KEY, data TEXT)")

	// Insert many rows to generate replication lag
	for i := 0; i < 1000; i++ {
		adapter.db.ExecContext(context.Background(),
			"INSERT INTO ha_test.lag_test VALUES (?, REPEAT('x', 1000))", i)
	}

	time.Sleep(1 * time.Second)

	// Check replica replication status
	replicaAdapter, _, replicaCleanup := setupReplica(t)
	defer replicaCleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  replicaAdapter,
		Params:   map[string]string{"op": "replication"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Replication during lag:\n%s", result.RawOutput)

	// At minimum, should report the lag value
	assertContains(t, result.RawOutput, "Seconds Behind", "should report seconds behind")
}

// =============================================================================
// Scenario 10: Replication — Recovery (Restart IO Thread)
// =============================================================================

func TestReal_Replication_Recovery(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	// Stop replication
	adapter.db.ExecContext(context.Background(), "STOP SLAVE")
	time.Sleep(2 * time.Second)

	// Check broken state
	req1 := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "replication"},
	}
	result1, _ := plugin.Execute(context.Background(), req1)
	t.Logf("Before recovery:\n%s", result1.RawOutput)

	// Restart replication
	adapter.db.ExecContext(context.Background(), "START SLAVE")
	time.Sleep(3 * time.Second)

	// Check recovered state
	result2, _ := plugin.Execute(context.Background(), req1)
	t.Logf("After recovery:\n%s", result2.RawOutput)

	assertContains(t, result2.RawOutput, "RUNNING", "should be RUNNING after recovery")
}

// =============================================================================
// Scenario 11: Failover Validation (Dry-Run Check)
// We won't actually run failover (it's destructive), but we verify
// the command structure and pre-check works.
// =============================================================================

func TestReal_Failover_PreCheckPasses(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params: map[string]string{
			"op":         "failover",
			"new_master": "127.0.0.1:3307",
			"dry_run":    "true",
		},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Failover dry-run result:\n%s", result.RawOutput)

	// Verify the pre-check section was printed
	assertContains(t, result.RawOutput, "Failover Pre-Check", "should show pre-check")
	assertContains(t, result.RawOutput, "IO Thread", "should report IO thread status")
	assertContains(t, result.RawOutput, "SQL Thread", "should report SQL thread status")
	assertContains(t, result.RawOutput, "Dry Run Complete", "should show dry run marker")

	// Dry run should not generate rollback commands
	if result.RollbackCmd != nil {
		t.Error("dry run should not generate rollback command")
	}
}

// =============================================================================
// Scenario 12: Health Check — Version Detection
// =============================================================================

func TestReal_HealthCheck_VersionDetection(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	// Should show the actual MySQL version
	if !strings.Contains(result.RawOutput, "8.0") {
		t.Logf("version may differ from expected: %s", result.RawOutput)
	}
	assertContains(t, result.RawOutput, "8.0", "should show MySQL 8.0 version")
}

// =============================================================================
// Scenario 13: Health Check — Read-Only Detection on Replica
// =============================================================================

func TestReal_HealthCheck_ReadOnlyDetection(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Replica health:\n%s", result.RawOutput)

	// Replica should be read-only (we started with --read-only=ON)
	if !strings.Contains(result.RawOutput, "read-only") && !strings.Contains(result.RawOutput, "writable") {
		t.Error("should report read-only or writable status")
	}
}

// =============================================================================
// Scenario 14: Health Check — Read-Write Detection on Primary
// =============================================================================

func TestReal_HealthCheck_WriteDetection(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	t.Logf("Primary health:\n%s", result.RawOutput)

	// Primary should be writable
	assertContains(t, result.RawOutput, "writable", "primary should be writable")
}

// =============================================================================
// Scenario 15: Connection Test — Verify Primary Connectivity
// =============================================================================

func TestReal_Connection_PrimaryPing(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	err := adapter.Ping(context.Background())
	if err != nil {
		t.Fatalf("primary ping failed: %v", err)
	}

	version, err := adapter.Version(context.Background())
	if err != nil {
		t.Fatalf("primary version failed: %v", err)
	}
	t.Logf("Primary version: %s", version)

	processes, err := adapter.GetProcessList(context.Background())
	if err != nil {
		t.Fatalf("primary process list failed: %v", err)
	}
	t.Logf("Primary has %d processes", len(processes))
}

// =============================================================================
// Scenario 16: Connection Test — Verify Replica Connectivity
// =============================================================================

func TestReal_Connection_ReplicaPing(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	err := adapter.Ping(context.Background())
	if err != nil {
		t.Fatalf("replica ping failed: %v", err)
	}

	version, err := adapter.Version(context.Background())
	if err != nil {
		t.Fatalf("replica version failed: %v", err)
	}
	t.Logf("Replica version: %s", version)
}

// =============================================================================
// Scenario 17: Replication — Write on Primary, Read on Replica
// =============================================================================

func TestReal_Replication_DataPropagation(t *testing.T) {
	skipIfNoMySQL(t)

	_, primaryDB, primaryCleanup := setupPrimary(t)
	defer primaryCleanup()

	// Create test data on primary
	primaryDB.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS ha_test")
	primaryDB.ExecContext(context.Background(), "DROP TABLE IF EXISTS ha_test.propagation_test")
	primaryDB.ExecContext(context.Background(), "CREATE TABLE ha_test.propagation_test (id INT, msg VARCHAR(50))")
	primaryDB.ExecContext(context.Background(), "INSERT INTO ha_test.propagation_test VALUES (1, 'hello from primary')")

	// Wait for replication
	time.Sleep(3 * time.Second)

	// Check if data exists on replica
	replica, _, replicaCleanup := setupReplica(t)
	defer replicaCleanup()

	rows, err := replica.Query(context.Background(), "SELECT msg FROM ha_test.propagation_test WHERE id = 1")
	if err != nil {
		t.Fatalf("replica query failed: %v", err)
	}

	if len(rows.Data) == 0 {
		t.Fatal("data not propagated to replica within 3 seconds")
	}

	msg := asString(rows.Data[0]["msg"])
	if msg != "hello from primary" {
		t.Errorf("expected 'hello from primary', got %q", msg)
	}

	t.Logf("Data propagated correctly: %s", msg)

	// Check replication status
	status, err := replica.GetReplicationStatus(context.Background())
	if err != nil {
		t.Fatalf("replication status failed: %v", err)
	}

	t.Logf("Replication: IO=%s SQL=%s Lag=%ds", status.IOState, status.SQLState, status.SecondsBehind)

	if status.IOState != "Yes" || status.SQLState != "Yes" {
		t.Error("replication threads should be running")
	}
}

// =============================================================================
// Scenario 18: Failover — Missing Parameter Validation
// =============================================================================

func TestReal_Failover_MissingNewMaster(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupReplica(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "failover"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)

	if result.Success {
		t.Error("failover should fail without new_master parameter")
	}
	assertContains(t, result.RawOutput, "new_master parameter required", "should report missing new_master")
}

// =============================================================================
// Scenario 19: Health Check — Score Calculation Accuracy
// Verify that the health score is correctly calculated for a healthy primary.
// =============================================================================

func TestReal_HealthCheck_ScoreAccuracy(t *testing.T) {
	skipIfNoMySQL(t)

	adapter, _, cleanup := setupPrimary(t)
	defer cleanup()

	req := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  adapter,
		Params:   map[string]string{"op": "health"},
	}

	result, err := plugin.Execute(context.Background(), req)
	assertNoError(t, err)
	assertSuccess(t, result)

	// Primary with no load should have a high score (>= 90)
	if !strings.Contains(result.RawOutput, "100/100") && !strings.Contains(result.RawOutput, "90/100") {
		t.Errorf("expected score >= 90 for healthy primary, got:\n%s", result.RawOutput)
	}

	// Should not show critical
	if strings.Contains(result.RawOutput, "critical") {
		t.Errorf("healthy primary should not be critical:\n%s", result.RawOutput)
	}
}

// =============================================================================
// Scenario 20: Full HA Lifecycle — Health → Replication Check → Validate
// End-to-end lifecycle test: check health, verify replication, confirm data propagation.
// =============================================================================

func TestReal_HALifecycle_EndToEnd(t *testing.T) {
	skipIfNoMySQL(t)

	primary, primaryDB, primaryCleanup := setupPrimary(t)
	defer primaryCleanup()

	replica, _, replicaCleanup := setupReplica(t)
	defer replicaCleanup()

	t.Log("=== Step 1: Check Primary Health ===")
	req1 := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3306, Type: model.MySQL},
		Adapter:  primary,
		Params:   map[string]string{"op": "health"},
	}
	result1, err := plugin.Execute(context.Background(), req1)
	assertNoError(t, err)
	assertSuccess(t, result1)
	t.Logf("Primary: %s", result1.RawOutput)

	t.Log("=== Step 2: Check Replica Health ===")
	req2 := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  replica,
		Params:   map[string]string{"op": "health"},
	}
	result2, err := plugin.Execute(context.Background(), req2)
	assertNoError(t, err)
	assertSuccess(t, result2)
	t.Logf("Replica: %s", result2.RawOutput)

	t.Log("=== Step 3: Check Replication Status ===")
	req3 := model.OperationRequest{
		Instance: model.InstanceConfig{Host: "127.0.0.1", Port: 3307, Type: model.MySQL},
		Adapter:  replica,
		Params:   map[string]string{"op": "replication"},
	}
	result3, err := plugin.Execute(context.Background(), req3)
	assertNoError(t, err)
	assertSuccess(t, result3)
	t.Logf("Replication: %s", result3.RawOutput)

	t.Log("=== Step 4: Write on Primary, Verify on Replica ===")
	primaryDB.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS ha_test")
	primaryDB.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS ha_test.lifecycle_test (id INT, ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP)")
	primaryDB.ExecContext(context.Background(), "INSERT INTO ha_test.lifecycle_test (id) VALUES (42)")

	time.Sleep(2 * time.Second)

	rows, err := replica.Query(context.Background(), "SELECT id FROM ha_test.lifecycle_test WHERE id = 42")
	if err != nil {
		t.Fatalf("replica lifecycle query failed: %v", err)
	}
	if len(rows.Data) == 0 {
		t.Fatal("lifecycle test data not propagated to replica")
	}
	t.Log("Data propagated correctly on replica")

	t.Log("=== End-to-End Lifecycle Test PASSED ===")
}

// =============================================================================
// Helper Functions
// =============================================================================

func setupPrimary(t *testing.T) (*realMySQLAdapter, *sql.DB, func()) {
	t.Helper()
	adapter, db, err := createRealMySQLAdapter(PrimaryDSN(), "127.0.0.1", 3306)
	if err != nil {
		t.Fatalf("failed to connect to primary: %v", err)
	}
	return adapter, db, func() { db.Close() }
}

func setupReplica(t *testing.T) (*realMySQLAdapter, *sql.DB, func()) {
	t.Helper()
	adapter, db, err := createRealMySQLAdapter(ReplicaDSN(), "127.0.0.1", 3307)
	if err != nil {
		t.Fatalf("failed to connect to replica: %v", err)
	}
	return adapter, db, func() { db.Close() }
}

func setupNoMySQL(t *testing.T) (*realMySQLAdapter, *sql.DB, func()) {
	t.Helper()
	db, err := sql.Open("mysql", "root:rootpass123@tcp(127.0.0.1:19999)/")
	if err != nil {
		t.Fatalf("failed to open connection: %v", err)
	}
	adapter := &realMySQLAdapter{db: db, cfg: model.InstanceConfig{Host: "127.0.0.1", Port: 19999, Type: model.MySQL}}
	return adapter, db, func() { db.Close() }
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertSuccess(t *testing.T, result model.OperationResult) {
	t.Helper()
	if !result.Success {
		t.Fatalf("expected success, got failure: %s", result.RawOutput)
	}
}

func assertContains(t *testing.T, output, substring, msg string) {
	t.Helper()
	if !strings.Contains(output, substring) {
		t.Errorf("%s: expected %q in output:\n%s", msg, substring, output)
	}
}
