package util

import (
	"testing"
)

func TestCheckConnections_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckConnections(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckConnections_HasActiveConnection(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckConnections(dummyInitialModel)
	m, ok := result.(ConnectionsModel)
	if !ok {
		t.Fatalf("expected ConnectionsModel, got %T", result)
	}
	if m.usedConns == 0 {
		t.Error("expected at least 1 active connection (test connection itself)")
	}
	if m.maxConns == 0 {
		t.Error("expected maxConns > 0")
	}
	if m.usedConns >= m.maxConns {
		t.Errorf("usedConns (%d) should be less than maxConns (%d)", m.usedConns, m.maxConns)
	}
}
