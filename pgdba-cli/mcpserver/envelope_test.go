package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestRespond_WrapsResultInEnvelope(t *testing.T) {
	config.Config.Host, config.Config.DBName, config.Config.Version = "db1", "app", "16.4"
	result, err := respond(context.Background(), []int{1, 2}, "", "something failed")
	if err != nil || result.IsError {
		t.Fatalf("respond failed: %v %+v", err, result)
	}
	var decoded struct {
		Meta     responseMeta `json:"meta"`
		Warnings []string     `json:"warnings"`
		Result   []int        `json:"result"`
	}
	text := result.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("invalid JSON %q: %v", text, err)
	}
	if meta := decoded.Meta; meta.Host != "db1" || meta.Database != "app" || meta.ServerVersion != "16.4" {
		t.Errorf("unexpected meta %+v", decoded.Meta)
	}
	if len(decoded.Warnings) != 1 || decoded.Warnings[0] != "something failed" {
		t.Errorf("empty warnings should be dropped, got %v", decoded.Warnings)
	}
	if len(decoded.Result) != 2 {
		t.Errorf("unexpected result %v", decoded.Result)
	}
}

func TestRespond_WarningsNeverNull(t *testing.T) {
	result, _ := respond(context.Background(), map[string]int{})
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["warnings"]) != "[]" {
		t.Errorf("warnings should be an empty list, got %s", decoded["warnings"])
	}
}
