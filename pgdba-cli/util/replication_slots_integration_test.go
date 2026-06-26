package util

import (
	"testing"
)

func TestCheckReplicationSlotsStatus_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckReplicationSlotsStatus(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckReplicationSlotsStatus_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckReplicationSlotsStatus(dummyInitialModel)
	if _, ok := result.(ReplicationSlotsModel); !ok {
		t.Errorf("expected ReplicationSlotsModel, got %T", result)
	}
}

func TestDropReplicationSlot_InvalidName_ReturnsError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	err := dropReplicationSlot("nonexistent_slot_xyz")
	if err == nil {
		t.Error("expected error when dropping non-existent slot")
	}
}
