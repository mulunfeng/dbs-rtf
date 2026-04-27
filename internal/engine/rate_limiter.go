package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/dbs-rtf/agent/pkg/errors"
)

type RateLimiter struct {
	mu  sync.Mutex
	ops map[string]time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		ops: make(map[string]time.Time),
	}
}

func (r *RateLimiter) Allow(instanceKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.ops[instanceKey]; exists {
		return errors.New(errors.ErrTimeout,
			fmt.Sprintf("operation already in progress on %s", instanceKey))
	}

	r.ops[instanceKey] = time.Now()
	return nil
}

func (r *RateLimiter) Release(instanceKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ops, instanceKey)
}
