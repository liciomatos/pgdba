package util

import (
	"testing"
)

func TestCheckLongRunningQueries_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckLongRunningQueries(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckLongRunningQueries_EmptyWhenIdle(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckLongRunningQueries(dummyInitialModel)
	m, ok := result.(LongRunningQueriesModel)
	if !ok {
		t.Fatalf("expected LongRunningQueriesModel, got %T", result)
	}
	if len(m.table.Rows()) != 0 {
		t.Errorf("expected 0 long running queries on idle server, got %d", len(m.table.Rows()))
	}
}
