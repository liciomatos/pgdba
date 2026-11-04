package util

import "testing"

func TestParseUpsert(t *testing.T) {
	cases := []struct {
		query, target, action string
		hasWhere              bool
	}{
		{`INSERT INTO accounts (id, balance) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET balance = EXCLUDED.balance`, "accounts", "update", false},
		{`insert into "Sales"."Daily Totals" (day) values ($1) on conflict do nothing`, `"Sales"."Daily Totals"`, "nothing", false},
		{`INSERT INTO app . events (id) VALUES ($1) ON CONFLICT ON CONSTRAINT events_pkey DO UPDATE SET seen = EXCLUDED.seen WHERE events.seen IS DISTINCT FROM EXCLUDED.seen`, "app.events", "update", true},
		{`WITH src AS (SELECT $1 AS id) INSERT INTO t SELECT id FROM src WHERE id > $2 ON CONFLICT (id) DO UPDATE SET n = t.n + $3`, "t", "update", false},
	}
	for _, c := range cases {
		target, action, hasWhere := parseUpsert(c.query)
		if target != c.target || action != c.action || hasWhere != c.hasWhere {
			t.Errorf("parseUpsert(%q) = (%q, %q, %v), want (%q, %q, %v)", c.query, target, action, hasWhere, c.target, c.action, c.hasWhere)
		}
	}
}

func TestUpsertStatementHint(t *testing.T) {
	if UpsertStatementHint(UpsertStatement{Action: "update"}) == "" {
		t.Error("DO UPDATE without WHERE should get a hint")
	}
	if UpsertStatementHint(UpsertStatement{Action: "update", UpdateHasWhere: true}) != "" ||
		UpsertStatementHint(UpsertStatement{Action: "nothing"}) != "" {
		t.Error("DO UPDATE … WHERE and DO NOTHING need no hint")
	}
}

func TestSequenceStatusAndColumnCap(t *testing.T) {
	value := func(v float64) *float64 { return &v }
	if SequenceStatus(OwnedSequence{PctUsed: value(91)}) != StatusCritical ||
		SequenceStatus(OwnedSequence{PctUsed: value(80)}) != StatusWarning ||
		SequenceStatus(OwnedSequence{PctUsed: value(10)}) != StatusOK ||
		SequenceStatus(OwnedSequence{}) != StatusOK {
		t.Error("sequence thresholds are 75% warning, 90% critical")
	}
	if columnTypeMax("integer") != 2147483647 || columnTypeMax("bigint") != 9223372036854775807 {
		t.Error("column type limits")
	}
	burned := UpsertTable{Sequences: []OwnedSequence{{Name: "s", ValuesPerLiveRow: value(5)}}}
	if len(UpsertTableHints(burned)) != 1 {
		t.Errorf("a burned sequence should produce one hint, got %v", UpsertTableHints(burned))
	}
}

func TestDatabaseHealthStatus(t *testing.T) {
	count := func(v int64) *int64 { return &v }
	ms := func(v float64) *float64 { return &v }
	if DatabaseHealthStatus(DatabaseHealth{ChecksumFailures: count(1)}) != StatusCritical {
		t.Error("a checksum failure is critical")
	}
	if DatabaseHealthStatus(DatabaseHealth{Deadlocks: 1}) != StatusWarning {
		t.Error("a deadlock is a warning")
	}
	rollbacks := DatabaseHealth{XactCommit: 900, XactRollback: 100}
	rollbacks.RollbackPct = ms(10)
	if DatabaseHealthStatus(rollbacks) != StatusWarning {
		t.Error("10% rollbacks over 1,000 transactions is a warning")
	}
	idle := DatabaseHealth{SessionTimeMS: ms(1000), IdleInTransactionTimeMS: ms(200)}
	if DatabaseHealthStatus(idle) != StatusWarning {
		t.Error("20% of session time idle in transaction is a warning")
	}
	if DatabaseHealthStatus(DatabaseHealth{ChecksumFailures: count(0), XactCommit: 10}) != StatusOK {
		t.Error("a quiet database is ok")
	}
}

func TestReplayStatus(t *testing.T) {
	backlog := int64(2 << 30)
	if ReplayStatus(RecoveryConflicts{InRecovery: false}) != StatusOK {
		t.Error("a primary is ok")
	}
	if ReplayStatus(RecoveryConflicts{InRecovery: true}) != StatusWarning {
		t.Error("a standby without a WAL receiver is a warning")
	}
	streaming := &WALReceiverStatus{Status: "streaming"}
	if ReplayStatus(RecoveryConflicts{InRecovery: true, WALReceiver: streaming, ReplayBacklogBytes: &backlog}) != StatusCritical {
		t.Error("2 GB of replay backlog is critical")
	}
	idleLag := 3600.0
	zero := int64(0)
	if ReplayStatus(RecoveryConflicts{InRecovery: true, WALReceiver: streaming, ReplayBacklogBytes: &zero, ReplayLagSeconds: &idleLag}) != StatusOK {
		t.Error("an old replay timestamp without backlog is an idle primary, not lag")
	}
}
