package main

import (
	"fmt"
	"log"
	"os"

	// Import plugins
	_ "github.com/dbs-rtf/agent/internal/operations/session_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/sql_diag"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/interface/api"
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

	server := api.NewServer(orchestrator)

	addr := ":8080"
	if cfg != nil {
		addr = fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	log.Printf("starting dbs-agent server on %s", addr)
	if err := server.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
