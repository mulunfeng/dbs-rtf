package mysql

import (
	"context"
	"testing"

	"github.com/dbs-rtf/agent/internal/adapters"
	"github.com/dbs-rtf/agent/pkg/model"
)

func TestMySQLAdapterRegistration(t *testing.T) {
	cfg := model.InstanceConfig{
		Name:     "test",
		Host:     "localhost",
		Port:     3306,
		Type:     model.MySQL,
		User:     "root",
		Password: "",
	}
	adapter, err := adapters.New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.Type() != model.MySQL {
		t.Errorf("expected type mysql, got %s", adapter.Type())
	}
}

func TestMySQLAdapterConnectFail(t *testing.T) {
	cfg := model.InstanceConfig{
		Host:     "127.0.0.1",
		Port:     19999,
		Type:     model.MySQL,
		User:     "root",
		Password: "wrong",
	}
	adapter, _ := adapters.New(cfg)
	err := adapter.Connect(context.Background())
	if err == nil {
		t.Error("expected connection error on unreachable host")
	}
	adapter.Close()
}

func TestMySQLAdapterType(t *testing.T) {
	cfg := model.InstanceConfig{Type: model.MySQL}
	a, _ := NewAdapter(cfg)
	if a.Type() != model.MySQL {
		t.Errorf("expected mysql, got %s", a.Type())
	}
}
