package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func withReplicas(t *testing.T, names ...string) {
	t.Helper()
	previous := config.Config.Replicas
	config.Config.Replicas = nil
	for _, name := range names {
		config.Config.Replicas = append(config.Config.Replicas, &config.Target{Name: name, Host: name + ".internal", DBName: "app"})
	}
	t.Cleanup(func() { config.Config.Replicas = previous })
}

func callWithTarget(t *testing.T, target string) (*config.Target, *mcp.CallToolResult) {
	t.Helper()
	var seen *config.Target
	handler := withTarget(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen = targetFromContext(ctx)
		return mcp.NewToolResultText("ok"), nil
	})
	req := mcp.CallToolRequest{}
	if target != "" {
		req.Params.Arguments = map[string]any{"target": target}
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return seen, result
}

func TestWithTarget_RoutesToTheNamedServer(t *testing.T) {
	withReplicas(t, "replica1")
	config.Config.Host = "primary.internal"

	if seen, _ := callWithTarget(t, ""); seen == nil || seen.Name != primaryTargetName || seen.Host != "primary.internal" {
		t.Errorf("no target should mean the primary, got %+v", seen)
	}
	if seen, _ := callWithTarget(t, "replica1"); seen == nil || seen.Host != "replica1.internal" {
		t.Errorf("target=replica1 should route to the replica, got %+v", seen)
	}
	seen, result := callWithTarget(t, "nope")
	if seen != nil || !result.IsError {
		t.Fatalf("an unknown target must fail before the handler runs")
	}
	if text := result.Content[0].(mcp.TextContent).Text; !strings.Contains(text, "primary, replica1") {
		t.Errorf("the error should list the available targets, got %q", text)
	}
}

func TestAddTool_AdvertisesTargets(t *testing.T) {
	withReplicas(t, "replica1", "replica2")
	s := server.NewMCPServer("test", "0")
	addTool(s, mcp.NewTool("check_something"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	tool := s.GetTool("check_something")
	if tool == nil {
		t.Fatal("tool not registered")
	}
	property, ok := tool.Tool.InputSchema.Properties["target"].(map[string]any)
	if !ok {
		t.Fatalf("target parameter missing: %+v", tool.Tool.InputSchema.Properties)
	}
	enum, _ := property["enum"].([]string)
	if strings.Join(enum, ",") != "primary,replica1,replica2" {
		t.Errorf("target enum = %v", enum)
	}
}
