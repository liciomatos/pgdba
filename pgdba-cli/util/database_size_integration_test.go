package util

import (
	"testing"
)

func TestCheckDatabaseSizes_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckDatabaseSizes(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckDatabaseSizes_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckDatabaseSizes(dummyInitialModel)
	m, ok := result.(DatabaseSizeModel)
	if !ok {
		t.Fatalf("expected DatabaseSizeModel, got %T", result)
	}
	if len(m.report.Databases) == 0 {
		t.Error("expected at least one database in the report")
	}
	if m.report.TotalBytes <= 0 {
		t.Error("expected a positive total cluster size")
	}
}
