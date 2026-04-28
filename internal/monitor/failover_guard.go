package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

type FailoverGuard struct {
	mu            sync.RWMutex
	lastFailovers map[string]time.Time
	cooldown      time.Duration
}

func NewFailoverGuard(cooldown time.Duration) *FailoverGuard {
	return &FailoverGuard{
		lastFailovers: make(map[string]time.Time),
		cooldown:      cooldown,
	}
}

func (g *FailoverGuard) CheckCooldown(instanceKey string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if lastTime, exists := g.lastFailovers[instanceKey]; exists {
		elapsed := time.Since(lastTime)
		if elapsed < g.cooldown {
			remaining := g.cooldown - elapsed
			return fmt.Errorf("failover cooldown: must wait %v before next failover on %s", remaining, instanceKey)
		}
	}
	return nil
}

func (g *FailoverGuard) RecordFailover(instanceKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastFailovers[instanceKey] = time.Now()
}

func (g *FailoverGuard) VerifyNewMaster(ctx context.Context, cfg model.InstanceConfig) error {
	adapter, err := adapters.New(cfg)
	if err != nil {
		return fmt.Errorf("create adapter for new master: %w", err)
	}

	if err := adapter.Connect(ctx); err != nil {
		return fmt.Errorf("new master %s not reachable: %w", cfg.Address(), err)
	}
	defer adapter.Close()

	if err := adapter.Ping(ctx); err != nil {
		return fmt.Errorf("new master %s ping failed: %w", cfg.Address(), err)
	}

	return nil
}
