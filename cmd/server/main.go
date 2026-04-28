package main

import (
	"context"
	"fmt"
	"log"
	"os"

	// Import plugins
	_ "github.com/dbs-rtf/agent/internal/adapters/mysql"
	_ "github.com/dbs-rtf/agent/internal/operations/ha_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/session_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/sql_diag"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/interface/api"
	"github.com/dbs-rtf/agent/internal/monitor"
	"github.com/dbs-rtf/agent/pkg/config"
)

func main() {
	configPath := os.Getenv("DBS_CONFIG")
	if configPath == "" {
		configPath = "configs/dbs-agent.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("warning: could not load config: %v", err)
	}

	audit, err := engine.NewAuditLogger("./logs/audit/")
	if err != nil {
		log.Fatalf("failed to init audit: %v", err)
	}

	auth := &engine.Auth{}
	limiter := engine.NewRateLimiter()
	orchestrator := engine.NewOrchestrator(auth, audit, limiter)

	// Initialize HA monitor
	var haSupervisor *monitor.Supervisor
	if cfg != nil && cfg.HA.Enabled && len(cfg.Database.Instances) > 0 {
		haCfg := monitor.HAConfig{
			Enabled: cfg.HA.Enabled,
			Monitor: monitor.MonitorConfig{
				Interval:            cfg.HA.Monitor.Interval.Duration,
				PingTimeout:         cfg.HA.Monitor.PingTimeout.Duration,
				ConsecutiveFailures: cfg.HA.Monitor.ConsecutiveFailures,
			},
			Failover: monitor.FailoverConfig{
				Cooldown:     cfg.HA.Failover.Cooldown.Duration,
				DryRun:       cfg.HA.Failover.DryRun,
				NotifyBefore: cfg.HA.Failover.NotifyBefore,
			},
			Notification: monitor.NotificationConfig{
				WebhookURL: cfg.HA.Notification.WebhookURL,
				LogOnly:    cfg.HA.Notification.LogOnly,
			},
		}

		var notifier monitor.Notifier
		if haCfg.Notification.LogOnly {
			notifier = monitor.NewLogNotifier()
		} else if haCfg.Notification.WebhookURL != "" {
			notifier = monitor.NewWebhookNotifier(haCfg.Notification.WebhookURL)
		} else {
			notifier = monitor.NewLogNotifier()
		}

		haSupervisor = monitor.NewSupervisor(
			cfg.Database.Instances,
			haCfg,
			notifier,
		)
	}

	server := api.NewServer(orchestrator, haSupervisor)

	addr := ":8080"
	if cfg != nil {
		addr = fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	log.Printf("starting dbs-agent server on %s", addr)

	if haSupervisor != nil {
		haSupervisor.Start(context.Background())
		log.Println("HA monitor started")
	}

	if err := server.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
