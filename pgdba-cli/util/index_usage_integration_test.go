package util

import (
	"testing"
)

func TestCheckIndexUsage_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckIndexUsage(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckIndexUsage_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckIndexUsage(dummyInitialModel)
	if _, ok := result.(IndexUsageModel); !ok {
		t.Errorf("expected IndexUsageModel, got %T", result)
	}
}
