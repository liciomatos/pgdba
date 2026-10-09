package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/liciomatos/pgdba-cli/util"
	"github.com/mark3labs/mcp-go/mcp"
)

// intParam reads an integer parameter from the request with a default fallback.
func intParam(req mcp.CallToolRequest, key string, def int) int {
	return int(req.GetFloat(key, float64(def)))
}

// strParam reads a string parameter from the request with a default fallback.
func strParam(req mcp.CallToolRequest, key, def string) string {
	return req.GetString(key, def)
}

// maxQueryChars caps query text in responses: statements can be megabytes long and may
// carry literals (pg_stat_activity shows them un-normalized).
const maxQueryChars = 500

// truncateQuery cuts query text to maxQueryChars runes and reports whether it did.
func truncateQuery(query string) (string, bool) {
	runes := []rune(query)
	if len(runes) <= maxQueryChars {
		return query, false
	}
	return string(runes[:maxQueryChars]), true
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
	Host            string  `json:"host"`
	Database        string  `json:"database"`
	UsedConnections int     `json:"used_connections"`
	MaxConnections  int     `json:"max_connections"`
	ConnectionPct   float64 `json:"connection_pct"`
	ActiveQueries   int     `json:"active_queries"`
	BlockedQueries  int     `json:"blocked_queries"`
	// SlowQueryCount is null when pg_stat_statements can't be queried; the reason is
	// in SlowQueryUnavailableReason.
	SlowQueryCount             *int     `json:"slow_query_count"`
	SlowQueryUnavailableReason string   `json:"slow_query_unavailable_reason,omitempty"`
	SlowThresholdMS            int      `json:"slow_threshold_ms"`
	CacheHitRatio              *float64 `json:"cache_hit_ratio"`
	DeadTuples                 int64    `json:"dead_tuples"`
	InvalidIndexes             int      `json:"invalid_indexes"`
	ReplicationSlots           int      `json:"replication_slots"`
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
	return respond(ctx, dashboardResponse{
		Host:                       data.Host,
		Database:                   data.Database,
		UsedConnections:            data.UsedConnections,
		MaxConnections:             data.MaxConnections,
		ConnectionPct:              data.ConnectionPct,
		ActiveQueries:              data.ActiveQueries,
		BlockedQueries:             data.BlockedQueries,
		SlowQueryCount:             data.SlowQueryCount,
		SlowQueryUnavailableReason: data.SlowQueryUnavailableReason,
		SlowThresholdMS:            data.SlowThresholdMS,
		CacheHitRatio:              data.CacheHitRatio,
		DeadTuples:                 data.DeadTuples,
		InvalidIndexes:             data.InvalidIndexes,
		ReplicationSlots:           data.ReplicationSlots,
	}, data.Warnings...)
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

// pgStatStatementsResult wraps rows read from pg_stat_statements with the extension
// version they were read with (column names differ before 1.8).
type pgStatStatementsResult struct {
	PgStatStatementsVersion string `json:"pg_stat_statements_version"`
	Queries                 any    `json:"queries"`
}

// pgStatStatementsVersion returns the installed extension version and, when it is
// older than the server's, the ALTER EXTENSION warning.
func pgStatStatementsVersion(ctx context.Context) (string, string) {
	info, err := util.FetchPgStatStatementsInfo(ctx, config.Config.DB)
	if err != nil {
		return "", "reading pg_stat_statements version: " + err.Error()
	}
	return info.Version, info.UpdateHint()
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
	version, warning := pgStatStatementsVersion(ctx)
	return respond(ctx, pgStatStatementsResult{PgStatStatementsVersion: version, Queries: resp}, warning)
}

// --- long running queries ---

type longRunningQueryResponse struct {
	PID             int     `json:"pid"`
	Username        string  `json:"username"`
	ApplicationName string  `json:"application_name"`
	ClientAddr      string  `json:"client_addr"`
	BackendType     string  `json:"backend_type"`
	State           string  `json:"state"`
	WaitEventType   string  `json:"wait_event_type"`
	WaitEvent       string  `json:"wait_event"`
	DurationSeconds float64 `json:"duration_seconds"`
	Query           string  `json:"query"`
}

func handleCheckLongRunningQueries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	minDuration := intParam(req, "min_duration_seconds", 5)
	limit := intParam(req, "limit", 20)
	includeBackground := req.GetBool("include_background", false)
	queries, err := util.FetchLongRunningQueries(ctx, config.Config.DB, minDuration, limit, includeBackground)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]longRunningQueryResponse, 0, len(queries))
	for _, q := range queries {
		resp = append(resp, longRunningQueryResponse{
			PID:             q.PID,
			Username:        q.Username,
			ApplicationName: q.ApplicationName,
			ClientAddr:      q.ClientAddr,
			BackendType:     q.BackendType,
			State:           q.State,
			WaitEventType:   q.WaitEventType,
			WaitEvent:       q.WaitEvent,
			DurationSeconds: q.DurationSeconds,
			Query:           q.Query,
		})
	}
	return respond(ctx, resp)
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
	return respond(ctx, resp)
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
	return respond(ctx, connectionsResponse{
		States:     states,
		TotalUsed:  result.TotalUsed,
		MaxAllowed: result.MaxAllowed,
		UsagePct:   result.UsagePct,
	})
}

// --- autovacuum ---

type autovacuumCostResponse struct {
	CostDelayMS            float64  `json:"cost_delay_ms"`
	CostDelaySource        string   `json:"cost_delay_source"` // "table" or "global"
	CostLimit              float64  `json:"cost_limit"`
	CostLimitSource        string   `json:"cost_limit_source"`
	ThroughputPerSec       *float64 `json:"throughput_cost_units_per_sec"` // null = unthrottled
	MaxReadMBPerSec        *float64 `json:"max_read_mb_per_sec"`
	GlobalCostDelayMS      float64  `json:"global_cost_delay_ms"`
	GlobalCostLimit        float64  `json:"global_cost_limit"`
	GlobalThroughputPerSec *float64 `json:"global_throughput_cost_units_per_sec"`
	GlobalMaxReadMBPerSec  *float64 `json:"global_max_read_mb_per_sec"`
	SlowerThanGlobal       bool     `json:"slower_than_global"`
	SlowdownFactor         *float64 `json:"slowdown_factor"` // global ÷ table throughput
}

type autovacuumTableResponse struct {
	SchemaName        string                 `json:"schema_name"`
	TableName         string                 `json:"table_name"`
	Status            string                 `json:"status"`
	DeadTuples        int64                  `json:"dead_tuples"`
	LiveTuples        int64                  `json:"live_tuples"`
	DeadPct           *float64               `json:"dead_pct"`
	TriggerThreshold  float64                `json:"trigger_threshold_tuples"`
	TriggerRatio      *float64               `json:"trigger_ratio"` // dead ÷ threshold; ≥ 1 = vacuum due
	ModSinceAnalyze   int64                  `json:"mod_since_analyze"`
	TotalSizeBytes    int64                  `json:"total_size_bytes"`
	TotalSize         string                 `json:"total_size_pretty"`
	AutovacuumEnabled bool                   `json:"autovacuum_enabled"`
	HasOverrides      bool                   `json:"has_overrides"`
	Overrides         map[string]string      `json:"overrides"`
	Cost              autovacuumCostResponse `json:"cost"`
	LastVacuum        string                 `json:"last_vacuum"`
	LastAnalyze       string                 `json:"last_analyze"`
	LastAutovacuum    string                 `json:"last_autovacuum"`
	LastAutoanalyze   string                 `json:"last_autoanalyze"`
	AutovacuumCount   int64                  `json:"autovacuum_count"`
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
			SchemaName:        t.SchemaName,
			TableName:         t.TableName,
			Status:            util.AutovacuumTableStatus(t).String(),
			DeadTuples:        t.DeadTuples,
			LiveTuples:        t.LiveTuples,
			DeadPct:           t.DeadPct,
			TriggerThreshold:  t.TriggerThreshold,
			TriggerRatio:      t.TriggerRatio,
			ModSinceAnalyze:   t.ModSinceAnalyze,
			TotalSizeBytes:    t.TotalSizeBytes,
			TotalSize:         t.TotalSize,
			AutovacuumEnabled: t.AutovacuumEnabled,
			HasOverrides:      t.HasOverrides(),
			Overrides:         t.Overrides,
			Cost: autovacuumCostResponse{
				CostDelayMS:            t.Cost.DelayMS,
				CostDelaySource:        t.Cost.DelaySource,
				CostLimit:              t.Cost.Limit,
				CostLimitSource:        t.Cost.LimitSource,
				ThroughputPerSec:       t.Cost.ThroughputPerSec,
				MaxReadMBPerSec:        t.Cost.MaxReadMBPerSec,
				GlobalCostDelayMS:      t.Cost.GlobalDelayMS,
				GlobalCostLimit:        t.Cost.GlobalLimit,
				GlobalThroughputPerSec: t.Cost.GlobalThroughputPerSec,
				GlobalMaxReadMBPerSec:  t.Cost.GlobalMaxReadMBPerSec,
				SlowerThanGlobal:       t.Cost.SlowerThanGlobal,
				SlowdownFactor:         t.Cost.SlowdownFactor,
			},
			LastVacuum:      formatTime(t.LastVacuum),
			LastAnalyze:     formatTime(t.LastAnalyze),
			LastAutovacuum:  formatTime(t.LastAutovacuum),
			LastAutoanalyze: formatTime(t.LastAutoanalyze),
			AutovacuumCount: t.AutovacuumCount,
		})
	}
	return respond(ctx, resp)
}

// --- server info ---

func handleCheckServerInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// The envelope's meta block is the whole answer; result repeats nothing.
	return respond(ctx, struct{}{})
}

// --- xmin horizon ---

type xminHolderResponse struct {
	Source                string  `json:"source"` // session, replica, slot, prepared
	ID                    string  `json:"id"`
	Status                string  `json:"status"`
	DefinesHorizon        bool    `json:"defines_horizon"`
	XminAge               int64   `json:"xmin_age_xids"`
	CatalogOnly           bool    `json:"catalog_only,omitempty"`
	TransactionStart      *string `json:"xact_start"`
	TransactionAgeSeconds *int64  `json:"xact_age_seconds"`
	Database              string  `json:"database,omitempty"`
	User                  string  `json:"user,omitempty"`
	State                 string  `json:"state,omitempty"`
	Application           string  `json:"application,omitempty"`
	ClientAddr            string  `json:"client_addr,omitempty"`
	Query                 string  `json:"query,omitempty"`
	QueryTruncated        bool    `json:"query_truncated,omitempty"`
}

type xminHorizonResponse struct {
	HorizonXminAge       *int64               `json:"horizon_xmin_age_xids"` // null when nothing (or only catalog_xmin) holds it
	HotStandbyFeedback   bool                 `json:"hot_standby_feedback"`
	FreezeMaxAge         int64                `json:"autovacuum_freeze_max_age"`
	DatabaseFrozenXIDAge int64                `json:"database_frozen_xid_age"`
	Holders              []xminHolderResponse `json:"holders"`
}

func handleCheckXminHorizon(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	horizon, err := util.FetchXminHorizon(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := xminHorizonResponse{
		HotStandbyFeedback:   horizon.HotStandbyFeedback,
		FreezeMaxAge:         horizon.FreezeMaxAge,
		DatabaseFrozenXIDAge: horizon.DatabaseFrozenXIDAge,
		Holders:              make([]xminHolderResponse, 0, len(horizon.Holders)),
	}
	if holder := horizon.HorizonHolder(); holder != nil {
		resp.HorizonXminAge = &holder.XminAge
	}
	for _, holder := range horizon.Holders {
		var transactionStart *string
		if holder.TransactionStart != nil {
			formatted := holder.TransactionStart.Format(time.RFC3339)
			transactionStart = &formatted
		}
		query, truncated := truncateQuery(holder.Query)
		resp.Holders = append(resp.Holders, xminHolderResponse{
			Source:                holder.Source,
			ID:                    holder.ID,
			Status:                util.XminHolderStatus(holder.XminAge, holder.TransactionAgeSeconds, horizon.FreezeMaxAge).String(),
			DefinesHorizon:        holder.DefinesHorizon,
			XminAge:               holder.XminAge,
			CatalogOnly:           holder.CatalogOnly,
			TransactionStart:      transactionStart,
			TransactionAgeSeconds: holder.TransactionAgeSeconds,
			Database:              holder.Database,
			User:                  holder.User,
			State:                 holder.State,
			Application:           holder.Application,
			ClientAddr:            holder.ClientAddr,
			Query:                 query,
			QueryTruncated:        truncated,
		})
	}
	return respond(ctx, resp)
}

// --- vacuum progress ---

type vacuumProgressResponse struct {
	PID               int      `json:"pid"`
	Database          string   `json:"database"`
	SchemaName        string   `json:"schema_name"`
	TableName         string   `json:"table_name"`
	RelationID        int64    `json:"relid"`
	Status            string   `json:"status"`
	Phase             string   `json:"phase"`
	IsAutovacuum      bool     `json:"is_autovacuum"`
	IsWraparound      bool     `json:"is_wraparound"`
	HeapBlksTotal     int64    `json:"heap_blks_total"`
	HeapBlksScanned   int64    `json:"heap_blks_scanned"`
	HeapBlksVacuumed  int64    `json:"heap_blks_vacuumed"`
	ScannedPct        *float64 `json:"scanned_pct"`
	VacuumedPct       *float64 `json:"vacuumed_pct"`
	IndexVacuumCount  int64    `json:"index_vacuum_count"`
	IndexesTotal      *int64   `json:"indexes_total,omitempty"`
	IndexesProcessed  *int64   `json:"indexes_processed,omitempty"`
	DeadTuples        *int64   `json:"num_dead_tuples,omitempty"`
	MaxDeadTuples     *int64   `json:"max_dead_tuples,omitempty"`
	DeadTupleBytes    *int64   `json:"dead_tuple_bytes,omitempty"`
	MaxDeadTupleBytes *int64   `json:"max_dead_tuple_bytes,omitempty"`
	DeadItemIDs       *int64   `json:"num_dead_item_ids,omitempty"`
	DurationSeconds   *int64   `json:"duration_seconds"`
}

func handleCheckVacuumProgress(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	progress, err := util.FetchVacuumProgress(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]vacuumProgressResponse, 0, len(progress))
	for _, p := range progress {
		resp = append(resp, vacuumProgressResponse{
			PID:               p.PID,
			Database:          p.Database,
			SchemaName:        p.SchemaName,
			TableName:         p.TableName,
			RelationID:        p.RelationID,
			Status:            util.VacuumProgressStatus(p).String(),
			Phase:             p.Phase,
			IsAutovacuum:      p.IsAutovacuum,
			IsWraparound:      p.IsWraparound,
			HeapBlksTotal:     p.HeapBlksTotal,
			HeapBlksScanned:   p.HeapBlksScanned,
			HeapBlksVacuumed:  p.HeapBlksVacuumed,
			ScannedPct:        p.ScannedPct(),
			VacuumedPct:       p.VacuumedPct(),
			IndexVacuumCount:  p.IndexVacuumCount,
			IndexesTotal:      p.IndexesTotal,
			IndexesProcessed:  p.IndexesProcessed,
			DeadTuples:        p.DeadTuples,
			MaxDeadTuples:     p.MaxDeadTuples,
			DeadTupleBytes:    p.DeadTupleBytes,
			MaxDeadTupleBytes: p.MaxDeadTupleBytes,
			DeadItemIDs:       p.DeadItemIDs,
			DurationSeconds:   p.DurationSeconds,
		})
	}
	return respond(ctx, resp)
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
	return respond(ctx, resp)
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
	return respond(ctx, resp)
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
	return respond(ctx, resp)
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
	version, warning := pgStatStatementsVersion(ctx)
	return respond(ctx, pgStatStatementsResult{PgStatStatementsVersion: version, Queries: resp}, warning)
}

// --- replication slots ---

type replicationSlotResponse struct {
	SlotName      string  `json:"slot_name"`
	Plugin        string  `json:"plugin"`
	SlotType      string  `json:"slot_type"`
	Database      *string `json:"database"`
	Active        bool    `json:"active"`
	ActivePID     *int    `json:"active_pid"`
	WALLag        string  `json:"wal_lag"`
	SafeWALSize   *string `json:"safe_wal_size"`
	TwoPhase      bool    `json:"two_phase"`
	Failover      *bool   `json:"failover,omitempty"`
	Synced        *bool   `json:"synced,omitempty"`
	InactiveSince *string `json:"inactive_since,omitempty"`
}

func handleCheckReplicationSlots(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slots, err := util.FetchReplicationSlots(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]replicationSlotResponse, 0, len(slots))
	for _, s := range slots {
		resp = append(resp, replicationSlotResponse{
			SlotName:      s.SlotName,
			Plugin:        s.Plugin,
			SlotType:      s.SlotType,
			Database:      s.Database,
			Active:        s.Active,
			ActivePID:     s.ActivePID,
			WALLag:        s.WALLag,
			SafeWALSize:   s.SafeWALSize,
			TwoPhase:      s.TwoPhase,
			Failover:      s.Failover,
			Synced:        s.Synced,
			InactiveSince: s.InactiveSince,
		})
	}
	return respond(ctx, resp)
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
	return respond(ctx, resp)
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
	return respond(ctx, resp)
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
	return respond(ctx, resp)
}

// --- pg_config ---

type pgSettingResponse struct {
	Name           string `json:"name"`
	Setting        string `json:"setting"`
	Unit           string `json:"unit"`
	Category       string `json:"category"`
	Source         string `json:"source"`
	PendingRestart bool   `json:"pending_restart"`
	Description    string `json:"description,omitempty"`
}

type pgConfigResponse struct {
	Scope    string              `json:"scope"`
	Total    int                 `json:"total"`
	Returned int                 `json:"returned"`
	Offset   int                 `json:"offset"`
	Settings []pgSettingResponse `json:"settings"`
}

// handleCheckPgConfig defaults to the curated key parameters without descriptions:
// the full pg_settings with short_desc is ~80 KB, past most MCP clients' output limit.
func handleCheckPgConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filter := strParam(req, "filter", "")
	defaultScope := util.PgConfigScopeKey
	if filter != "" {
		defaultScope = util.PgConfigScopeAll
	}
	scope := util.PgConfigScope(strParam(req, "scope", string(defaultScope)))
	if req.GetBool("only_modified", false) {
		scope = util.PgConfigScopeModified
	}
	switch scope {
	case util.PgConfigScopeAll, util.PgConfigScopeKey, util.PgConfigScopeModified:
	default:
		return mcp.NewToolResultError(fmt.Sprintf("invalid scope %q: use key, modified or all", scope)), nil
	}
	opts := util.PgConfigOptions{
		Filter: filter,
		Scope:  scope,
		Limit:  intParam(req, "limit", 100),
		Offset: intParam(req, "offset", 0),
	}
	includeDescription := req.GetBool("include_description", false)
	result, err := util.FetchPgConfig(ctx, config.Config.DB, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	settings := make([]pgSettingResponse, 0, len(result.Settings))
	for _, s := range result.Settings {
		setting := pgSettingResponse{
			Name:           s.Name,
			Setting:        s.Setting,
			Unit:           s.Unit,
			Category:       s.Category,
			Source:         s.Source,
			PendingRestart: s.PendingRestart,
		}
		if includeDescription {
			setting.Description = s.Description
		}
		settings = append(settings, setting)
	}
	var warnings []string
	if returnedUpTo := opts.Offset + len(settings); returnedUpTo < result.Total {
		warnings = append(warnings, fmt.Sprintf("%d of %d settings returned; call again with offset=%d for more",
			len(settings), result.Total, returnedUpTo))
	}
	return respond(ctx, pgConfigResponse{
		Scope:    string(scope),
		Total:    result.Total,
		Returned: len(settings),
		Offset:   opts.Offset,
		Settings: settings,
	}, warnings...)
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
	return respond(ctx, resp)
}

// --- autovacuum detail ---

type autovacuumDetailResponse struct {
	SchemaName        string `json:"schema_name"`
	TableName         string `json:"table_name"`
	LiveTuples        int64  `json:"live_tuples"`
	DeadTuples        int64  `json:"dead_tuples"`
	ModSinceAnalyze   int64  `json:"mod_since_analyze"`
	VacuumCount       int64  `json:"vacuum_count"`
	AutovacuumCount   int64  `json:"autovacuum_count"`
	AnalyzeCount      int64  `json:"analyze_count"`
	AutoanalyzeCount  int64  `json:"autoanalyze_count"`
	TableSize         string `json:"table_size"`
	TotalSize         string `json:"total_size"`
	ToastAndIndexSize string `json:"toast_and_index_size"`
	FrozenXIDAge      int64  `json:"frozen_xid_age"`
	MXIDAge           int64  `json:"mxid_age"`
	LastVacuum        string `json:"last_vacuum"`
	LastAutovacuum    string `json:"last_autovacuum"`
	LastAnalyze       string `json:"last_analyze"`
	LastAutoanalyze   string `json:"last_autoanalyze"`
}

type autovacuumParamResponse struct {
	Name        string `json:"name"`
	TableValue  string `json:"table_value"`
	GlobalValue string `json:"global_value"`
	Unit        string `json:"unit"`
}

type thresholdSettingResponse struct {
	Value  float64 `json:"value"`
	Source string  `json:"source"` // "table", "global" or "adjusted"
}

type rowThresholdResponse struct {
	Base             thresholdSettingResponse  `json:"base"`
	ScaleFactor      thresholdSettingResponse  `json:"scale_factor"`
	MaxThreshold     *thresholdSettingResponse `json:"max_threshold,omitempty"`
	UnfrozenFraction *float64                  `json:"unfrozen_fraction,omitempty"`
	Threshold        float64                   `json:"threshold"`
	Current          int64                     `json:"current"`
	Due              bool                      `json:"due"`
}

type ageThresholdResponse struct {
	Limit   thresholdSettingResponse `json:"limit"`
	Current int64                    `json:"current_age"`
	Due     bool                     `json:"due"`
}

type autovacuumThresholdsResponse struct {
	AutovacuumEnabled       bool                     `json:"autovacuum_enabled"`
	AutovacuumEnabledSource string                   `json:"autovacuum_enabled_source"`
	TrackCounts             bool                     `json:"track_counts"`
	Reltuples               float64                  `json:"reltuples"`
	ReltuplesUnknown        bool                     `json:"reltuples_unknown"`
	AutovacuumDue           bool                     `json:"autovacuum_due"`
	VacuumDeadTuples        rowThresholdResponse     `json:"vacuum_dead_tuples"`
	VacuumInserts           *rowThresholdResponse    `json:"vacuum_inserts"` // null when disabled (-1)
	Analyze                 rowThresholdResponse     `json:"analyze"`
	XIDWraparound           ageThresholdResponse     `json:"xid_wraparound"`
	XIDAggressive           ageThresholdResponse     `json:"xid_aggressive"`
	XIDFreezeMinAge         thresholdSettingResponse `json:"xid_freeze_min_age"`
	XIDFailsafe             *ageThresholdResponse    `json:"xid_failsafe"` // null on PG13
	MXIDWraparound          ageThresholdResponse     `json:"mxid_wraparound"`
	MXIDAggressive          ageThresholdResponse     `json:"mxid_aggressive"`
	MXIDFreezeMinAge        thresholdSettingResponse `json:"mxid_freeze_min_age"`
	MXIDFailsafe            *ageThresholdResponse    `json:"mxid_failsafe"` // null on PG13
}

func toSettingResponse(setting util.ThresholdSetting) thresholdSettingResponse {
	return thresholdSettingResponse{Value: setting.Value, Source: setting.Source}
}

func toRowThresholdResponse(check util.RowThreshold) rowThresholdResponse {
	resp := rowThresholdResponse{
		Base:             toSettingResponse(check.Base),
		ScaleFactor:      toSettingResponse(check.ScaleFactor),
		UnfrozenFraction: check.UnfrozenFraction,
		Threshold:        check.Threshold,
		Current:          check.Current,
		Due:              check.Due,
	}
	if check.MaxThreshold != nil {
		maxThreshold := toSettingResponse(*check.MaxThreshold)
		resp.MaxThreshold = &maxThreshold
	}
	return resp
}

func toAgeThresholdResponse(check util.AgeThreshold) ageThresholdResponse {
	return ageThresholdResponse{Limit: toSettingResponse(check.Limit), Current: check.Current, Due: check.Due}
}

func toOptionalAgeThresholdResponse(check *util.AgeThreshold) *ageThresholdResponse {
	if check == nil {
		return nil
	}
	resp := toAgeThresholdResponse(*check)
	return &resp
}

func toThresholdsResponse(thresholds util.AutovacuumThresholds) autovacuumThresholdsResponse {
	resp := autovacuumThresholdsResponse{
		AutovacuumEnabled:       thresholds.AutovacuumEnabled,
		AutovacuumEnabledSource: thresholds.AutovacuumEnabledSource,
		TrackCounts:             thresholds.TrackCounts,
		Reltuples:               thresholds.Reltuples,
		ReltuplesUnknown:        thresholds.ReltuplesUnknown,
		AutovacuumDue:           thresholds.AutovacuumDue(),
		VacuumDeadTuples:        toRowThresholdResponse(thresholds.DeadTuples),
		Analyze:                 toRowThresholdResponse(thresholds.Analyze),
		XIDWraparound:           toAgeThresholdResponse(thresholds.XIDWraparound),
		XIDAggressive:           toAgeThresholdResponse(thresholds.XIDAggressive),
		XIDFreezeMinAge:         toSettingResponse(thresholds.XIDFreezeMinAge),
		XIDFailsafe:             toOptionalAgeThresholdResponse(thresholds.XIDFailsafe),
		MXIDWraparound:          toAgeThresholdResponse(thresholds.MXIDWraparound),
		MXIDAggressive:          toAgeThresholdResponse(thresholds.MXIDAggressive),
		MXIDFreezeMinAge:        toSettingResponse(thresholds.MXIDFreezeMinAge),
		MXIDFailsafe:            toOptionalAgeThresholdResponse(thresholds.MXIDFailsafe),
	}
	if thresholds.Inserts != nil {
		inserts := toRowThresholdResponse(*thresholds.Inserts)
		resp.VacuumInserts = &inserts
	}
	return resp
}

type autovacuumDetailResult struct {
	Stats      autovacuumDetailResponse      `json:"stats"`
	Params     []autovacuumParamResponse     `json:"params"`
	Thresholds *autovacuumThresholdsResponse `json:"thresholds,omitempty"`
}

func handleCheckAutovacuumDetail(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	schema := strParam(req, "schema", "public")
	table := strParam(req, "table", "")
	if table == "" {
		return mcp.NewToolResultError("table parameter is required"), nil
	}
	stats, err := util.FetchAutovacuumDetail(ctx, config.Config.DB, schema, table)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	params, _ := util.FetchAutovacuumParams(ctx, config.Config.DB, schema, table)
	var thresholdsResp *autovacuumThresholdsResponse
	if thresholds, err := util.FetchAutovacuumThresholds(ctx, config.Config.DB, schema, table); err == nil {
		computed := toThresholdsResponse(thresholds)
		thresholdsResp = &computed
	}
	paramResp := make([]autovacuumParamResponse, 0, len(params))
	for _, p := range params {
		paramResp = append(paramResp, autovacuumParamResponse{
			Name:        p.Name,
			TableValue:  p.TableValue,
			GlobalValue: p.GlobalValue,
			Unit:        p.Unit,
		})
	}
	return respond(ctx, autovacuumDetailResult{
		Stats: autovacuumDetailResponse{
			SchemaName:        stats.SchemaName,
			TableName:         stats.TableName,
			LiveTuples:        stats.LiveTuples,
			DeadTuples:        stats.DeadTuples,
			ModSinceAnalyze:   stats.ModSinceAnalyze,
			VacuumCount:       stats.VacuumCount,
			AutovacuumCount:   stats.AutovacuumCount,
			AnalyzeCount:      stats.AnalyzeCount,
			AutoanalyzeCount:  stats.AutoanalyzeCount,
			TableSize:         stats.TableSize,
			TotalSize:         stats.TotalSize,
			ToastAndIndexSize: stats.ToastAndIndexSize,
			FrozenXIDAge:      stats.FrozenXIDAge,
			MXIDAge:           stats.MXIDAge,
			LastVacuum:        formatTime(stats.LastVacuum),
			LastAutovacuum:    formatTime(stats.LastAutovacuum),
			LastAnalyze:       formatTime(stats.LastAnalyze),
			LastAutoanalyze:   formatTime(stats.LastAutoanalyze),
		},
		Params:     paramResp,
		Thresholds: thresholdsResp,
	})
}

// --- freeze by database ---

type freezeDatabaseResponse struct {
	DatabaseName      string  `json:"database_name"`
	DBXIDAge          int64   `json:"db_xid_age"`
	PctTowardShutdown float64 `json:"pct_toward_shutdown"`
	DBMXIDAge         int64   `json:"db_mxid_age"`
	Status            string  `json:"status"`
}

func handleCheckFreezeByDatabase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dbs, err := util.FetchFreezeByDatabase(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	statusLabels := []string{"ok", "warning", "critical"}
	resp := make([]freezeDatabaseResponse, 0, len(dbs))
	for _, d := range dbs {
		statusLabel := "ok"
		if d.Status < len(statusLabels) {
			statusLabel = statusLabels[d.Status]
		}
		resp = append(resp, freezeDatabaseResponse{
			DatabaseName:      d.DatabaseName,
			DBXIDAge:          d.DBXIDAge,
			PctTowardShutdown: d.PctTowardShutdown,
			DBMXIDAge:         d.DBMXIDAge,
			Status:            statusLabel,
		})
	}
	return respond(ctx, resp)
}

// --- freeze by table ---

type freezeTableResponse struct {
	SchemaName      string  `json:"schema_name"`
	TableName       string  `json:"table_name"`
	XIDAge          int64   `json:"xid_age"`
	MXIDAge         int64   `json:"mxid_age"`
	FreezeMaxAge    int64   `json:"freeze_max_age"`
	PctTowardFreeze float64 `json:"pct_toward_freeze"`
	TotalSize       string  `json:"total_size"`
	LastAutovacuum  string  `json:"last_autovacuum"`
	Status          string  `json:"status"`
}

func handleCheckFreezeByTable(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := intParam(req, "limit", 50)
	tables, err := util.FetchFreezeByTable(ctx, config.Config.DB, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	statusLabels := []string{"ok", "warning", "critical"}
	resp := make([]freezeTableResponse, 0, len(tables))
	for _, t := range tables {
		statusLabel := "ok"
		if t.Status < len(statusLabels) {
			statusLabel = statusLabels[t.Status]
		}
		resp = append(resp, freezeTableResponse{
			SchemaName:      t.SchemaName,
			TableName:       t.TableName,
			XIDAge:          t.XIDAge,
			MXIDAge:         t.MXIDAge,
			FreezeMaxAge:    t.FreezeMaxAge,
			PctTowardFreeze: t.PctTowardFreeze,
			TotalSize:       t.TotalSize,
			LastAutovacuum:  formatTime(t.LastAutovacuum),
			Status:          statusLabel,
		})
	}
	return respond(ctx, resp)
}

// --- streaming standbys ---

type streamingStandbyResponse struct {
	ApplicationName string `json:"application_name"`
	ClientAddr      string `json:"client_addr"`
	State           string `json:"state"`
	SyncState       string `json:"sync_state"`
	SentLSN         string `json:"sent_lsn"`
	WriteLSN        string `json:"write_lsn"`
	FlushLSN        string `json:"flush_lsn"`
	ReplayLSN       string `json:"replay_lsn"`
	WriteLag        string `json:"write_lag"`
	FlushLag        string `json:"flush_lag"`
	ReplayLag       string `json:"replay_lag"`
	LagBytes        int64  `json:"lag_bytes"`
	PID             int    `json:"pid"`
}

func handleCheckStreamingStandbys(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	standbys, err := util.FetchStreamingStandbys(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]streamingStandbyResponse, 0, len(standbys))
	for _, s := range standbys {
		resp = append(resp, streamingStandbyResponse{
			ApplicationName: s.ApplicationName,
			ClientAddr:      s.ClientAddr,
			State:           s.State,
			SyncState:       s.SyncState,
			SentLSN:         s.SentLSN,
			WriteLSN:        s.WriteLSN,
			FlushLSN:        s.FlushLSN,
			ReplayLSN:       s.ReplayLSN,
			WriteLag:        s.WriteLag,
			FlushLag:        s.FlushLag,
			ReplayLag:       s.ReplayLag,
			LagBytes:        s.LagBytes,
			PID:             s.PID,
		})
	}
	return respond(ctx, resp)
}

// --- replication config ---

type replicationParamResponse struct {
	Name      string `json:"name"`
	Setting   string `json:"setting"`
	Unit      string `json:"unit"`
	Context   string `json:"context"`
	ShortDesc string `json:"short_desc"`
	Hint      string `json:"hint"`
	HintLevel int    `json:"hint_level"`
}

type replicationConfigResponse struct {
	Params        []replicationParamResponse `json:"params"`
	ActiveSenders int                        `json:"active_senders"`
	TotalSlots    int                        `json:"total_slots"`
	ActiveSlots   int                        `json:"active_slots"`
}

func handleCheckReplicationConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, counts, err := util.FetchReplicationConfig(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	paramResp := make([]replicationParamResponse, 0, len(params))
	for _, p := range params {
		paramResp = append(paramResp, replicationParamResponse{
			Name:      p.Name,
			Setting:   p.Setting,
			Unit:      p.Unit,
			Context:   p.Context,
			ShortDesc: p.ShortDesc,
			Hint:      p.Hint,
			HintLevel: p.HintLevel,
		})
	}
	return respond(ctx, replicationConfigResponse{
		Params:        paramResp,
		ActiveSenders: counts.ActiveSenders,
		TotalSlots:    counts.TotalSlots,
		ActiveSlots:   counts.ActiveSlots,
	})
}

// --- database sizes ---

type databaseSizeResponse struct {
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	Encoding   string `json:"encoding"`
	SizeBytes  int64  `json:"size_bytes"`
	SizePretty string `json:"size_pretty"`
}

type tablespaceSizeResponse struct {
	Name       string `json:"name"`
	Location   string `json:"location"`
	SizeBytes  int64  `json:"size_bytes"`
	SizePretty string `json:"size_pretty"`
}

type databaseSizeReportResponse struct {
	Databases   []databaseSizeResponse   `json:"databases"`
	Tablespaces []tablespaceSizeResponse `json:"tablespaces"`
	TotalBytes  int64                    `json:"total_bytes"`
	TotalPretty string                   `json:"total_pretty"`
}

func handleCheckDatabaseSizes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	report, err := util.FetchDatabaseSizes(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	databases := make([]databaseSizeResponse, 0, len(report.Databases))
	for _, d := range report.Databases {
		databases = append(databases, databaseSizeResponse{
			Name:       d.Name,
			Owner:      d.Owner,
			Encoding:   d.Encoding,
			SizeBytes:  d.SizeBytes,
			SizePretty: d.SizePretty,
		})
	}
	tablespaces := make([]tablespaceSizeResponse, 0, len(report.Tablespaces))
	for _, t := range report.Tablespaces {
		tablespaces = append(tablespaces, tablespaceSizeResponse{
			Name:       t.Name,
			Location:   t.Location,
			SizeBytes:  t.SizeBytes,
			SizePretty: t.SizePretty,
		})
	}
	return respond(ctx, databaseSizeReportResponse{
		Databases:   databases,
		Tablespaces: tablespaces,
		TotalBytes:  report.TotalBytes,
		TotalPretty: report.TotalPretty,
	})
}

// --- temp file usage ---

type tempFileUsageResponse struct {
	Database   string `json:"database"`
	TempFiles  int64  `json:"temp_files"`
	TempBytes  int64  `json:"temp_bytes"`
	TempPretty string `json:"temp_pretty"`
	StatsReset string `json:"stats_reset"`
}

func handleCheckTempFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	usage, err := util.FetchTempFileUsage(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]tempFileUsageResponse, 0, len(usage))
	for _, u := range usage {
		resp = append(resp, tempFileUsageResponse{
			Database:   u.Database,
			TempFiles:  u.TempFiles,
			TempBytes:  u.TempBytes,
			TempPretty: u.TempPretty,
			StatsReset: u.StatsReset,
		})
	}
	return respond(ctx, resp)
}

// --- memory & checkpoint stats ---

type memoryConfigResponse struct {
	Name      string `json:"name"`
	Setting   string `json:"setting"`
	Unit      string `json:"unit"`
	ShortDesc string `json:"short_desc"`
}

type checkpointStatsResponse struct {
	CheckpointsTimed    int64  `json:"checkpoints_timed"`
	CheckpointsReq      int64  `json:"checkpoints_req"`
	BuffersCheckpoint   int64  `json:"buffers_checkpoint"`
	BuffersClean        int64  `json:"buffers_clean"`
	MaxwrittenClean     int64  `json:"maxwritten_clean"`
	BuffersBackend      *int64 `json:"buffers_backend,omitempty"`
	BuffersBackendFsync *int64 `json:"buffers_backend_fsync,omitempty"`
	BuffersAlloc        int64  `json:"buffers_alloc"`
	StatsReset          string `json:"stats_reset"`
}

type memoryStatsResponse struct {
	Configs       []memoryConfigResponse  `json:"configs"`
	CacheHitRatio float64                 `json:"cache_hit_ratio"`
	Checkpoint    checkpointStatsResponse `json:"checkpoint"`
}

func handleCheckMemoryStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	stats, err := util.FetchMemoryStats(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	configs := make([]memoryConfigResponse, 0, len(stats.Configs))
	for _, c := range stats.Configs {
		configs = append(configs, memoryConfigResponse{
			Name:      c.Name,
			Setting:   c.Setting,
			Unit:      c.Unit,
			ShortDesc: c.ShortDesc,
		})
	}
	return respond(ctx, memoryStatsResponse{
		Configs:       configs,
		CacheHitRatio: stats.CacheHitRatio,
		Checkpoint: checkpointStatsResponse{
			CheckpointsTimed:    stats.Checkpoint.CheckpointsTimed,
			CheckpointsReq:      stats.Checkpoint.CheckpointsReq,
			BuffersCheckpoint:   stats.Checkpoint.BuffersCheckpoint,
			BuffersClean:        stats.Checkpoint.BuffersClean,
			MaxwrittenClean:     stats.Checkpoint.MaxwrittenClean,
			BuffersBackend:      stats.Checkpoint.BuffersBackend,
			BuffersBackendFsync: stats.Checkpoint.BuffersBackendFsync,
			BuffersAlloc:        stats.Checkpoint.BuffersAlloc,
			StatsReset:          stats.Checkpoint.StatsReset,
		},
	}, stats.Warnings...)
}

// --- publications ---

type publicationResponse struct {
	PubName    string `json:"pub_name"`
	AllTables  bool   `json:"all_tables"`
	Insert     bool   `json:"insert"`
	Update     bool   `json:"update"`
	Delete     bool   `json:"delete"`
	Truncate   bool   `json:"truncate"`
	ViaRoot    bool   `json:"via_root"`
	GenCols    *bool  `json:"gen_cols,omitempty"`
	TableCount int    `json:"table_count"`
}

func handleCheckPublications(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pubs, err := util.FetchPublications(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]publicationResponse, len(pubs))
	for i, pub := range pubs {
		out[i] = publicationResponse{
			PubName:    pub.PubName,
			AllTables:  pub.AllTables,
			Insert:     pub.Insert,
			Update:     pub.Update,
			Delete:     pub.Delete,
			Truncate:   pub.Truncate,
			ViaRoot:    pub.ViaRoot,
			GenCols:    pub.GenCols,
			TableCount: pub.TableCount,
		}
	}
	return respond(ctx, out)
}

// --- subscriptions ---

type subscriptionResponse struct {
	SubName         string  `json:"sub_name"`
	Enabled         bool    `json:"enabled"`
	SlotName        string  `json:"slot_name"`
	Publications    string  `json:"publications"`
	WorkerPID       *int    `json:"worker_pid,omitempty"`
	ReceivedLSN     string  `json:"received_lsn"`
	LastReceiveTime *string `json:"last_receive_time,omitempty"`
	ApplyErrorCount *int64  `json:"apply_error_count,omitempty"`
	SyncErrorCount  *int64  `json:"sync_error_count,omitempty"`
	TwoPhaseState   *string `json:"two_phase_state,omitempty"`
	DisableOnError  *bool   `json:"disable_on_error,omitempty"`
	Failover        *bool   `json:"failover,omitempty"`
}

type publicationTableResponse struct {
	SchemaName string `json:"schema_name"`
	TableName  string `json:"table_name"`
	Columns    string `json:"columns"`
	RowFilter  string `json:"row_filter,omitempty"`
	LiveRows   int64  `json:"live_rows"`
	DeadRows   int64  `json:"dead_rows"`
	SeqScans   int64  `json:"seq_scans"`
	IdxScans   int64  `json:"idx_scans"`
	LastVacuum string `json:"last_vacuum"`
}

func handleCheckPublicationTables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pubname := strParam(req, "name", "")
	if pubname == "" {
		return mcp.NewToolResultError("name parameter is required"), nil
	}
	tables, err := util.FetchPublicationTables(ctx, config.Config.DB, pubname)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]publicationTableResponse, len(tables))
	for i, t := range tables {
		out[i] = publicationTableResponse{
			SchemaName: t.SchemaName,
			TableName:  t.TableName,
			Columns:    t.Columns,
			RowFilter:  t.RowFilter,
			LiveRows:   t.LiveRows,
			DeadRows:   t.DeadRows,
			SeqScans:   t.SeqScans,
			IdxScans:   t.IdxScans,
			LastVacuum: t.LastVacuum,
		}
	}
	return respond(ctx, out)
}

type subscriptionTableResponse struct {
	SchemaName string `json:"schema_name"`
	TableName  string `json:"table_name"`
	SyncState  string `json:"sync_state"`
	SyncLSN    string `json:"sync_lsn,omitempty"`
	Columns    string `json:"columns,omitempty"`
	LiveRows   int64  `json:"live_rows"`
	InsRows    int64  `json:"ins_rows"`
	UpdRows    int64  `json:"upd_rows"`
	DelRows    int64  `json:"del_rows"`
}

func handleCheckSubscriptionTables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	subname := strParam(req, "name", "")
	if subname == "" {
		return mcp.NewToolResultError("name parameter is required"), nil
	}
	tables, err := util.FetchSubscriptionTables(ctx, config.Config.DB, subname)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]subscriptionTableResponse, len(tables))
	for i, t := range tables {
		out[i] = subscriptionTableResponse{
			SchemaName: t.SchemaName,
			TableName:  t.TableName,
			SyncState:  t.SyncState,
			SyncLSN:    t.SyncLSN,
			Columns:    t.Columns,
			LiveRows:   t.LiveRows,
			InsRows:    t.InsRows,
			UpdRows:    t.UpdRows,
			DelRows:    t.DelRows,
		}
	}
	return respond(ctx, out)
}

func handleCheckToastTables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type toastTableResponse struct {
		SchemaName      string   `json:"schema_name"`
		TableName       string   `json:"table_name"`
		ToastRelname    string   `json:"toast_relname"`
		ToastSizePretty string   `json:"toast_size"`
		ToastPct        float64  `json:"toast_pct"`
		ToastDeadTuples int64    `json:"toast_dead_tuples"`
		BlksRead        int64    `json:"blks_read"`
		BlksHit         int64    `json:"blks_hit"`
		CacheHitPct     *float64 `json:"cache_hit_pct"`
		LastAutovacuum  *string  `json:"last_autovacuum"`
		ToastColumns    string   `json:"toast_columns"`
	}
	tables, err := util.FetchToastTables(ctx, config.Config.DB, 50)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]toastTableResponse, len(tables))
	for i, tt := range tables {
		var lastAV *string
		if tt.LastAutovacuum != nil {
			formatted := tt.LastAutovacuum.Format("2006-01-02 15:04:05")
			lastAV = &formatted
		}
		out[i] = toastTableResponse{
			SchemaName:      tt.SchemaName,
			TableName:       tt.TableName,
			ToastRelname:    tt.ToastRelname,
			ToastSizePretty: tt.ToastSizePretty,
			ToastPct:        tt.ToastPct,
			ToastDeadTuples: tt.ToastDeadTuples,
			BlksRead:        tt.BlksRead,
			BlksHit:         tt.BlksHit,
			CacheHitPct:     tt.CacheHitPct,
			LastAutovacuum:  lastAV,
			ToastColumns:    tt.ToastColumns,
		}
	}
	return respond(ctx, out)
}

func handleCheckSubscriptions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	subs, err := util.FetchSubscriptions(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]subscriptionResponse, len(subs))
	for i, sub := range subs {
		out[i] = subscriptionResponse{
			SubName:         sub.SubName,
			Enabled:         sub.Enabled,
			SlotName:        sub.SlotName,
			Publications:    sub.Publications,
			WorkerPID:       sub.WorkerPID,
			ReceivedLSN:     sub.ReceivedLSN,
			LastReceiveTime: sub.LastReceiveTime,
			ApplyErrorCount: sub.ApplyErrorCount,
			SyncErrorCount:  sub.SyncErrorCount,
			TwoPhaseState:   sub.TwoPhaseState,
			DisableOnError:  sub.DisableOnError,
			Failover:        sub.Failover,
		}
	}
	return respond(ctx, out)
}

// --- replica identity ---

type replicaIdentityResponse struct {
	SchemaName            string   `json:"schema_name"`
	TableName             string   `json:"table_name"`
	HasPrimaryKey         bool     `json:"has_primary_key"`
	ReplicaIdentity       string   `json:"replica_identity"`
	Publications          []string `json:"publications"`
	PublishesUpdateDelete bool     `json:"publishes_update_delete"`
	PglogicalSets         []string `json:"pglogical_sets"`
	PglogicalUpdateDelete bool     `json:"pglogical_update_delete"`
	CandidateIndex        *string  `json:"candidate_index"`
	SuggestedFix          string   `json:"suggested_fix"`
	TotalSize             string   `json:"total_size"`
	Status                string   `json:"status"` // "critical" = UPDATE/DELETE can't be replicated today
}

func handleCheckReplicaIdentity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	issues, err := util.FetchReplicaIdentityIssues(ctx, config.Config.DB)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]replicaIdentityResponse, 0, len(issues))
	for _, issue := range issues {
		status := "warning"
		if issue.IsCritical() {
			status = "critical"
		}
		resp = append(resp, replicaIdentityResponse{
			SchemaName:            issue.SchemaName,
			TableName:             issue.TableName,
			HasPrimaryKey:         issue.HasPrimaryKey,
			ReplicaIdentity:       issue.ReplicaIdentity,
			Publications:          issue.Publications,
			PublishesUpdateDelete: issue.PublishesUpdateDelete,
			PglogicalSets:         issue.PglogicalSets,
			PglogicalUpdateDelete: issue.PglogicalUpdateDelete,
			CandidateIndex:        issue.CandidateIndex,
			SuggestedFix:          issue.SuggestedFix(),
			TotalSize:             issue.TotalSize,
			Status:                status,
		})
	}
	return respond(ctx, resp)
}
