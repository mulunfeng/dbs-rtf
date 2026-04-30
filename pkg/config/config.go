package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/dbs-rtf/agent/pkg/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Audit    AuditConfig    `yaml:"audit"`
	Security SecurityConfig `yaml:"security"`
	NLP      NLPConfig      `yaml:"nlp"`
	HA       HAConfig       `yaml:"ha"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Instances []model.InstanceConfig `yaml:"instances"`
}

type AuditConfig struct {
	Mode string `yaml:"mode"`
	Path string `yaml:"path"`
}

type SecurityConfig struct {
	DefaultTimeout          string `yaml:"default_timeout"`
	DangerousOpsConfirm     bool   `yaml:"dangerous_ops_confirm"`
	MaxConcurrentOpsPerInst int    `yaml:"max_concurrent_ops_per_instance"`
}

type NLPConfig struct {
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
}

type HAConfig struct {
	Enabled      bool             `yaml:"enabled"`
	Monitor      MonitorConfig    `yaml:"monitor"`
	Failover     FailoverConfig   `yaml:"failover"`
	Notification NotificationConfig `yaml:"notification"`
}

type MonitorConfig struct {
	Interval            Duration `yaml:"interval"`
	PingTimeout         Duration `yaml:"ping_timeout"`
	ConsecutiveFailures int      `yaml:"consecutive_failures"`
}

type FailoverConfig struct {
	Cooldown          Duration `yaml:"cooldown"`
	DryRun            bool     `yaml:"dry_run"`
	NotifyBefore      bool     `yaml:"notify_before"`
	ProxySwitchURL    string   `yaml:"proxy_switch_url"`
	MasterHostnameMap map[string]string `yaml:"master_hostname_map"`
}

type NotificationConfig struct {
	WebhookURL string `yaml:"webhook_url"`
	LogOnly    bool   `yaml:"log_only"`
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

var envRegex = regexp.MustCompile(`\$\{(\w+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	resolved := envRegex.ReplaceAllStringFunc(string(data), func(match string) string {
		envVar := match[2 : len(match)-1]
		if val := os.Getenv(envVar); val != "" {
			return fmt.Sprintf("%q", val)
		}
		return match
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return nil, err
	}

	if cfg.Security.DefaultTimeout == "" {
		cfg.Security.DefaultTimeout = "30s"
	}
	if cfg.Security.MaxConcurrentOpsPerInst == 0 {
		cfg.Security.MaxConcurrentOpsPerInst = 1
	}
	if cfg.NLP.Model == "" {
		cfg.NLP.Model = "claude-sonnet-4-6"
	}
	if cfg.HA.Monitor.Interval.Duration == 0 {
		cfg.HA.Monitor.Interval.Duration = 5 * time.Second
	}
	if cfg.HA.Monitor.PingTimeout.Duration == 0 {
		cfg.HA.Monitor.PingTimeout.Duration = 3 * time.Second
	}
	if cfg.HA.Monitor.ConsecutiveFailures == 0 {
		cfg.HA.Monitor.ConsecutiveFailures = 3
	}
	if cfg.HA.Failover.Cooldown.Duration == 0 {
		cfg.HA.Failover.Cooldown.Duration = 60 * time.Second
	}

	return &cfg, cfg.Validate()
}

func (c *Config) Validate() error {
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	return nil
}
