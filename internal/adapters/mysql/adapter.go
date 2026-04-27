package mysql

import (
	"context"
	"database/sql"
	"fmt"
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

func (a *MySQLAdapter) Version(ctx context.Context) (string, error) {
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
			row[col] = vals[i]
		}
		result.Data = append(result.Data, row)
	}
	return result, nil
}

func (a *MySQLAdapter) GetVariables(ctx context.Context, pattern string) (map[string]string, error) {
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
		name, _ := row["Variable_name"].(string)
		value, _ := row["Value"].(string)
		result[name] = value
	}
	return result, nil
}

func (a *MySQLAdapter) SetVariable(ctx context.Context, name, value string, scope model.VariableScope) error {
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

func (a *MySQLAdapter) GetProcessList(ctx context.Context) ([]model.ProcessInfo, error) {
	rows, err := a.Query(ctx, "SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.PROCESSLIST")
	if err != nil {
		return nil, err
	}
	var processes []model.ProcessInfo
	for _, row := range rows.Data {
		p := model.ProcessInfo{}
		if v, ok := row["ID"].(int64); ok {
			p.ID = int(v)
		}
		if v, ok := row["USER"].(string); ok {
			p.User = v
		}
		if v, ok := row["HOST"].(string); ok {
			p.Host = v
		}
		if v, ok := row["DB"].(string); ok {
			p.Database = v
		}
		if v, ok := row["COMMAND"].(string); ok {
			p.Command = v
		}
		if v, ok := row["TIME"].(int64); ok {
			p.Time = v
		}
		if v, ok := row["STATE"].(string); ok {
			p.State = v
		}
		if v, ok := row["INFO"].(string); ok {
			p.Info = v
		}
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
	if v, ok := row["Master_Host"].(string); ok {
		status.SourceHost = v
	}
	if v, ok := row["Slave_IO_Running"].(string); ok {
		status.IOState = v
	}
	if v, ok := row["Slave_SQL_Running"].(string); ok {
		status.SQLState = v
	}
	if v, ok := row["Seconds_Behind_Master"].(string); ok {
		fmt.Sscanf(v, "%d", &status.SecondsBehind)
	}
	if v, ok := row["Last_Error"].(string); ok {
		status.LastError = v
	}
	return status, nil
}
