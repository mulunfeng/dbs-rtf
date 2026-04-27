package config

import (
	"os"
	"regexp"

	"github.com/dbs-rtf/agent/pkg/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Audit    AuditConfig    `yaml:"audit"`
	Security SecurityConfig `yaml:"security"`
	NLP      NLPConfig      `yaml:"nlp"`
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

var envRegex = regexp.MustCompile(`\$\{(\w+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	resolved := envRegex.ReplaceAllStringFunc(string(data), func(match string) string {
		envVar := match[2 : len(match)-1]
		if val := os.Getenv(envVar); val != "" {
			return val
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

	return &cfg, nil
}
