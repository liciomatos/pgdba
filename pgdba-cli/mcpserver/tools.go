package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/liciomatos/pgdba-cli/util"
	"github.com/mark3labs/mcp-go/mcp"
)

// jsonResult serializes v as a JSON tool result.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// intParam reads an integer parameter from the request with a default fallback.
func intParam(req mcp.CallToolRequest, key string, def int) int {
	return int(req.GetFloat(key, float64(def)))
}

// strParam reads a string parameter from the request with a default fallback.
func strParam(req mcp.CallToolRequest, key, def string) string {
	return req.GetString(key, def)
}

// formatTime converts a nullable *time.Time to a string for JSON output.
func formatTime(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Format(time.RFC3339)
}

// --- dashboard ---

type dashboardResponse struct {
	Host             string   `json:"host"`
	Database         string   `json:"database"`
	UsedConnections  int      `json:"used_connections"`
	MaxConnections   int      `json:"max_connections"`
	ConnectionPct    float64  `json:"connection_pct"`
	ActiveQueries    int      `json:"active_queries"`
	BlockedQueries   int      `json:"blocked_queries"`
	SlowQueryCount   int      `json:"slow_query_count"`
	SlowThresholdMS  int      `json:"slow_threshold_ms"`
	CacheHitRatio    *float64 `json:"cache_hit_ratio"`
	DeadTuples       int64    `json:"dead_tuples"`
	InvalidIndexes   int      `json:"invalid_indexes"`
	ReplicationSlots int      `json:"replication_slots"`
}

func handleCheckDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	threshold := config.Config.SlowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}
	data, err := util.FetchDashboard(ctx, config.Config.DB, threshold)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(dashboardResponse{
		Host:             data.Host,
		Database:         data.Database,
		UsedConnections:  data.UsedConnections,
		MaxConnections:   data.MaxConnections,
		ConnectionPct:    data.ConnectionPct,
		ActiveQueries:    data.ActiveQueries,
		BlockedQueries:   data.BlockedQueries,
		SlowQueryCount:   data.SlowQueryCount,
		SlowThresholdMS:  data.SlowThresholdMS,
		CacheHitRatio:    data.CacheHitRatio,
		DeadTuples:       data.DeadTuples,
		InvalidIndexes:   data.InvalidIndexes,
		ReplicationSlots: data.ReplicationSlots,
	})
}

// --- slow queries ---

type slowQueryResponse struct {
	QueryID          int64   `json:"query_id"`
	Query            string  `json:"query"`
	Calls            int     `json:"calls"`
	TotalExecTimeMS  float64 `json:"total_exec_time_ms"`
	MeanExecTimeMS   float64 `json:"mean_exec_time_ms"`
	StddevExecTimeMS float64 `json:"stddev_exec_time_ms"`
	Rows             int     `json:"rows"`
}

func handleCheckSlowQueries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	threshold := int(req.GetFloat("threshold_ms", 1000))
	limit := intParam(req, "limit", 20)
	queries, err := util.FetchSlowQueries(ctx, config.Config.DB, threshold, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]slowQueryResponse, 0, len(queries))
	for _, q := range queries {
		resp = append(resp, slowQueryResponse{
			QueryID:          q.QueryID,
			Query:            q.Query,
			Calls:            q.Calls,
			TotalExecTimeMS:  q.TotalExecTimeMS,
			MeanExecTimeMS:   q.MeanExecTimeMS,
			StddevExecTimeMS: q.StddevExecTimeMS,
			Rows:             q.Rows,
		})
	}
	return jsonResult(resp)
}

// --- long running queries ---

type longRunningQueryResponse struct {
	PID             int     `json:"pid"`
	Username        string  `json:"username"`
	ApplicationName string  `json:"application_name"`
	State           string  `json:"state"`
	DurationSeconds float64 `json:"duration_seconds"`
	Query           string  `json:"query"`
}

func handleCheckLongRunningQueries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	minDuration := intParam(req, "min_duration_seconds", 5)
	limit := intParam(req, "limit", 20)
	queries, err := util.FetchLongRunningQueries(ctx, config.Config.DB, minDuration, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]longRunningQueryResponse, 0, len(queries))
	for _, q := range queries {
		resp = append(resp, longRunningQueryResponse{
			PID:             q.PID,
			Username:        q.Username,
			ApplicationName: q.ApplicationName,
			State:           q.State,
			DurationSeconds: q.DurationSeconds,
			Query:           q.Query,
		})
	}
	return jsonResult(resp)
}

// --- blocked queries ---

type blockedQueryResponse struct {
	BlockedPID          int    `json:"blocked_pid"`
	BlockedUser         string `json:"blocked_user"`
	BlockingPID         int    `json:"blocking_pid"`
	BlockingUser        string `json:"blocking_user"`
	BlockedStatement    string `json:"blocked_statement"`
	BlockingStatement   string `json:"blocking_statement"`
	BlockedApplication  string `json:"blocked_application"`
	BlockingApplication string `json:"blocking_application"`
}

func handleCheckBlockedQueries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	blocked, err := util.FetchBlockedQueries(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]blockedQueryResponse, 0, len(blocked))
	for _, b := range blocked {
		resp = append(resp, blockedQueryResponse{
			BlockedPID:          b.BlockedPID,
			BlockedUser:         b.BlockedUser,
			BlockingPID:         b.BlockingPID,
			BlockingUser:        b.BlockingUser,
			BlockedStatement:    b.BlockedStatement,
			BlockingStatement:   b.BlockingStatement,
			BlockedApplication:  b.BlockedApplication,
			BlockingApplication: b.BlockingApplication,
		})
	}
	return jsonResult(resp)
}

// --- connections ---

type connectionStateResponse struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type connectionsResponse struct {
	States     []connectionStateResponse `json:"states"`
	TotalUsed  int                       `json:"total_used"`
	MaxAllowed int                       `json:"max_allowed"`
	UsagePct   float64                   `json:"usage_pct"`
}

func handleCheckConnections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := util.FetchConnections(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	states := make([]connectionStateResponse, 0, len(result.States))
	for _, s := range result.States {
		states = append(states, connectionStateResponse{State: s.State, Count: s.Count})
	}
	return jsonResult(connectionsResponse{
		States:     states,
		TotalUsed:  result.TotalUsed,
		MaxAllowed: result.MaxAllowed,
		UsagePct:   result.UsagePct,
	})
}

// --- autovacuum ---

type autovacuumTableResponse struct {
	SchemaName      string   `json:"schema_name"`
	TableName       string   `json:"table_name"`
	DeadTuples      int64    `json:"dead_tuples"`
	LiveTuples      int64    `json:"live_tuples"`
	DeadPct         *float64 `json:"dead_pct"`
	LastVacuum      string   `json:"last_vacuum"`
	LastAnalyze     string   `json:"last_analyze"`
	LastAutovacuum  string   `json:"last_autovacuum"`
	LastAutoanalyze string   `json:"last_autoanalyze"`
	AutovacuumCount int64    `json:"autovacuum_count"`
}

func handleCheckAutovacuum(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := intParam(req, "limit", 20)
	tables, err := util.FetchAutovacuum(ctx, config.Config.DB, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]autovacuumTableResponse, 0, len(tables))
	for _, t := range tables {
		resp = append(resp, autovacuumTableResponse{
			SchemaName:      t.SchemaName,
			TableName:       t.TableName,
			DeadTuples:      t.DeadTuples,
			LiveTuples:      t.LiveTuples,
			DeadPct:         t.DeadPct,
			LastVacuum:      formatTime(t.LastVacuum),
			LastAnalyze:     formatTime(t.LastAnalyze),
			LastAutovacuum:  formatTime(t.LastAutovacuum),
			LastAutoanalyze: formatTime(t.LastAutoanalyze),
			AutovacuumCount: t.AutovacuumCount,
		})
	}
	return jsonResult(resp)
}

// --- index usage ---

type indexUsageResponse struct {
	SchemaName   string `json:"schema_name"`
	TableName    string `json:"table_name"`
	IndexName    string `json:"index_name"`
	IndexColumns string `json:"index_columns"`
	IsValid      bool   `json:"is_valid"`
	IdxScan      int64  `json:"idx_scan"`
	IdxTupRead   int64  `json:"idx_tup_read"`
	IdxTupFetch  int64  `json:"idx_tup_fetch"`
	IndexSize    string `json:"index_size"`
}

func handleCheckIndexUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := intParam(req, "limit", 50)
	indexes, err := util.FetchIndexUsage(ctx, config.Config.DB, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]indexUsageResponse, 0, len(indexes))
	for _, idx := range indexes {
		resp = append(resp, indexUsageResponse{
			SchemaName:   idx.SchemaName,
			TableName:    idx.TableName,
			IndexName:    idx.IndexName,
			IndexColumns: idx.IndexColumns,
			IsValid:      idx.IsValid,
			IdxScan:      idx.IdxScan,
			IdxTupRead:   idx.IdxTupRead,
			IdxTupFetch:  idx.IdxTupFetch,
			IndexSize:    idx.IndexSize,
		})
	}
	return jsonResult(resp)
}

// --- cache hit ---

type cacheHitTableResponse struct {
	TableName        string  `json:"table_name"`
	HeapBlksRead     int64   `json:"heap_blks_read"`
	HeapBlksHit      int64   `json:"heap_blks_hit"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"`
	IdxBlksRead      int64   `json:"idx_blks_read"`
	IdxBlksHit       int64   `json:"idx_blks_hit"`
	IdxCacheHitRatio float64 `json:"idx_cache_hit_ratio"`
}

func handleCheckCacheHit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := intParam(req, "limit", 50)
	tables, err := util.FetchCacheHit(ctx, config.Config.DB, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]cacheHitTableResponse, 0, len(tables))
	for _, t := range tables {
		resp = append(resp, cacheHitTableResponse{
			TableName:        t.TableName,
			HeapBlksRead:     t.HeapBlksRead,
			HeapBlksHit:      t.HeapBlksHit,
			CacheHitRatio:    t.CacheHitRatio,
			IdxBlksRead:      t.IdxBlksRead,
			IdxBlksHit:       t.IdxBlksHit,
			IdxCacheHitRatio: t.IdxCacheHitRatio,
		})
	}
	return jsonResult(resp)
}

// --- wait events ---

type waitEventResponse struct {
	EventType string  `json:"event_type"`
	Event     string  `json:"event"`
	Count     int     `json:"count"`
	Pct       float64 `json:"pct"`
}

func handleCheckWaitEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	events, err := util.FetchWaitEvents(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]waitEventResponse, 0, len(events))
	for _, e := range events {
		resp = append(resp, waitEventResponse{
			EventType: e.EventType,
			Event:     e.Event,
			Count:     e.Count,
			Pct:       e.Pct,
		})
	}
	return jsonResult(resp)
}

// --- query load ---

type queryLoadResponse struct {
	QueryID  string  `json:"query_id"`
	Query    string  `json:"query"`
	Calls    int     `json:"calls"`
	TotalMS  float64 `json:"total_ms"`
	MeanMS   float64 `json:"mean_ms"`
	BufferMB float64 `json:"buffer_mb"`
	TempMB   float64 `json:"temp_mb"`
	LoadPct  float64 `json:"load_pct"`
}

func handleCheckQueryLoad(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := intParam(req, "limit", 20)
	queries, err := util.FetchQueryLoad(ctx, config.Config.DB, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]queryLoadResponse, 0, len(queries))
	for _, q := range queries {
		resp = append(resp, queryLoadResponse{
			QueryID:  q.QueryID,
			Query:    q.Query,
			Calls:    q.Calls,
			TotalMS:  q.TotalMS,
			MeanMS:   q.MeanMS,
			BufferMB: q.BufferMB,
			TempMB:   q.TempMB,
			LoadPct:  q.LoadPct,
		})
	}
	return jsonResult(resp)
}

// --- replication slots ---

type replicationSlotResponse struct {
	SlotName  string  `json:"slot_name"`
	SlotType  string  `json:"slot_type"`
	Database  *string `json:"database"`
	Active    bool    `json:"active"`
	ActivePID *int    `json:"active_pid"`
	WALLag    string  `json:"wal_lag"`
}

func handleCheckReplicationSlots(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slots, err := util.FetchReplicationSlots(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]replicationSlotResponse, 0, len(slots))
	for _, s := range slots {
		resp = append(resp, replicationSlotResponse{
			SlotName:  s.SlotName,
			SlotType:  s.SlotType,
			Database:  s.Database,
			Active:    s.Active,
			ActivePID: s.ActivePID,
			WALLag:    s.WALLag,
		})
	}
	return jsonResult(resp)
}

// --- users ---

type userResponse struct {
	RoleName    string `json:"role_name"`
	Superuser   bool   `json:"superuser"`
	CreateDB    bool   `json:"create_db"`
	CreateRole  bool   `json:"create_role"`
	Replication bool   `json:"replication"`
	ConnLimit   int    `json:"conn_limit"`
	ValidUntil  string `json:"valid_until"`
	MemberOf    string `json:"member_of"`
}

func handleCheckUsers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	users, err := util.FetchUsers(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]userResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, userResponse{
			RoleName:    u.RoleName,
			Superuser:   u.Superuser,
			CreateDB:    u.CreateDB,
			CreateRole:  u.CreateRole,
			Replication: u.Replication,
			ConnLimit:   u.ConnLimit,
			ValidUntil:  formatTime(u.ValidUntil),
			MemberOf:    u.MemberOf,
		})
	}
	return jsonResult(resp)
}

// --- roles ---

type roleResponse struct {
	RoleName   string `json:"role_name"`
	Superuser  bool   `json:"superuser"`
	Inherit    bool   `json:"inherit"`
	CreateRole bool   `json:"create_role"`
	CreateDB   bool   `json:"create_db"`
	Members    string `json:"members"`
}

func handleCheckRoles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	roles, err := util.FetchRoles(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]roleResponse, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, roleResponse{
			RoleName:   r.RoleName,
			Superuser:  r.Superuser,
			Inherit:    r.Inherit,
			CreateRole: r.CreateRole,
			CreateDB:   r.CreateDB,
			Members:    r.Members,
		})
	}
	return jsonResult(resp)
}

// --- extensions ---

type extensionResponse struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Schema      string `json:"schema"`
	Description string `json:"description"`
}

func handleCheckExtensions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	exts, err := util.FetchExtensions(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]extensionResponse, 0, len(exts))
	for _, e := range exts {
		resp = append(resp, extensionResponse{
			Name:        e.Name,
			Version:     e.Version,
			Schema:      e.Schema,
			Description: e.Description,
		})
	}
	return jsonResult(resp)
}

// --- pg_config ---

type pgSettingResponse struct {
	Name        string `json:"name"`
	Setting     string `json:"setting"`
	Unit        string `json:"unit"`
	Category    string `json:"category"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

func handleCheckPgConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filter := strParam(req, "filter", "")
	settings, err := util.FetchPgConfig(ctx, config.Config.DB, filter)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]pgSettingResponse, 0, len(settings))
	for _, s := range settings {
		resp = append(resp, pgSettingResponse{
			Name:        s.Name,
			Setting:     s.Setting,
			Unit:        s.Unit,
			Category:    s.Category,
			Source:      s.Source,
			Description: s.Description,
		})
	}
	return jsonResult(resp)
}

// --- schema ---

type schemaColumnResponse struct {
	ColumnName    string `json:"column_name"`
	DataType      string `json:"data_type"`
	MaxLength     string `json:"max_length"`
	IsNullable    string `json:"is_nullable"`
	ColumnDefault string `json:"column_default"`
}

type schemaTableResponse struct {
	SchemaName string                 `json:"schema_name"`
	TableName  string                 `json:"table_name"`
	SizePretty string                 `json:"size"`
	EstRows    int64                  `json:"est_rows"`
	Columns    []schemaColumnResponse `json:"columns"`
}

func handleCheckSchema(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	schema := strParam(req, "schema", "public")
	tables, err := util.FetchSchema(ctx, config.Config.DB, schema)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]schemaTableResponse, 0, len(tables))
	for _, t := range tables {
		cols := make([]schemaColumnResponse, 0, len(t.Columns))
		for _, c := range t.Columns {
			cols = append(cols, schemaColumnResponse{
				ColumnName:    c.ColumnName,
				DataType:      c.DataType,
				MaxLength:     c.MaxLength,
				IsNullable:    c.IsNullable,
				ColumnDefault: c.ColumnDefault,
			})
		}
		resp = append(resp, schemaTableResponse{
			SchemaName: t.SchemaName,
			TableName:  t.TableName,
			SizePretty: t.SizePretty,
			EstRows:    t.EstRows,
			Columns:    cols,
		})
	}
	return jsonResult(resp)
}
