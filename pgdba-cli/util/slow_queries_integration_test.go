package util

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func dummyInitialModel() tea.Model { return nil }

func TestIdentifySlowQueries_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := IdentifySlowQueries(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestIdentifySlowQueries_ReturnsSlowQueriesModel(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := IdentifySlowQueries(dummyInitialModel)
	if _, ok := result.(SlowQueriesModel); !ok {
		t.Errorf("expected SlowQueriesModel, got %T", result)
	}
}
