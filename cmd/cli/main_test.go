package main

import (
	"bytes"
	"testing"

	"github.com/dbs-rtf/agent/internal/interface/command"
)

func TestListPluginsCommand(t *testing.T) {
	router := command.NewRouter()
	router.RegisterListPluginsCmd()

	buf := new(bytes.Buffer)
	router.RootCmd().SetOut(buf)
	router.RootCmd().SetErr(buf)
	router.RootCmd().SetArgs([]string{"list-plugins"})

	err := router.RootCmd().Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("expected plugin list output")
	}
}
