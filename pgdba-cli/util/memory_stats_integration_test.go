package util

import (
	"testing"
)

func TestCheckMemoryStats_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckMemoryStats(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckMemoryStats_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckMemoryStats(dummyInitialModel)
	m, ok := result.(MemoryStatsModel)
	if !ok {
		t.Fatalf("expected MemoryStatsModel, got %T", result)
	}
	if len(m.stats.Configs) == 0 {
		t.Error("expected at least one memory config row")
	}
}
