package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dbs-rtf/agent/internal/engine"
	"github.com/dbs-rtf/agent/internal/interface/command"
	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"

	// Import plugins to trigger init() registration
	_ "github.com/dbs-rtf/agent/internal/adapters/mysql"
	_ "github.com/dbs-rtf/agent/internal/operations/ha_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/session_mgmt"
	_ "github.com/dbs-rtf/agent/internal/operations/sql_diag"
)

func main() {
	router := command.NewRouter()
	router.RegisterListPluginsCmd()

	orchestrator := engine.NewOrchestrator(
		&engine.Auth{},
		nil,
		engine.NewRateLimiter(),
	)

	router.RegisterExecCmd(func(cmd *model.Command) error {
		user := engine.User{Name: "cli-user", Role: operations.RoleAdmin}
		ctx := context.Background()
		result, err := orchestrator.Execute(ctx, *cmd, user, "cli")
		if err != nil {
			return err
		}
		if result.RawOutput != "" {
			fmt.Println(result.RawOutput)
		}
		if !result.Success {
			os.Exit(1)
		}
		return nil
	})

	if err := router.RootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
