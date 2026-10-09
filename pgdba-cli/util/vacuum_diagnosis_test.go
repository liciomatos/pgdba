package util

import (
	"math"
	"testing"
)

func TestEffectiveAutovacuumCost_SlowOverride(t *testing.T) {
	// The pricing-vault-v2 case: global 2 ms / 600, tables overridden to 20 ms / 200.
	globals := map[string]string{
		"autovacuum_vacuum_cost_delay": "2", "autovacuum_vacuum_cost_limit": "600",
		"vacuum_cost_delay": "0", "vacuum_cost_limit": "200", "vacuum_cost_page_miss": "2",
	}
	cost := effectiveAutovacuumCost(map[string]string{
		"autovacuum_vacuum_cost_delay": "20", "autovacuum_vacuum_cost_limit": "200",
	}, globals)
	if cost.DelaySource != SourceTable || cost.LimitSource != SourceTable {
		t.Fatalf("expected table sources, got %+v", cost)
	}
	if !cost.SlowerThanGlobal || cost.SlowdownFactor == nil || math.Abs(*cost.SlowdownFactor-30) > 1e-9 {
		t.Fatalf("expected 30x slower, got %+v", cost)
	}
	if math.Abs(*cost.ThroughputPerSec-10_000) > 1e-9 || math.Abs(*cost.GlobalThroughputPerSec-300_000) > 1e-9 {
		t.Errorf("unexpected throughputs %v / %v", *cost.ThroughputPerSec, *cost.GlobalThroughputPerSec)
	}
	// 10,000 units/s ÷ 2 per page miss × 8 KiB ≈ 39 MB/s.
	if math.Abs(*cost.MaxReadMBPerSec-39.0625) > 1e-6 {
		t.Errorf("max read rate = %v MB/s, want 39.0625", *cost.MaxReadMBPerSec)
	}
	if status := AutovacuumTableStatus(AutovacuumTable{AutovacuumEnabled: true, Cost: cost}); status != StatusCritical {
		t.Errorf("a 30x slower override should be critical, got %s", status)
	}
}

func TestEffectiveAutovacuumCost_Fallbacks(t *testing.T) {
	globals := map[string]string{
		"autovacuum_vacuum_cost_delay": "-1", "autovacuum_vacuum_cost_limit": "-1",
		"vacuum_cost_delay": "5", "vacuum_cost_limit": "300", "vacuum_cost_page_miss": "2",
	}
	cost := effectiveAutovacuumCost(map[string]string{"autovacuum_vacuum_cost_limit": "-1"}, globals)
	if cost.DelayMS != 5 || cost.Limit != 300 || cost.LimitSource != SourceGlobal {
		t.Errorf("-1 should fall back to vacuum_cost_*, got %+v", cost)
	}
	if cost.SlowerThanGlobal {
		t.Error("a table without overrides can't be slower than global")
	}

	unthrottled := effectiveAutovacuumCost(map[string]string{"autovacuum_vacuum_cost_delay": "0"}, globals)
	if unthrottled.ThroughputPerSec != nil || unthrottled.SlowerThanGlobal {
		t.Errorf("delay 0 is unthrottled and never slower, got %+v", unthrottled)
	}

	globals["vacuum_cost_delay"] = "0"
	throttledOnly := effectiveAutovacuumCost(map[string]string{"autovacuum_vacuum_cost_delay": "10"}, globals)
	if !throttledOnly.SlowerThanGlobal || throttledOnly.SlowdownFactor != nil {
		t.Errorf("a throttled table under an unthrottled global is slower with no finite factor, got %+v", throttledOnly)
	}
	if status := AutovacuumTableStatus(AutovacuumTable{AutovacuumEnabled: true, Cost: throttledOnly}); status != StatusCritical {
		t.Errorf("expected critical, got %s", status)
	}
}

func TestAutovacuumTableStatus_TriggerRatio(t *testing.T) {
	ratio := func(value float64) *float64 { return &value }
	cases := []struct {
		table AutovacuumTable
		want  Status
	}{
		{AutovacuumTable{AutovacuumEnabled: true, TriggerRatio: ratio(0.5)}, StatusOK},
		{AutovacuumTable{AutovacuumEnabled: true, TriggerRatio: ratio(1.2)}, StatusWarning},
		{AutovacuumTable{AutovacuumEnabled: true, TriggerRatio: ratio(2)}, StatusCritical},
		{AutovacuumTable{AutovacuumEnabled: false}, StatusWarning},
		{AutovacuumTable{AutovacuumEnabled: true}, StatusOK}, // threshold 0: no ratio
	}
	for _, tc := range cases {
		if got := AutovacuumTableStatus(tc.table); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.table, got, tc.want)
		}
	}
}

func TestXminHolderStatus_Thresholds(t *testing.T) {
	hour := int64(3600)
	minute := int64(60)
	cases := []struct {
		age            int64
		transactionAge *int64
		want           Status
	}{
		{1_000, &minute, StatusOK},
		{1_000, &hour, StatusWarning},
		{10_000_000, nil, StatusWarning}, // 5% of the 200M default
		{50_000_000, nil, StatusCritical},
	}
	for _, tc := range cases {
		if got := XminHolderStatus(tc.age, tc.transactionAge, 200_000_000); got != tc.want {
			t.Errorf("age %d: got %s, want %s", tc.age, got, tc.want)
		}
	}
}

func TestThrottleText(t *testing.T) {
	factor := 30.0
	cases := []struct {
		cost      AutovacuumCost
		wantText  string
		wantLevel int
	}{
		{AutovacuumCost{DelaySource: SourceGlobal, LimitSource: SourceGlobal}, "global", 3},
		{AutovacuumCost{DelayMS: 20, Limit: 200, DelaySource: SourceTable, LimitSource: SourceTable, SlowerThanGlobal: true, SlowdownFactor: &factor}, "200/20ms x30", 2},
		{AutovacuumCost{DelayMS: 0, Limit: 200, DelaySource: SourceTable, LimitSource: SourceGlobal}, "unthrottled", -1},
		{AutovacuumCost{DelayMS: 10, Limit: 200, DelaySource: SourceTable, LimitSource: SourceGlobal, SlowerThanGlobal: true}, "200/10ms slower", 2},
	}
	for _, tc := range cases {
		text := throttleText(tc.cost)
		if text != tc.wantText || throttleLevel(text) != tc.wantLevel {
			t.Errorf("got %q (level %d), want %q (level %d)", text, throttleLevel(text), tc.wantText, tc.wantLevel)
		}
	}
}
