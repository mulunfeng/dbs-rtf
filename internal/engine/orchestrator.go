package engine

import (
	"context"
	"time"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/errors"
	"github.com/dbs-rtf/agent/pkg/model"
)

type Orchestrator struct {
	auth        *Auth
	audit       *AuditLogger
	rateLimiter *RateLimiter
}

func NewOrchestrator(
	auth *Auth,
	audit *AuditLogger,
	rateLimiter *RateLimiter,
) *Orchestrator {
	return &Orchestrator{
		auth:        auth,
		audit:       audit,
		rateLimiter: rateLimiter,
	}
}

func (o *Orchestrator) Execute(ctx context.Context, cmd model.Command, user User, source string) (model.OperationResult, error) {
	plugin, err := operations.Get(cmd.Plugin)
	if err != nil {
		return model.OperationResult{}, err
	}

	if err := o.auth.CheckPermission(user, plugin, cmd.Operation); err != nil {
		return model.OperationResult{}, err
	}

	instanceKey := cmd.Instance.Address()
	if err := o.rateLimiter.Allow(instanceKey); err != nil {
		return model.OperationResult{}, err
	}
	defer o.rateLimiter.Release(instanceKey)

	adapter, err := adapters.New(cmd.Instance)
	if err != nil {
		return model.OperationResult{}, err
	}

	if err := adapter.Connect(ctx); err != nil {
		return model.OperationResult{}, errors.Wrap(errors.ErrConnectionFailed, "connect failed", err)
	}
	defer adapter.Close()

	req := model.OperationRequest{
		Instance: cmd.Instance,
		Adapter:  adapter,
		Params:   cmd.Params,
	}

	start := time.Now()
	result, err := plugin.Execute(ctx, req)
	result.Duration = time.Since(start)

	if o.audit != nil {
		entry := o.audit.BuildEntry(user.Name, source, cmd, result)
		o.audit.Log(entry)
	}

	return result, err
}
