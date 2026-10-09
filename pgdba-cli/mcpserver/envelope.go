package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// responseMeta identifies which server and database produced a response, so counters
// and recommendations can be read in context.
type responseMeta struct {
	Host          string `json:"host"`
	Database      string `json:"database"`
	ServerVersion string `json:"server_version"`
}

// envelope is the shape of every tool response: the same meta block, the parts that
// failed without aborting the call, and the tool-specific result.
type envelope struct {
	Meta     responseMeta `json:"meta"`
	Warnings []string     `json:"warnings"`
	Result   any          `json:"result"`
}

func buildMeta() responseMeta {
	return responseMeta{
		Host:          config.Config.Host,
		Database:      config.Config.DBName,
		ServerVersion: config.Config.Version,
	}
}

// respond wraps result in the standard envelope and serializes it as the tool result.
// Empty warnings are dropped; the list is always present (never null) in the JSON.
func respond(ctx context.Context, result any, warnings ...string) (*mcp.CallToolResult, error) {
	nonEmpty := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning != "" {
			nonEmpty = append(nonEmpty, warning)
		}
	}
	data, err := json.Marshal(envelope{Meta: buildMeta(), Warnings: nonEmpty, Result: result})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
