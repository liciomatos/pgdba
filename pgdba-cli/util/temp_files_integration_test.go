package util

import (
	"testing"
)

func TestCheckTempFiles_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckTempFiles(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckTempFiles_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckTempFiles(dummyInitialModel)
	if _, ok := result.(TempFilesModel); !ok {
		t.Errorf("expected TempFilesModel, got %T", result)
	}
}
