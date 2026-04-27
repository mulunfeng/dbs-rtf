package model

import "time"

type VariableScope string

const (
	ScopeGlobal  VariableScope = "global"
	ScopeSession VariableScope = "session"
	ScopeBoth    VariableScope = "both"
)

type Result struct {
	RowsAffected int64
	LastInsertID int64
}

type Row map[string]interface{}

type Rows struct {
	Columns []string
	Data    []Row
}

type BackupOptions struct {
	OutputPath string
	Database   string
	Tables     []string
	Compress   bool
}

type BackupTask struct {
	ID        string
	Status    string
	StartTime time.Time
	EndTime   time.Time
	FilePath  string
	Error     string
}

type ReplicationStatus struct {
	IsReplica     bool
	SourceHost    string
	SourcePort    int
	IOState       string
	SQLState      string
	SecondsBehind int64
	LastError     string
}

type OperationRequest struct {
	Instance InstanceConfig
	Adapter  interface{}
	Params   map[string]string
}

type OperationResult struct {
	Success     bool
	Data        interface{}
	RawOutput   string
	Duration    time.Duration
	Warnings    []string
	RollbackCmd *Command
}

type Command struct {
	Plugin    string
	Operation string
	Params    map[string]string
	Instance  InstanceConfig
	DryRun    bool
	Timeout   time.Duration
}
