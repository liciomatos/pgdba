package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liciomatos/pgdba-cli/util"
	"github.com/mark3labs/mcp-go/mcp"
)

// responseMeta identifies which server and database produced a response and since
// when its cumulative counters accumulate, so counters and recommendations can be read
// in context. Fields after server_version come from FetchServerMeta and are omitted
// when it fails (the failure is reported in warnings).
type responseMeta struct {
	Target                  string                        `json:"target"` // "primary" or a --replica name
	Host                    string                        `json:"host"`
	Database                string                        `json:"database"`
	ServerVersion           string                        `json:"server_version"`
	Role                    string                        `json:"role,omitempty"` // primary or replica
	InRecovery              *bool                         `json:"in_recovery,omitempty"`
	PostmasterStartTime     string                        `json:"postmaster_start_time,omitempty"`
	UptimeSeconds           *int64                        `json:"uptime_seconds,omitempty"`
	StatsReset              map[string]statsResetResponse `json:"stats_reset,omitempty"`
	PgStatStatementsVersion *string                       `json:"pg_stat_statements_version,omitempty"`
}

// statsResetResponse is when one group of cumulative counters was last reset.
type statsResetResponse struct {
	ResetAt           *string `json:"reset_at"`
	SecondsSinceReset *int64  `json:"seconds_since_reset"`
	Note              string  `json:"note,omitempty"`
}

// neverResetNote explains a null stats_reset, which otherwise reads like missing data.
const neverResetNote = "never reset: counters accumulate since the cluster was created or its last crash recovery"

func toStatsReset(reset *time.Time, serverTime time.Time) statsResetResponse {
	if reset == nil {
		return statsResetResponse{Note: neverResetNote}
	}
	formatted := reset.Format(time.RFC3339)
	seconds := int64(serverTime.Sub(*reset).Seconds())
	return statsResetResponse{ResetAt: &formatted, SecondsSinceReset: &seconds}
}

// envelope is the shape of every tool response: the same meta block, the parts that
// failed without aborting the call, and the tool-specific result.
type envelope struct {
	Meta     responseMeta `json:"meta"`
	Warnings []string     `json:"warnings"`
	Result   any          `json:"result"`
}

// buildMeta returns the meta block and, when the server metadata can't be read, a
// warning saying so (the identity fields are always present).
func buildMeta(ctx context.Context) (responseMeta, string) {
	target := targetFromContext(ctx)
	meta := responseMeta{
		Target:        target.Name,
		Host:          target.Host,
		Database:      target.DBName,
		ServerVersion: target.Version,
	}
	if target.DB == nil {
		return meta, ""
	}
	server, err := util.FetchServerMeta(ctx, target.DB)
	if err != nil {
		return meta, "server metadata: " + err.Error()
	}
	meta.ServerVersion = server.ServerVersion
	meta.Role = "primary"
	if server.InRecovery {
		meta.Role = "replica"
	}
	meta.InRecovery = &server.InRecovery
	meta.PostmasterStartTime = server.PostmasterStartTime.Format(time.RFC3339)
	meta.UptimeSeconds = &server.UptimeSeconds
	meta.StatsReset = map[string]statsResetResponse{
		"database": toStatsReset(server.DatabaseStatsReset, server.ServerTime),
		"bgwriter": toStatsReset(server.BgwriterStatsReset, server.ServerTime),
	}
	// pg_stat_checkpointer has its own reset from PG17 on.
	if server.CheckpointerStatsReset != nil {
		meta.StatsReset["checkpointer"] = toStatsReset(server.CheckpointerStatsReset, server.ServerTime)
	}
	if server.PgStatStatementsVersion != "" {
		meta.PgStatStatementsVersion = &server.PgStatStatementsVersion
	}
	return meta, ""
}

// respond wraps result in the standard envelope and serializes it as the tool result.
// Empty warnings are dropped; the list is always present (never null) in the JSON.
func respond(ctx context.Context, result any, warnings ...string) (*mcp.CallToolResult, error) {
	meta, metaWarning := buildMeta(ctx)
	nonEmpty := make([]string, 0, len(warnings)+1)
	for _, warning := range append(warnings, metaWarning) {
		if warning != "" {
			nonEmpty = append(nonEmpty, warning)
		}
	}
	data, err := json.Marshal(envelope{Meta: meta, Warnings: nonEmpty, Result: result})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
