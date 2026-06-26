package util

import (
	"testing"
)

func TestCheckAutovacuum_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckAutovacuum(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckAutovacuum_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckAutovacuum(dummyInitialModel)
	if _, ok := result.(AutovacuumModel); !ok {
		t.Errorf("expected AutovacuumModel, got %T", result)
	}
}
