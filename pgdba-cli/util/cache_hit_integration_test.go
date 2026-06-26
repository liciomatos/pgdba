package util

import (
	"testing"
)

func TestCheckCacheHit_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckCacheHit(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckCacheHit_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckCacheHit(dummyInitialModel)
	if _, ok := result.(CacheHitModel); !ok {
		t.Errorf("expected CacheHitModel, got %T", result)
	}
}
