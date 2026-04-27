package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	cfgContent := `
server:
  host: "127.0.0.1"
  port: 9090
database:
  instances: []
audit:
  mode: "file"
  path: "/tmp/audit/"
security:
  default_timeout: "60s"
  dangerous_ops_confirm: false
  max_concurrent_ops_per_instance: 2
nlp:
  provider: "claude"
  model: "test-model"
`
	tmp := filepath.Join(t.TempDir(), "test.yaml")
	os.WriteFile(tmp, []byte(cfgContent), 0644)

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Security.DangerousOpsConfirm != false {
		t.Error("expected dangerous_ops_confirm false")
	}
}

func TestEnvVarExpansion(t *testing.T) {
	os.Setenv("TEST_DB_PASS", "secret123")
	defer os.Unsetenv("TEST_DB_PASS")

	cfgContent := `
server:
  host: "localhost"
  port: 8080
database:
  instances:
    - name: "test"
      host: "localhost"
      port: 3306
      type: "mysql"
      user: "root"
      password: "${TEST_DB_PASS}"
audit:
  mode: "file"
security:
nlp:
`
	tmp := filepath.Join(t.TempDir(), "envtest.yaml")
	os.WriteFile(tmp, []byte(cfgContent), 0644)

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Instances[0].Password != "secret123" {
		t.Errorf("expected password secret123, got %s", cfg.Database.Instances[0].Password)
	}
}

func TestDefaults(t *testing.T) {
	cfgContent := `
server:
  host: "localhost"
  port: 8080
database:
  instances: []
audit:
  mode: "file"
security:
nlp:
`
	tmp := filepath.Join(t.TempDir(), "defaults.yaml")
	os.WriteFile(tmp, []byte(cfgContent), 0644)

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Security.DefaultTimeout != "30s" {
		t.Errorf("expected default timeout 30s, got %s", cfg.Security.DefaultTimeout)
	}
	if cfg.Security.MaxConcurrentOpsPerInst != 1 {
		t.Errorf("expected default max concurrent 1, got %d", cfg.Security.MaxConcurrentOpsPerInst)
	}
}
