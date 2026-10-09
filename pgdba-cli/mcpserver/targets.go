package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// primaryTargetName addresses the main connection (the --url / --host flags).
const primaryTargetName = "primary"

// targetContextKey carries the request's resolved *config.Target through ctx, so
// handlers and the meta block use the server the caller asked for.
type targetContextKey struct{}

// primaryTarget describes the main connection as a Target.
func primaryTarget() *config.Target {
	return &config.Target{
		Name:    primaryTargetName,
		Host:    config.Config.Host,
		Port:    config.Config.Port,
		DBName:  config.Config.DBName,
		Version: config.Config.Version,
		DB:      config.Config.DB,
	}
}

// allTargets returns the primary followed by every --replica, in flag order.
func allTargets() []*config.Target {
	return append([]*config.Target{primaryTarget()}, config.Config.Replicas...)
}

func targetNames() []string {
	names := make([]string, 0, len(config.Config.Replicas)+1)
	for _, target := range allTargets() {
		names = append(names, target.Name)
	}
	return names
}

func findTarget(name string) (*config.Target, error) {
	if name == "" {
		name = primaryTargetName
	}
	for _, target := range allTargets() {
		if target.Name == name {
			return target, nil
		}
	}
	return nil, fmt.Errorf("unknown target %q (available: %s)", name, strings.Join(targetNames(), ", "))
}

// targetFromContext returns the target chosen for this request (the primary when the
// handler runs outside withTarget, e.g. in tests).
func targetFromContext(ctx context.Context) *config.Target {
	if target, ok := ctx.Value(targetContextKey{}).(*config.Target); ok {
		return target
	}
	return primaryTarget()
}

// targetDB is the database connection of the request's target.
func targetDB(ctx context.Context) *sql.DB { return targetFromContext(ctx).DB }

// withTarget resolves the optional "target" argument before running the handler.
func withTarget(handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		target, err := findTarget(req.GetString("target", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return handler(context.WithValue(ctx, targetContextKey{}, target), req)
	}
}

// addTool registers a tool with the "target" argument every tool accepts, listing the
// configured targets in its description.
func addTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	if tool.InputSchema.Properties == nil {
		tool.InputSchema.Properties = map[string]any{}
	}
	tool.InputSchema.Properties["target"] = map[string]any{
		"type": "string",
		"description": "Server to query: " + strings.Join(targetNames(), ", ") +
			" (default primary). Statistics such as index usage and cache hit are per instance, so replicas report their own.",
		"enum": targetNames(),
	}
	s.AddTool(tool, withTarget(handler))
}
