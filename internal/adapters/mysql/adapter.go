package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func init() {
	adapters.Register(model.MySQL, NewAdapter)
}

type MySQLAdapter struct {
	cfg model.InstanceConfig
	db  *sql.DB
	mu  sync.RWMutex
}

func NewAdapter(cfg model.InstanceConfig) (adapters.DatabaseAdapter, error) {
	a := &MySQLAdapter{cfg: cfg}
	return a, nil
}

func (a *MySQLAdapter) Type() model.DBType { return model.MySQL }

func (a *MySQLAdapter) Connect(ctx context.Context) error {
	dsn := buildDSN(a.cfg)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("mysql ping: %w", err)
	}
	a.mu.Lock()
	a.db = db
	a.mu.Unlock()
	return nil
}

func (a *MySQLAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *MySQLAdapter) Ping(ctx context.Context) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("not connected")
	}
	return a.db.PingContext(ctx)
}

func (a *MySQLAdapter) ensureConnected() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("not connected")
	}
	return nil
}

func (a *MySQLAdapter) Version(ctx context.Context) (string, error) {
	if err := a.ensureConnected(); err != nil {
		return "", err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	return version, err
}

func (a *MySQLAdapter) Exec(ctx context.Context, sqlStr string, args ...any) (model.Result, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res, err := a.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return model.Result{}, err
	}
	rows, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	return model.Result{RowsAffected: rows, LastInsertID: id}, nil
}

func (a *MySQLAdapter) Query(ctx context.Context, sqlStr string, args ...any) (model.Rows, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
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

func (a *MySQLAdapter) GetVariables(ctx context.Context, pattern string) (map[string]string, error) {
	if err := a.ensureConnected(); err != nil {
		return nil, err
	}
	query := "SHOW VARIABLES"
	if pattern != "" {
		safePattern := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `%`, `\%`, `_`, `\_`).Replace(pattern)
		query += fmt.Sprintf(" LIKE '%s'", safePattern)
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

func (a *MySQLAdapter) SetVariable(ctx context.Context, name, value string, scope model.VariableScope) error {
	if err := a.ensureConnected(); err != nil {
		return err
	}
	// Validate variable name: only allow alphanumeric and underscore
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return fmt.Errorf("invalid variable name: %s", name)
		}
	}
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
	if err != nil {
		return err
	}
	if scope == model.ScopeBoth {
		sqlStr = fmt.Sprintf("SET SESSION %s = ?", name)
		_, err = a.Exec(ctx, sqlStr, value)
	}
	return err
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

func (a *MySQLAdapter) GetProcessList(ctx context.Context) ([]model.ProcessInfo, error) {
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

func (a *MySQLAdapter) KillProcess(ctx context.Context, processID int) error {
	_, err := a.Exec(ctx, fmt.Sprintf("KILL %d", processID))
	return err
}

func (a *MySQLAdapter) StartBackup(ctx context.Context, opts model.BackupOptions) (model.BackupTask, error) {
	return model.BackupTask{}, fmt.Errorf("backup not yet implemented")
}

func (a *MySQLAdapter) GetExecutedGTIDSet(ctx context.Context) (string, error) {
	if err := a.ensureConnected(); err != nil {
		return "", err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var gtidSet string
	err := a.db.QueryRowContext(ctx, "SELECT @@global.gtid_executed").Scan(&gtidSet)
	return gtidSet, err
}

func (a *MySQLAdapter) SetGTIDPurged(ctx context.Context, gtidSet string) error {
	if err := a.ensureConnected(); err != nil {
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("SET GLOBAL gtid_purged='%s'", gtidSet))
	return err
}

func (a *MySQLAdapter) GetReplicationStatus(ctx context.Context) (model.ReplicationStatus, error) {
	rows, err := a.Query(ctx, "SHOW SLAVE STATUS")
	if err != nil {
		return model.ReplicationStatus{}, err
	}
	if len(rows.Data) == 0 {
		return model.ReplicationStatus{}, nil
	}
	row := rows.Data[0]
	status := model.ReplicationStatus{
		IsReplica: true,
	}
	status.SourceHost = asString(row["Master_Host"])
	status.IOState = asString(row["Slave_IO_Running"])
	status.SQLState = asString(row["Slave_SQL_Running"])
	if v := asString(row["Seconds_Behind_Master"]); v != "" {
		fmt.Sscanf(v, "%d", &status.SecondsBehind)
	}
	status.LastError = asString(row["Last_Error"])
	return status, nil
}
