package nlp

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/pkg/model"
)

func TestLoadIntentTemplate(t *testing.T) {
	template, err := LoadIntentTemplate()
	if err != nil {
		t.Fatalf("failed to load template: %v", err)
	}
	if template.SystemPrompt == "" {
		t.Error("system prompt should not be empty")
	}
	if len(template.Examples) == 0 {
		t.Error("template should have examples")
	}
}

func TestBuildPrompt(t *testing.T) {
	template := &IntentTemplate{
		SystemPrompt: "You are a DBA assistant.",
	}
	prompt := BuildPrompt(template, "show me slow queries")
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestIntentParserToCommand(t *testing.T) {
	parser := &IntentParser{}
	intent := &ParsedIntent{
		Plugin:    "session_mgmt",
		Operation: "list",
		Instance:  model.InstanceConfig{Host: "10.0.1.5", Port: 3306},
		Params:    map[string]string{"filter": "long_running"},
	}
	cmd := parser.ToCommand(intent)
	if cmd.Plugin != "session_mgmt" {
		t.Errorf("expected plugin session_mgmt, got %s", cmd.Plugin)
	}
	if cmd.Operation != "list" {
		t.Errorf("expected operation list, got %s", cmd.Operation)
	}
	if cmd.Instance.Host != "10.0.1.5" {
		t.Errorf("expected host 10.0.1.5, got %s", cmd.Instance.Host)
	}
}

func TestParseWithProvider(t *testing.T) {
	parser := &IntentParser{
		template: &IntentTemplate{SystemPrompt: "test"},
	}

	mockResponse := `{
		"plugin": "session_mgmt",
		"operation": "list",
		"instance": {"host": "10.0.1.5", "port": 3306},
		"params": {"filter": "long_running"},
		"missing_fields": []
	}`

	mockLLM := func(ctx context.Context, prompt string) (string, error) {
		return mockResponse, nil
	}

	intent, err := parser.ParseWithProvider(context.Background(), "test", mockLLM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if intent.Plugin != "session_mgmt" {
		t.Errorf("expected session_mgmt, got %s", intent.Plugin)
	}
}
