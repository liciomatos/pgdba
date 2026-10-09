package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	Status          string  `json:"status"` // worst of connections, cache hit, blocked, long-running, invalid indexes, freeze
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
	HeapHitPct                 *float64 `json:"heap_hit_pct"`
	IdxHitPct                  *float64 `json:"idx_hit_pct"`
	CacheHitStatus             string   `json:"cache_hit_status"`
	HeapHitFormula             string   `json:"heap_hit_formula"`
	DeadTuples                 int64    `json:"dead_tuples"`
	InvalidIndexes             int      `json:"invalid_indexes"`
	ReplicationSlots           int      `json:"replication_slots"`
}

func handleCheckDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	threshold := config.Config.SlowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}
	data, err := util.FetchDashboard(ctx, targetDB(ctx), threshold)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// FetchDashboard fills host/database from the main connection's flags; report the
	// server actually queried.
	target := targetFromContext(ctx)
	return respond(ctx, dashboardResponse{
		Status:                     util.DashboardStatus(data).String(),
		Host:                       target.Host,
		Database:                   target.DBName,
		UsedConnections:            data.UsedConnections,
		MaxConnections:             data.MaxConnections,
		ConnectionPct:              data.ConnectionPct,
		ActiveQueries:              data.ActiveQueries,
		BlockedQueries:             data.BlockedQueries,
		SlowQueryCount:             data.SlowQueryCount,
		SlowQueryUnavailableReason: data.SlowQueryUnavailableReason,
		SlowThresholdMS:            data.SlowThresholdMS,
		HeapHitPct:                 data.CacheHitRatio,
		IdxHitPct:                  data.IndexHitRatio,
		CacheHitStatus:             util.CacheHitStatus(data.CacheHitRatio).String(),
		HeapHitFormula:             util.HeapHitFormula,
		DeadTuples:                 data.DeadTuples,
		InvalidIndexes:             data.InvalidIndexes,
		ReplicationSlots:           data.ReplicationSlots,
	}, data.Warnings...)
}

// --- slow queries ---

type slowQueryResponse struct {
	Status           string  `json:"status"`
	QueryTruncated   bool    `json:"query_truncated,omitempty"`
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
	info, err := util.FetchPgStatStatementsInfo(ctx, targetDB(ctx))
	if err != nil {
		return "", "reading pg_stat_statements version: " + err.Error()
	}
	return info.Version, info.UpdateHint()
}

func handleCheckSlowQueries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	threshold := int(req.GetFloat("threshold_ms", 1000))
	limit := intParam(req, "limit", 20)
	queries, err := util.FetchSlowQueries(ctx, targetDB(ctx), threshold, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]slowQueryResponse, 0, len(queries))
	for _, q := range queries {
		query, truncated := truncateQuery(q.Query)
		resp = append(resp, slowQueryResponse{
			QueryID:          q.QueryID,
			Query:            query,
			QueryTruncated:   truncated,
			Status:           util.SlowQueryStatus(q.MeanExecTimeMS, threshold).String(),
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
	Status          string  `json:"status"`
	QueryTruncated  bool    `json:"query_truncated,omitempty"`
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
	queries, err := util.FetchLongRunningQueries(ctx, targetDB(ctx), minDuration, limit, includeBackground)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]longRunningQueryResponse, 0, len(queries))
	for _, q := range queries {
		query, truncated := truncateQuery(q.Query)
		resp = append(resp, longRunningQueryResponse{
			Status:          util.LongRunningStatus(q.DurationSeconds).String(),
			QueryTruncated:  truncated,
			PID:             q.PID,
			Username:        q.Username,
			ApplicationName: q.ApplicationName,
			ClientAddr:      q.ClientAddr,
			BackendType:     q.BackendType,
			State:           q.State,
			WaitEventType:   q.WaitEventType,
			WaitEvent:       q.WaitEvent,
			DurationSeconds: q.DurationSeconds,
			Query:           query,
		})
	}
	return respond(ctx, resp)
}

// --- blocked queries ---

type blockedQueryResponse struct {
	Status              string `json:"status"` // always critical: a session is waiting on another one's lock
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
	blocked, err := util.FetchBlockedQueries(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]blockedQueryResponse, 0, len(blocked))
	for _, b := range blocked {
		blockedStatement, _ := truncateQuery(b.BlockedStatement)
		blockingStatement, _ := truncateQuery(b.BlockingStatement)
		resp = append(resp, blockedQueryResponse{
			Status:              util.StatusCritical.String(),
			BlockedPID:          b.BlockedPID,
			BlockedUser:         b.BlockedUser,
			BlockingPID:         b.BlockingPID,
			BlockingUser:        b.BlockingUser,
			BlockedStatement:    blockedStatement,
			BlockingStatement:   blockingStatement,
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

type connectionGroupResponse struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type idleTransactionResponse struct {
	PID                   int    `json:"pid"`
	Status                string `json:"status"`
	User                  string `json:"user"`
	Application           string `json:"application"`
	ClientAddr            string `json:"client_addr"`
	State                 string `json:"state"`
	StateAgeSeconds       int64  `json:"idle_seconds"`
	TransactionAgeSeconds *int64 `json:"xact_age_seconds"`
	Query                 string `json:"last_query"`
	QueryTruncated        bool   `json:"query_truncated,omitempty"`
}

type connectionsResponse struct {
	Status                      string                    `json:"status"`
	States                      []connectionStateResponse `json:"states"`
	TotalUsed                   int                       `json:"total_used"`
	MaxAllowed                  int                       `json:"max_allowed"`
	UsagePct                    float64                   `json:"usage_pct"`
	TopApplications             []connectionGroupResponse `json:"top_applications"`
	TopUsers                    []connectionGroupResponse `json:"top_users"`
	TopClients                  []connectionGroupResponse `json:"top_clients"`
	IdleInTransaction           []idleTransactionResponse `json:"idle_in_transaction"`
	OldestTransactionAgeSeconds *int64                    `json:"oldest_xact_age_seconds"`
}

func toGroupResponses(groups []util.ConnectionGroup) []connectionGroupResponse {
	resp := make([]connectionGroupResponse, 0, len(groups))
	for _, group := range groups {
		resp = append(resp, connectionGroupResponse{Name: group.Name, Count: group.Count})
	}
	return resp
}

func handleCheckConnections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := util.FetchConnections(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	states := make([]connectionStateResponse, 0, len(result.States))
	for _, s := range result.States {
		states = append(states, connectionStateResponse{State: s.State, Count: s.Count})
	}
	// Overall status: the connection usage, or a long idle-in-transaction session.
	status := util.ConnectionUsageStatus(result.UsagePct)
	idle := make([]idleTransactionResponse, 0, len(result.IdleInTransaction))
	for _, session := range result.IdleInTransaction {
		sessionStatus := util.IdleInTransactionStatus(session.StateAgeSeconds)
		status = max(status, sessionStatus)
		query, truncated := truncateQuery(session.Query)
		idle = append(idle, idleTransactionResponse{
			PID:                   session.PID,
			Status:                sessionStatus.String(),
			User:                  session.User,
			Application:           session.Application,
			ClientAddr:            session.ClientAddr,
			State:                 session.State,
			StateAgeSeconds:       session.StateAgeSeconds,
			TransactionAgeSeconds: session.TransactionAgeSeconds,
			Query:                 query,
			QueryTruncated:        truncated,
		})
	}
	return respond(ctx, connectionsResponse{
		Status:                      status.String(),
		States:                      states,
		TotalUsed:                   result.TotalUsed,
		MaxAllowed:                  result.MaxAllowed,
		UsagePct:                    result.UsagePct,
		TopApplications:             toGroupResponses(result.TopApplications),
		TopUsers:                    toGroupResponses(result.TopUsers),
		TopClients:                  toGroupResponses(result.TopClients),
		IdleInTransaction:           idle,
		OldestTransactionAgeSeconds: result.OldestTransactionAgeSeconds,
	}, result.Warnings...)
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
	tables, err := util.FetchAutovacuum(ctx, targetDB(ctx), limit)
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
	Status               string               `json:"status"`                // status of the holder that defines the horizon; ok when none
	HorizonXminAge       *int64               `json:"horizon_xmin_age_xids"` // null when nothing (or only catalog_xmin) holds it
	HotStandbyFeedback   bool                 `json:"hot_standby_feedback"`
	FreezeMaxAge         int64                `json:"autovacuum_freeze_max_age"`
	DatabaseFrozenXIDAge int64                `json:"database_frozen_xid_age"`
	Holders              []xminHolderResponse `json:"holders"`
}

func handleCheckXminHorizon(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	horizon, err := util.FetchXminHorizon(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := xminHorizonResponse{
		HotStandbyFeedback:   horizon.HotStandbyFeedback,
		FreezeMaxAge:         horizon.FreezeMaxAge,
		DatabaseFrozenXIDAge: horizon.DatabaseFrozenXIDAge,
		Holders:              make([]xminHolderResponse, 0, len(horizon.Holders)),
	}
	resp.Status = util.StatusOK.String()
	if holder := horizon.HorizonHolder(); holder != nil {
		resp.HorizonXminAge = &holder.XminAge
		resp.Status = util.XminHolderStatus(holder.XminAge, holder.TransactionAgeSeconds, horizon.FreezeMaxAge).String()
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
	progress, err := util.FetchVacuumProgress(ctx, targetDB(ctx))
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

type indexRedundancyResponse struct {
	Index string `json:"index"`
	Kind  string `json:"kind"` // duplicate, prefix, same_columns_different_order
}

type indexUsageResponse struct {
	SchemaName        string                    `json:"schema_name"`
	TableName         string                    `json:"table_name"`
	IndexName         string                    `json:"index_name"`
	IndexColumns      string                    `json:"index_columns"`
	Status            string                    `json:"status"`
	IsValid           bool                      `json:"is_valid"`
	IsPrimary         bool                      `json:"is_primary"`
	IsUnique          bool                      `json:"is_unique"`
	IsConstraint      bool                      `json:"is_constraint"`
	AccessMethod      string                    `json:"access_method"`
	IsPartial         bool                      `json:"is_partial"`
	IdxScan           int64                     `json:"idx_scan"`
	IdxTupRead        int64                     `json:"idx_tup_read"`
	IdxTupFetch       int64                     `json:"idx_tup_fetch"`
	TupReadPerScan    *float64                  `json:"tup_read_per_scan"`
	TableScanSharePct *float64                  `json:"table_scan_share_pct"`
	IndexSizeBytes    int64                     `json:"index_size_bytes"`
	IndexSize         string                    `json:"index_size_pretty"`
	WasteScoreBytes   float64                   `json:"waste_score_bytes"`
	RedundantWith     []indexRedundancyResponse `json:"redundant_with"`
}

func toIndexUsageResponse(idx util.IndexUsage) indexUsageResponse {
	redundancies := make([]indexRedundancyResponse, 0, len(idx.RedundantWith))
	for _, redundancy := range idx.RedundantWith {
		redundancies = append(redundancies, indexRedundancyResponse{Index: redundancy.OtherIndex, Kind: redundancy.Kind})
	}
	return indexUsageResponse{
		SchemaName:        idx.SchemaName,
		TableName:         idx.TableName,
		IndexName:         idx.IndexName,
		IndexColumns:      idx.IndexColumns,
		Status:            util.IndexUsageStatus(idx).String(),
		IsValid:           idx.IsValid,
		IsPrimary:         idx.IsPrimary,
		IsUnique:          idx.IsUnique,
		IsConstraint:      idx.IsConstraint,
		AccessMethod:      idx.AccessMethod,
		IsPartial:         idx.IsPartial,
		IdxScan:           idx.IdxScan,
		IdxTupRead:        idx.IdxTupRead,
		IdxTupFetch:       idx.IdxTupFetch,
		TupReadPerScan:    idx.TupReadPerScan,
		TableScanSharePct: idx.TableScanSharePct,
		IndexSizeBytes:    idx.IndexSizeBytes,
		IndexSize:         idx.IndexSize,
		WasteScoreBytes:   idx.WasteScore(),
		RedundantWith:     redundancies,
	}
}

func handleCheckIndexUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts := util.IndexUsageOptions{
		Limit:              intParam(req, "limit", 50),
		IncludeConstraints: req.GetBool("include_constraints", false),
	}
	if req.GetBool("compare", false) {
		return compareIndexUsage(ctx, opts)
	}
	indexes, err := util.FetchIndexUsage(ctx, targetDB(ctx), opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]indexUsageResponse, 0, len(indexes))
	for _, idx := range indexes {
		resp = append(resp, toIndexUsageResponse(idx))
	}
	return respond(ctx, resp)
}

// indexTargetUsage is one index's counters on one server.
type indexTargetUsage struct {
	IdxScan           int64    `json:"idx_scan"`
	IdxTupRead        int64    `json:"idx_tup_read"`
	IdxTupFetch       int64    `json:"idx_tup_fetch"`
	TableScanSharePct *float64 `json:"table_scan_share_pct"`
}

type indexComparisonResponse struct {
	SchemaName     string                    `json:"schema_name"`
	TableName      string                    `json:"table_name"`
	IndexName      string                    `json:"index_name"`
	IndexColumns   string                    `json:"index_columns"`
	IndexSizeBytes int64                     `json:"index_size_bytes"`
	IndexSize      string                    `json:"index_size_pretty"`
	IsConstraint   bool                      `json:"is_constraint"`
	IsValid        bool                      `json:"is_valid"`
	RedundantWith  []indexRedundancyResponse `json:"redundant_with"`
	// PerTarget has the counters of every server that answered; a missing key means
	// the index doesn't exist there (or that target failed — see warnings).
	PerTarget        map[string]indexTargetUsage `json:"per_target"`
	TotalScans       int64                       `json:"total_scans"`
	UnusedEverywhere bool                        `json:"unused_everywhere"`
	Status           string                      `json:"status"`
	// primary is the first server's row, rated with the scans of every server.
	primary util.IndexUsage
}

// compareIndexUsage runs check_index_usage on the primary and every replica and joins
// the counters by index, because a standby keeps its own idx_scan: an index unused on
// the primary may serve the read traffic of a replica. Size, redundancy and validity
// come from the primary. A failing replica becomes a warning.
func compareIndexUsage(ctx context.Context, opts util.IndexUsageOptions) (*mcp.CallToolResult, error) {
	limit := opts.Limit
	opts.Limit = 0 // rank after joining, not per server
	targets := allTargets()
	var warnings []string
	if len(targets) == 1 {
		warnings = append(warnings, "no --replica configured: compare shows the primary only")
	}
	byIndex := map[string]*indexComparisonResponse{}
	var order []string
	for _, target := range targets {
		indexes, err := util.FetchIndexUsage(ctx, target.DB, opts)
		if err != nil {
			if target.Name == primaryTargetName {
				return mcp.NewToolResultError(err.Error()), nil
			}
			warnings = append(warnings, fmt.Sprintf("target %s: %v", target.Name, err))
			continue
		}
		for _, idx := range indexes {
			key := idx.SchemaName + "." + idx.IndexName
			comparison, seen := byIndex[key]
			if !seen {
				primary := toIndexUsageResponse(idx)
				comparison = &indexComparisonResponse{
					SchemaName: idx.SchemaName, TableName: idx.TableName, IndexName: idx.IndexName,
					IndexColumns: idx.IndexColumns, IndexSizeBytes: idx.IndexSizeBytes, IndexSize: idx.IndexSize,
					IsConstraint: idx.IsConstraint, IsValid: idx.IsValid, RedundantWith: primary.RedundantWith,
					PerTarget: map[string]indexTargetUsage{}, primary: idx,
				}
				byIndex[key] = comparison
				order = append(order, key)
			}
			comparison.PerTarget[target.Name] = indexTargetUsage{
				IdxScan: idx.IdxScan, IdxTupRead: idx.IdxTupRead, IdxTupFetch: idx.IdxTupFetch,
				TableScanSharePct: idx.TableScanSharePct,
			}
			comparison.TotalScans += idx.IdxScan
		}
	}
	resp := make([]indexComparisonResponse, 0, len(order))
	for _, key := range order {
		comparison := byIndex[key]
		comparison.UnusedEverywhere = comparison.TotalScans == 0
		// "Unused" must mean unused on every server, so rate it with the summed scans.
		combined := comparison.primary
		combined.IdxScan = comparison.TotalScans
		comparison.Status = util.IndexUsageStatus(combined).String()
		resp = append(resp, *comparison)
	}
	// Indexes no server uses come first, biggest first; then the least used overall.
	sort.SliceStable(resp, func(i, j int) bool {
		if resp[i].TotalScans != resp[j].TotalScans {
			return resp[i].TotalScans < resp[j].TotalScans
		}
		return resp[i].IndexSizeBytes > resp[j].IndexSizeBytes
	})
	if limit > 0 && len(resp) > limit {
		resp = resp[:limit]
	}
	return respond(ctx, resp, warnings...)
}

// --- cache hit ---

// maxDeltaSeconds caps delta_seconds: the tool sleeps that long, and MCP clients time
// out long calls.
const maxDeltaSeconds = 60

// deltaInterval reads delta_seconds (0 = cumulative since the stats reset).
func deltaInterval(req mcp.CallToolRequest) (time.Duration, error) {
	seconds := req.GetFloat("delta_seconds", 0)
	if seconds < 0 || seconds > maxDeltaSeconds {
		return 0, fmt.Errorf("delta_seconds must be between 0 and %d", maxDeltaSeconds)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

type cacheHitRatioResponse struct {
	Pct     *float64 `json:"pct"`
	Status  string   `json:"status"`
	Hits    int64    `json:"blks_hit"`
	Reads   int64    `json:"blks_read"`
	Formula string   `json:"formula"`
}

type cacheHitSummaryResponse struct {
	IntervalSeconds float64               `json:"interval_seconds"` // 0 = cumulative since stats reset
	Heap            cacheHitRatioResponse `json:"heap"`
	Index           cacheHitRatioResponse `json:"index"`
	Database        cacheHitRatioResponse `json:"database"`
}

func toCacheHitSummaryResponse(summary util.CacheHitSummary, intervalSeconds float64) cacheHitSummaryResponse {
	ratio := func(pct *float64, hits, reads int64, formula string) cacheHitRatioResponse {
		return cacheHitRatioResponse{Pct: pct, Status: util.CacheHitStatus(pct).String(), Hits: hits, Reads: reads, Formula: formula}
	}
	return cacheHitSummaryResponse{
		IntervalSeconds: intervalSeconds,
		Heap:            ratio(summary.HeapHitPct, summary.HeapBlksHit, summary.HeapBlksRead, util.HeapHitFormula),
		Index:           ratio(summary.IdxHitPct, summary.IdxBlksHit, summary.IdxBlksRead, util.IndexHitFormula),
		Database:        ratio(summary.DatabaseHitPct, summary.DatabaseBlksHit, summary.DatabaseBlksRead, util.DatabaseHitFormula),
	}
}

type cacheHitTableResponse struct {
	SchemaName   string   `json:"schema_name"`
	TableName    string   `json:"table_name"`
	Status       string   `json:"status"`
	HeapBlksRead int64    `json:"heap_blks_read"`
	HeapBlksHit  int64    `json:"heap_blks_hit"`
	HeapHitPct   *float64 `json:"heap_hit_pct"`
	IdxBlksRead  int64    `json:"idx_blks_read"`
	IdxBlksHit   int64    `json:"idx_blks_hit"`
	IdxHitPct    *float64 `json:"idx_hit_pct"`
}

type cacheHitResponse struct {
	Summary cacheHitSummaryResponse `json:"summary"`
	Tables  []cacheHitTableResponse `json:"tables"`
}

func handleCheckCacheHit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	interval, err := deltaInterval(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	report, err := util.FetchCacheHitReport(ctx, targetDB(ctx), interval, intParam(req, "limit", 50))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tables := make([]cacheHitTableResponse, 0, len(report.Tables))
	for _, t := range report.Tables {
		tables = append(tables, cacheHitTableResponse{
			SchemaName:   t.SchemaName,
			TableName:    t.TableName,
			Status:       util.TableCacheHitStatus(t.HeapHitPct).String(),
			HeapBlksRead: t.HeapBlksRead,
			HeapBlksHit:  t.HeapBlksHit,
			HeapHitPct:   t.HeapHitPct,
			IdxBlksRead:  t.IdxBlksRead,
			IdxBlksHit:   t.IdxBlksHit,
			IdxHitPct:    t.IdxHitPct,
		})
	}
	return respond(ctx, cacheHitResponse{
		Summary: toCacheHitSummaryResponse(report.Summary, report.IntervalSeconds),
		Tables:  tables,
	})
}

// --- wait events ---

type waitEventResponse struct {
	EventType       string  `json:"event_type"` // "CPU" = running, not waiting
	Event           string  `json:"event"`
	Status          string  `json:"status"`
	AAS             float64 `json:"aas"` // average active sessions over the samples
	SessionsSampled int     `json:"sessions_sampled"`
	Pct             float64 `json:"pct"`
}

type waitEventsResponse struct {
	Status      string              `json:"status"` // worst event status
	Samples     int                 `json:"samples"`
	IntervalMS  int64               `json:"interval_ms"`
	IncludeIdle bool                `json:"include_idle"`
	TotalAAS    float64             `json:"total_aas"` // equals the sum of the events' aas
	Events      []waitEventResponse `json:"events"`
}

// maxWaitSamplingSeconds caps samples × interval: the call blocks while sampling.
const maxWaitSamplingSeconds = 60

func handleCheckWaitEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	samples := intParam(req, "samples", 20)
	intervalMS := intParam(req, "interval_ms", 1000)
	if samples < 1 || intervalMS < 0 || (samples-1)*intervalMS > maxWaitSamplingSeconds*1000 {
		return mcp.NewToolResultError(fmt.Sprintf("samples must be ≥ 1 and (samples-1) × interval_ms ≤ %d s", maxWaitSamplingSeconds)), nil
	}
	sampling, err := util.FetchWaitEvents(ctx, targetDB(ctx), samples, time.Duration(intervalMS)*time.Millisecond,
		req.GetBool("include_idle", false))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	events := make([]waitEventResponse, 0, len(sampling.Events))
	overall := util.StatusOK
	for _, e := range sampling.Events {
		eventStatus := util.WaitEventStatus(e.EventType, e.AAS)
		overall = max(overall, eventStatus)
		events = append(events, waitEventResponse{
			EventType:       e.EventType,
			Event:           e.Event,
			Status:          eventStatus.String(),
			AAS:             e.AAS,
			SessionsSampled: e.Count,
			Pct:             e.Pct,
		})
	}
	return respond(ctx, waitEventsResponse{
		Status:      overall.String(),
		Samples:     sampling.Samples,
		IntervalMS:  int64(intervalMS),
		IncludeIdle: sampling.IncludeIdle,
		TotalAAS:    sampling.TotalAAS,
		Events:      events,
	})
}

// --- query load ---

type queryLoadResponse struct {
	Status         string  `json:"status"`
	QueryTruncated bool    `json:"query_truncated,omitempty"`
	QueryID        string  `json:"query_id"`
	Query          string  `json:"query"`
	Calls          int     `json:"calls"`
	TotalMS        float64 `json:"total_ms"`
	MeanMS         float64 `json:"mean_ms"`
	BufferMB       float64 `json:"buffer_mb"`
	TempMB         float64 `json:"temp_mb"`
	LoadPct        float64 `json:"load_pct"`
}

func handleCheckQueryLoad(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := intParam(req, "limit", 20)
	queries, err := util.FetchQueryLoad(ctx, targetDB(ctx), limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]queryLoadResponse, 0, len(queries))
	for _, q := range queries {
		query, truncated := truncateQuery(q.Query)
		resp = append(resp, queryLoadResponse{
			Status:         util.QueryLoadStatus(q.LoadPct).String(),
			QueryTruncated: truncated,
			QueryID:        q.QueryID,
			Query:          query,
			Calls:          q.Calls,
			TotalMS:        q.TotalMS,
			MeanMS:         q.MeanMS,
			BufferMB:       q.BufferMB,
			TempMB:         q.TempMB,
			LoadPct:        q.LoadPct,
		})
	}
	version, warning := pgStatStatementsVersion(ctx)
	return respond(ctx, pgStatStatementsResult{PgStatStatementsVersion: version, Queries: resp}, warning)
}

// --- replication slots ---

type replicationSlotResponse struct {
	SlotName         string  `json:"slot_name"`
	Plugin           string  `json:"plugin"`
	SlotType         string  `json:"slot_type"`
	Database         *string `json:"database"`
	Active           bool    `json:"active"`
	ActivePID        *int    `json:"active_pid"`
	Status           string  `json:"status"`
	WALStatus        string  `json:"wal_status"`
	WALLagBytes      *int64  `json:"wal_lag_bytes"`
	WALLag           string  `json:"wal_lag_pretty"`
	SafeWALSizeBytes *int64  `json:"safe_wal_size_bytes"`
	SafeWALSize      *string `json:"safe_wal_size_pretty"`
	TwoPhase         bool    `json:"two_phase"`
	Failover         *bool   `json:"failover,omitempty"`
	Synced           *bool   `json:"synced,omitempty"`
	InactiveSince    *string `json:"inactive_since,omitempty"`
}

func handleCheckReplicationSlots(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slots, err := util.FetchReplicationSlots(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]replicationSlotResponse, 0, len(slots))
	for _, s := range slots {
		resp = append(resp, replicationSlotResponse{
			Status:           util.ReplicationSlotStatus(s).String(),
			WALStatus:        s.WALStatus,
			WALLagBytes:      s.WALLagBytes,
			SafeWALSizeBytes: s.SafeWALSizeBytes,
			SlotName:         s.SlotName,
			Plugin:           s.Plugin,
			SlotType:         s.SlotType,
			Database:         s.Database,
			Active:           s.Active,
			ActivePID:        s.ActivePID,
			WALLag:           s.WALLag,
			SafeWALSize:      s.SafeWALSize,
			TwoPhase:         s.TwoPhase,
			Failover:         s.Failover,
			Synced:           s.Synced,
			InactiveSince:    s.InactiveSince,
		})
	}
	return respond(ctx, resp)
}

// --- users ---

type userResponse struct {
	Status      string `json:"status"` // critical = password expired, warning = expires within 7 days
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
	users, err := util.FetchUsers(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]userResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, userResponse{
			Status:      util.UserStatus(u.ValidUntil, time.Now()).String(),
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
	Status     string `json:"status"` // informational: always ok
	RoleName   string `json:"role_name"`
	Superuser  bool   `json:"superuser"`
	Inherit    bool   `json:"inherit"`
	CreateRole bool   `json:"create_role"`
	CreateDB   bool   `json:"create_db"`
	Members    string `json:"members"`
}

func handleCheckRoles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	roles, err := util.FetchRoles(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]roleResponse, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, roleResponse{
			Status:     util.StatusOK.String(),
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
	DefaultVersion string `json:"default_version"`
	Status         string `json:"status"` // warning = older than default_version (ALTER EXTENSION ... UPDATE)
	Name           string `json:"name"`
	Version        string `json:"version"`
	Schema         string `json:"schema"`
	Description    string `json:"description"`
}

func handleCheckExtensions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	exts, err := util.FetchExtensions(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]extensionResponse, 0, len(exts))
	for _, e := range exts {
		resp = append(resp, extensionResponse{
			DefaultVersion: e.DefaultVersion,
			Status:         util.ExtensionStatus(e).String(),
			Name:           e.Name,
			Version:        e.Version,
			Schema:         e.Schema,
			Description:    e.Description,
		})
	}
	return respond(ctx, resp)
}

// --- pg_config ---

type pgSettingResponse struct {
	Status         string `json:"status"` // warning = changed but waiting for a restart
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
	result, err := util.FetchPgConfig(ctx, targetDB(ctx), opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	settings := make([]pgSettingResponse, 0, len(result.Settings))
	for _, s := range result.Settings {
		settingStatus := util.StatusOK
		if s.PendingRestart {
			settingStatus = util.StatusWarning
		}
		setting := pgSettingResponse{
			Status:         settingStatus.String(),
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
	Status     string                 `json:"status"` // informational: always ok
	SizeBytes  int64                  `json:"size_bytes"`
	SizePretty string                 `json:"size_pretty"`
	EstRows    int64                  `json:"est_rows"`
	Columns    []schemaColumnResponse `json:"columns"`
}

func handleCheckSchema(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	schema := strParam(req, "schema", "public")
	tables, err := util.FetchSchema(ctx, targetDB(ctx), schema)
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
			Status:     util.StatusOK.String(),
			SizeBytes:  t.SizeBytes,
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
	TableSizeBytes    int64  `json:"table_size_bytes"`
	TotalSizeBytes    int64  `json:"total_size_bytes"`
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
	Status     string                        `json:"status"` // from thresholds: critical = anti-wraparound/failsafe due, warning = autovacuum due
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
	stats, err := util.FetchAutovacuumDetail(ctx, targetDB(ctx), schema, table)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	params, _ := util.FetchAutovacuumParams(ctx, targetDB(ctx), schema, table)
	var thresholdsResp *autovacuumThresholdsResponse
	status := util.StatusOK
	var warnings []string
	if thresholds, err := util.FetchAutovacuumThresholds(ctx, targetDB(ctx), schema, table); err == nil {
		computed := toThresholdsResponse(thresholds)
		thresholdsResp = &computed
		status = util.AutovacuumThresholdsStatus(thresholds)
	} else {
		warnings = append(warnings, "thresholds: "+err.Error())
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
		Status: status.String(),
		Stats: autovacuumDetailResponse{
			TableSizeBytes:    stats.TableSizeBytes,
			TotalSizeBytes:    stats.TotalSizeBytes,
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
	}, warnings...)
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
	dbs, err := util.FetchFreezeByDatabase(ctx, targetDB(ctx))
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
	TotalSizeBytes  int64   `json:"total_size_bytes"`
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
	tables, err := util.FetchFreezeByTable(ctx, targetDB(ctx), limit)
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
			TotalSizeBytes:  t.TotalSizeBytes,
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
	Status           string   `json:"status"`
	WriteLagSeconds  *float64 `json:"write_lag_seconds"`
	FlushLagSeconds  *float64 `json:"flush_lag_seconds"`
	ReplayLagSeconds *float64 `json:"replay_lag_seconds"`
	ApplicationName  string   `json:"application_name"`
	ClientAddr       string   `json:"client_addr"`
	State            string   `json:"state"`
	SyncState        string   `json:"sync_state"`
	SentLSN          string   `json:"sent_lsn"`
	WriteLSN         string   `json:"write_lsn"`
	FlushLSN         string   `json:"flush_lsn"`
	ReplayLSN        string   `json:"replay_lsn"`
	WriteLag         string   `json:"write_lag"`
	FlushLag         string   `json:"flush_lag"`
	ReplayLag        string   `json:"replay_lag"`
	LagBytes         int64    `json:"lag_bytes"`
	PID              int      `json:"pid"`
}

func handleCheckStreamingStandbys(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	standbys, err := util.FetchStreamingStandbys(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := make([]streamingStandbyResponse, 0, len(standbys))
	for _, s := range standbys {
		resp = append(resp, streamingStandbyResponse{
			Status:           util.StandbyLagStatus(s.LagBytes, s.ReplayLagSeconds).String(),
			WriteLagSeconds:  s.WriteLagSeconds,
			FlushLagSeconds:  s.FlushLagSeconds,
			ReplayLagSeconds: s.ReplayLagSeconds,
			ApplicationName:  s.ApplicationName,
			ClientAddr:       s.ClientAddr,
			State:            s.State,
			SyncState:        s.SyncState,
			SentLSN:          s.SentLSN,
			WriteLSN:         s.WriteLSN,
			FlushLSN:         s.FlushLSN,
			ReplayLSN:        s.ReplayLSN,
			WriteLag:         s.WriteLag,
			FlushLag:         s.FlushLag,
			ReplayLag:        s.ReplayLag,
			LagBytes:         s.LagBytes,
			PID:              s.PID,
		})
	}
	return respond(ctx, resp)
}

// --- replication config ---

type replicationParamResponse struct {
	Status    string `json:"status"`
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
	params, counts, err := util.FetchReplicationConfig(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	paramResp := make([]replicationParamResponse, 0, len(params))
	for _, p := range params {
		paramResp = append(paramResp, replicationParamResponse{
			Status:    util.HintLevelStatus(p.HintLevel).String(),
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
	Status     string `json:"status"` // informational: always ok
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
	report, err := util.FetchDatabaseSizes(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	databases := make([]databaseSizeResponse, 0, len(report.Databases))
	for _, d := range report.Databases {
		databases = append(databases, databaseSizeResponse{
			Status:     util.StatusOK.String(),
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
	Database        string             `json:"database"`
	Status          string             `json:"status"` // warning = any temp file since the stats reset
	TempFiles       int64              `json:"temp_files"`
	TempBytes       int64              `json:"temp_bytes"`
	TempPretty      string             `json:"temp_pretty"`
	AvgBytesPerFile *float64           `json:"avg_bytes_per_file"`
	StatsReset      statsResetResponse `json:"stats_reset"`
}

type tempQueryResponse struct {
	QueryID          int64   `json:"query_id"`
	Query            string  `json:"query"`
	QueryTruncated   bool    `json:"query_truncated,omitempty"`
	Calls            int64   `json:"calls"`
	TempBlksWritten  int64   `json:"temp_blks_written"`
	TempBytesWritten int64   `json:"temp_bytes_written"`
	TempBlksRead     int64   `json:"temp_blks_read"`
	MeanTimeMS       float64 `json:"mean_time_ms"`
}

type tempFilesResponse struct {
	Databases  []tempFileUsageResponse `json:"databases"`
	TopQueries []tempQueryResponse     `json:"top_queries"` // from pg_stat_statements; empty without it (see warnings)
}

// tempTopQueries is how many statements check_temp_files attributes temp files to.
const tempTopQueries = 5

func handleCheckTempFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	usage, err := util.FetchTempFileUsage(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	now := time.Now()
	databases := make([]tempFileUsageResponse, 0, len(usage))
	for _, u := range usage {
		databases = append(databases, tempFileUsageResponse{
			Database:        u.Database,
			Status:          util.TempFileStatus(u).String(),
			TempFiles:       u.TempFiles,
			TempBytes:       u.TempBytes,
			TempPretty:      u.TempPretty,
			AvgBytesPerFile: u.AvgBytesPerFile,
			StatsReset:      toStatsReset(u.StatsReset, now),
		})
	}
	var warnings []string
	topQueries := []tempQueryResponse{}
	queries, err := util.FetchTopTempQueries(ctx, targetDB(ctx), tempTopQueries)
	if err != nil {
		warnings = append(warnings, "top temp queries: "+err.Error())
	}
	for _, q := range queries {
		query, truncated := truncateQuery(q.Query)
		topQueries = append(topQueries, tempQueryResponse{
			QueryID:          q.QueryID,
			Query:            query,
			QueryTruncated:   truncated,
			Calls:            q.Calls,
			TempBlksWritten:  q.TempBlksWritten,
			TempBytesWritten: q.TempBytesWritten,
			TempBlksRead:     q.TempBlksRead,
			MeanTimeMS:       q.MeanTimeMS,
		})
	}
	return respond(ctx, tempFilesResponse{Databases: databases, TopQueries: topQueries}, warnings...)
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
	Status     string                  `json:"status"` // worst cache hit status; warning when requested checkpoints exceed timed ones
	Configs    []memoryConfigResponse  `json:"configs"`
	CacheHit   cacheHitSummaryResponse `json:"cache_hit"`
	Checkpoint checkpointStatsResponse `json:"checkpoint"`
}

func handleCheckMemoryStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	interval, err := deltaInterval(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	stats, err := util.FetchMemoryStats(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cacheHit := toCacheHitSummaryResponse(stats.CacheHit, 0)
	if interval > 0 {
		report, err := util.FetchCacheHitReport(ctx, targetDB(ctx), interval, 0)
		if err != nil {
			stats.Warnings = append(stats.Warnings, "cache hit delta: "+err.Error())
		} else {
			cacheHit = toCacheHitSummaryResponse(report.Summary, report.IntervalSeconds)
		}
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
		Status:   util.MemoryStatsStatus(stats).String(),
		Configs:  configs,
		CacheHit: cacheHit,
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
	Status     string `json:"status"` // informational: always ok
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
	pubs, err := util.FetchPublications(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]publicationResponse, len(pubs))
	for i, pub := range pubs {
		out[i] = publicationResponse{
			Status:     util.StatusOK.String(),
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
	Status          string  `json:"status"` // critical = enabled without a running worker; warning = disabled or errors
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
	Status     string `json:"status"` // informational: always ok
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
	tables, err := util.FetchPublicationTables(ctx, targetDB(ctx), pubname)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]publicationTableResponse, len(tables))
	for i, t := range tables {
		out[i] = publicationTableResponse{
			Status:     util.StatusOK.String(),
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
	Status     string `json:"status"` // warning until the table is ready
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
	tables, err := util.FetchSubscriptionTables(ctx, targetDB(ctx), subname)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]subscriptionTableResponse, len(tables))
	for i, t := range tables {
		out[i] = subscriptionTableResponse{
			Status:     util.SubscriptionTableStatus(t.SyncState).String(),
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
		Status          string   `json:"status"` // TOAST heap cache hit: critical < 70%, warning < 90%
		ToastSizeBytes  int64    `json:"toast_size_bytes"`
		ToastSizePretty string   `json:"toast_size_pretty"`
		ToastPct        float64  `json:"toast_pct"`
		ToastDeadTuples int64    `json:"toast_dead_tuples"`
		BlksRead        int64    `json:"blks_read"`
		BlksHit         int64    `json:"blks_hit"`
		CacheHitPct     *float64 `json:"cache_hit_pct"`
		LastAutovacuum  *string  `json:"last_autovacuum"`
		ToastColumns    string   `json:"toast_columns"`
	}
	tables, err := util.FetchToastTables(ctx, targetDB(ctx), 50)
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
			Status:          util.TableCacheHitStatus(tt.CacheHitPct).String(),
			ToastSizeBytes:  tt.ToastSizeBytes,
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
	subs, err := util.FetchSubscriptions(ctx, targetDB(ctx))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]subscriptionResponse, len(subs))
	for i, sub := range subs {
		out[i] = subscriptionResponse{
			Status:          util.SubscriptionStatus(sub).String(),
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
	issues, err := util.FetchReplicaIdentityIssues(ctx, targetDB(ctx))
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

// --- table churn ---

type tableBloatResponse struct {
	TableLenBytes   int64   `json:"table_len_bytes"`
	ScannedPct      float64 `json:"scanned_pct"`
	DeadTupleBytes  int64   `json:"dead_tuple_bytes"`
	DeadTuplePct    float64 `json:"dead_tuple_pct"`
	ApproxFreeBytes int64   `json:"approx_free_bytes"`
	ApproxFreePct   float64 `json:"approx_free_pct"`
}

type tableChurnResponse struct {
	SchemaName         string              `json:"schema_name"`
	TableName          string              `json:"table_name"`
	Status             string              `json:"status"`
	Hint               string              `json:"hint,omitempty"`
	Inserts            int64               `json:"n_tup_ins"`
	Updates            int64               `json:"n_tup_upd"`
	Deletes            int64               `json:"n_tup_del"`
	HotUpdates         int64               `json:"n_tup_hot_upd"`
	NewPageUpdates     *int64              `json:"n_tup_newpage_upd,omitempty"` // PG16+
	HotPct             *float64            `json:"hot_pct"`
	Fillfactor         int                 `json:"fillfactor"`
	FillfactorSet      bool                `json:"fillfactor_set"`
	LiveTuples         int64               `json:"live_tuples"`
	DeadTuples         int64               `json:"dead_tuples"`
	TableSizeBytes     int64               `json:"table_size_bytes"`
	TotalSizeBytes     int64               `json:"total_size_bytes"`
	TotalSize          string              `json:"total_size_pretty"`
	Bloat              *tableBloatResponse `json:"bloat,omitempty"`
	BloatSkippedReason string              `json:"bloat_skipped_reason,omitempty"`
}

func handleCheckTableChurn(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tables, err := util.FetchTableChurn(ctx, targetDB(ctx), intParam(req, "limit", 20))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var bloat map[string]util.TableBloatEstimate
	var warnings []string
	if req.GetBool("include_bloat", false) {
		maxBytes := int64(req.GetFloat("bloat_max_table_bytes", util.DefaultBloatMaxTableBytes))
		if bloat, err = util.FetchTableBloat(ctx, targetDB(ctx), tables, maxBytes); err != nil {
			warnings = append(warnings, "bloat estimate: "+err.Error())
		}
	}
	resp := make([]tableChurnResponse, 0, len(tables))
	for _, t := range tables {
		row := tableChurnResponse{
			SchemaName:     t.SchemaName,
			TableName:      t.TableName,
			Status:         util.TableChurnStatus(t).String(),
			Hint:           util.TableChurnHint(t),
			Inserts:        t.Inserts,
			Updates:        t.Updates,
			Deletes:        t.Deletes,
			HotUpdates:     t.HotUpdates,
			NewPageUpdates: t.NewPageUpdates,
			HotPct:         t.HotPct,
			Fillfactor:     t.Fillfactor,
			FillfactorSet:  t.FillfactorSet,
			LiveTuples:     t.LiveTuples,
			DeadTuples:     t.DeadTuples,
			TableSizeBytes: t.TableSizeBytes,
			TotalSizeBytes: t.TotalSizeBytes,
			TotalSize:      t.TotalSize,
		}
		if estimate, ok := bloat[t.SchemaName+"."+t.TableName]; ok {
			if estimate.SkippedReason != "" {
				row.BloatSkippedReason = estimate.SkippedReason
			} else {
				row.Bloat = &tableBloatResponse{
					TableLenBytes:   estimate.TableLenBytes,
					ScannedPct:      estimate.ScannedPct,
					DeadTupleBytes:  estimate.DeadTupleBytes,
					DeadTuplePct:    estimate.DeadTuplePct,
					ApproxFreeBytes: estimate.ApproxFreeBytes,
					ApproxFreePct:   estimate.ApproxFreePct,
				}
			}
		}
		resp = append(resp, row)
	}
	return respond(ctx, resp, warnings...)
}

// --- I/O and WAL ---

// unsupportedResponse answers a tool whose view doesn't exist on this server version:
// a normal result, not an error, so the client can tell "unavailable" from "failed".
type unsupportedResponse struct {
	Status        string `json:"status"` // always "unsupported"
	Reason        string `json:"reason"`
	ServerVersion string `json:"server_version"`
}

// unsupportedResult returns the unsupported response when err is an UnsupportedError.
func unsupportedResult(ctx context.Context, err error) (*mcp.CallToolResult, bool) {
	var unsupported util.UnsupportedError
	if !errors.As(err, &unsupported) {
		return nil, false
	}
	result, _ := respond(ctx, unsupportedResponse{Status: "unsupported", Reason: unsupported.Error(), ServerVersion: unsupported.ServerVersion})
	return result, true
}

type ioRowResponse struct {
	BackendType string `json:"backend_type"`
	Object      string `json:"object"`
	Context     string `json:"context"`
	Reads       int64  `json:"reads"`
	ReadBytes   int64  `json:"read_bytes"`
	Writes      int64  `json:"writes"`
	WriteBytes  int64  `json:"write_bytes"`
	Writebacks  int64  `json:"writebacks"`
	Extends     int64  `json:"extends"`
	ExtendBytes int64  `json:"extend_bytes"`
	Hits        int64  `json:"hits"`
	Evictions   int64  `json:"evictions"`
	Reuses      int64  `json:"reuses"`
	Fsyncs      int64  `json:"fsyncs"`
}

type ioShareResponse struct {
	BackendType string  `json:"backend_type"`
	Writes      int64   `json:"writes"`
	Pct         float64 `json:"pct"`
}

type ioResponse struct {
	Status                string            `json:"status"`
	StatsReset            *string           `json:"stats_reset"`
	ClientBackendWritePct *float64          `json:"client_backend_normal_write_pct"`
	RelationWritesBy      []ioShareResponse `json:"relation_writes_by_backend_type"`
	Rows                  []ioRowResponse   `json:"rows"`
}

func handleCheckIO(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	stats, err := util.FetchIOStats(ctx, targetDB(ctx))
	if result, unsupported := unsupportedResult(ctx, err); unsupported {
		return result, nil
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := ioResponse{
		Status:                util.IOStatsStatus(stats).String(),
		ClientBackendWritePct: stats.ClientBackendWritePct,
		RelationWritesBy:      make([]ioShareResponse, 0, len(stats.WritesByBackend)),
		Rows:                  make([]ioRowResponse, 0, len(stats.Rows)),
	}
	if stats.StatsReset != nil {
		formatted := stats.StatsReset.Format(time.RFC3339)
		resp.StatsReset = &formatted
	}
	for _, share := range stats.WritesByBackend {
		resp.RelationWritesBy = append(resp.RelationWritesBy, ioShareResponse{BackendType: share.BackendType, Writes: share.Writes, Pct: share.Pct})
	}
	for _, row := range stats.Rows {
		resp.Rows = append(resp.Rows, ioRowResponse(row))
	}
	return respond(ctx, resp)
}

type walResponse struct {
	Status            string   `json:"status"`
	Records           int64    `json:"wal_records"`
	FPI               int64    `json:"wal_fpi"`
	Bytes             int64    `json:"wal_bytes"`
	BuffersFull       int64    `json:"wal_buffers_full"`
	FPIPct            *float64 `json:"fpi_pct"`
	BytesPerRecord    *float64 `json:"bytes_per_record"`
	BytesPerSecond    *float64 `json:"bytes_per_second_since_reset"`
	StatsReset        *string  `json:"stats_reset"`
	SecondsSinceReset *int64   `json:"seconds_since_reset"`
	FullPageWrites    string   `json:"full_page_writes"`
	WALCompression    string   `json:"wal_compression"`
	CheckpointTimeout string   `json:"checkpoint_timeout"`
	MaxWALSize        string   `json:"max_wal_size"`
	WALBuffers        string   `json:"wal_buffers"`
}

func handleCheckWAL(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	stats, err := util.FetchWALStats(ctx, targetDB(ctx))
	if result, unsupported := unsupportedResult(ctx, err); unsupported {
		return result, nil
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp := walResponse{
		Status:            util.WALStatsStatus(stats).String(),
		Records:           stats.Records,
		FPI:               stats.FPI,
		Bytes:             stats.Bytes,
		BuffersFull:       stats.BuffersFull,
		FPIPct:            stats.FPIPct(),
		BytesPerRecord:    stats.BytesPerRecord(),
		BytesPerSecond:    stats.BytesPerSecond(),
		SecondsSinceReset: stats.SecondsSinceReset,
		FullPageWrites:    stats.FullPageWrites,
		WALCompression:    stats.WALCompression,
		CheckpointTimeout: stats.CheckpointTimeout,
		MaxWALSize:        stats.MaxWALSize,
		WALBuffers:        stats.WALBuffers,
	}
	if stats.StatsReset != nil {
		formatted := stats.StatsReset.Format(time.RFC3339)
		resp.StatsReset = &formatted
	}
	return respond(ctx, resp)
}
