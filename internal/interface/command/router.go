package command

import (
	"fmt"

	"github.com/dbs-rtf/agent/internal/operations"
	"github.com/dbs-rtf/agent/pkg/model"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type Router struct {
	rootCmd *cobra.Command
}

func NewRouter() *Router {
	root := &cobra.Command{
		Use:   "dbs-agent",
		Short: "Database Operations Agent",
	}
	return &Router{rootCmd: root}
}

func (r *Router) RootCmd() *cobra.Command {
	return r.rootCmd
}

func (r *Router) RegisterListPluginsCmd() {
	listCmd := &cobra.Command{
		Use:   "list-plugins",
		Short: "List available operation plugins",
		Run: func(cmd *cobra.Command, args []string) {
			for _, p := range operations.List() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) - %s\n  Operations: %v\n", p.Name(), p.Category(), p.Description(), p.SupportedOps())
			}
		},
	}
	r.rootCmd.AddCommand(listCmd)
}

func (r *Router) RegisterExecCmd(run func(cmd *model.Command) error) {
	execCmd := &cobra.Command{
		Use:   "exec",
		Short: "Execute a database operation",
		RunE: func(c *cobra.Command, args []string) error {
			plugin, _ := c.Flags().GetString("plugin")
			operation, _ := c.Flags().GetString("operation")
			host, _ := c.Flags().GetString("host")
			port, _ := c.Flags().GetInt("port")
			dbType, _ := c.Flags().GetString("db-type")
			dryRun, _ := c.Flags().GetBool("dry-run")

			params := make(map[string]string)
			flags := c.Flags()
			flags.Visit(func(f *pflag.Flag) {
				if f.Name != "plugin" && f.Name != "operation" && f.Name != "host" && f.Name != "port" && f.Name != "db-type" && f.Name != "dry-run" {
					params[f.Name] = f.Value.String()
				}
			})

			modelCmd := &model.Command{
				Plugin:    plugin,
				Operation: operation,
				Instance:  model.InstanceConfig{Host: host, Port: port, Type: model.DBType(dbType)},
				Params:    params,
				DryRun:    dryRun,
			}

			return run(modelCmd)
		},
	}

	execCmd.Flags().StringP("plugin", "p", "", "Plugin name")
	execCmd.Flags().StringP("operation", "o", "", "Operation name")
	execCmd.Flags().StringP("host", "i", "", "Database host")
	execCmd.Flags().IntP("port", "P", 0, "Database port")
	execCmd.Flags().String("db-type", "", "Database type (mysql, postgresql, redis, mongodb)")
	execCmd.Flags().Bool("dry-run", false, "Preview without executing")
	execCmd.MarkFlagRequired("plugin")
	execCmd.MarkFlagRequired("operation")
	execCmd.MarkFlagRequired("host")

	r.rootCmd.AddCommand(execCmd)
}
