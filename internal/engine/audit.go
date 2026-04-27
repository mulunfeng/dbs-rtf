package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dbs-rtf/agent/pkg/model"
)

type AuditEntry struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	User      string            `json:"user"`
	Source    string            `json:"source"`
	Instance  string            `json:"instance"`
	Plugin    string            `json:"plugin"`
	Operation string            `json:"operation"`
	Params    map[string]string `json:"params,omitempty"`
	Result    string            `json:"result"`
	Duration  string            `json:"duration"`
}

type AuditLogger struct {
	mu   sync.Mutex
	path string
}

func NewAuditLogger(basePath string) (*AuditLogger, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create audit log dir: %w", err)
	}
	return &AuditLogger{path: basePath}, nil
}

func (l *AuditLogger) Log(entry AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry.ID == "" {
		entry.ID = fmt.Sprintf("audit-%d", time.Now().UnixNano())
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}

	filename := filepath.Join(l.path, fmt.Sprintf("audit-%s.log", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

func (l *AuditLogger) BuildEntry(user, source string, cmd model.Command, result model.OperationResult) AuditEntry {
	params := make(map[string]string)
	for k, v := range cmd.Params {
		params[k] = v
	}
	if _, ok := params["password"]; ok {
		params["password"] = "***"
	}

	res := "success"
	if !result.Success {
		res = "failed"
	}

	return AuditEntry{
		User:      user,
		Source:    source,
		Instance:  cmd.Instance.Address(),
		Plugin:    cmd.Plugin,
		Operation: cmd.Operation,
		Params:    params,
		Result:    res,
		Duration:  result.Duration.String(),
	}
}
