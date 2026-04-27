package errors

import "fmt"

type ErrorCode string

const (
	ErrConnectionFailed ErrorCode = "CONNECTION_FAILED"
	ErrPermissionDenied ErrorCode = "PERMISSION_DENIED"
	ErrTimeout          ErrorCode = "OPERATION_TIMEOUT"
	ErrAdapterNotFound  ErrorCode = "ADAPTER_NOT_FOUND"
	ErrPluginNotFound   ErrorCode = "PLUGIN_NOT_FOUND"
	ErrInvalidParam     ErrorCode = "INVALID_PARAMETER"
	ErrDatabaseError    ErrorCode = "DATABASE_ERROR"
	ErrWorkflowFailed   ErrorCode = "WORKFLOW_FAILED"
)

type DBSError struct {
	Code    ErrorCode
	Message string
	Detail  string
	Retry   bool
}

func (e *DBSError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func New(code ErrorCode, msg string) *DBSError {
	return &DBSError{Code: code, Message: msg}
}

func Wrap(code ErrorCode, msg string, detail error) *DBSError {
	d := ""
	if detail != nil {
		d = detail.Error()
	}
	return &DBSError{Code: code, Message: msg, Detail: d}
}

func IsDBSError(err error) bool {
	_, ok := err.(*DBSError)
	return ok
}
