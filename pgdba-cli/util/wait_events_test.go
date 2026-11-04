package util

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

func TestWaitEvents_EnterShowsQueriesOfSelectedEvent(t *testing.T) {
	queryID := int64(42)
	sampling := WaitEventSampling{
		Samples: 10, TotalAAS: 1.5,
		Events: []WaitEvent{
			{EventType: "Lock", Event: "transactionid", AAS: 1, Queries: []WaitEventQuery{
				{QueryID: &queryID, Query: "UPDATE orders SET qty = $1 WHERE id = $2", Normalized: true, AAS: 1, Pct: 100},
			}},
			{EventType: "IO", Event: "DataFileRead", AAS: 0.5, Queries: []WaitEventQuery{
				{BackendType: "autovacuum worker", AAS: 0.5, Pct: 100},
			}},
		},
	}
	rows := []table.Row{{"Lock", "transactionid", "1.00", ""}, {"IO", "DataFileRead", "0.50", ""}}
	model := WaitEventsModel{
		sampling: sampling,
		table: table.New(table.WithColumns([]table.Column{{Title: "Type", Width: 10}, {Title: "Event", Width: 15},
			{Title: "AAS", Width: 6}, {Title: "Distribution", Width: 10}}), table.WithRows(rows), table.WithFocused(true)),
		width: 120, height: 30,
	}

	var updated tea.Model = model
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := updated.View()
	if !strings.Contains(view, "IO:DataFileRead") || !strings.Contains(view, "(autovacuum worker)") {
		t.Fatalf("enter on the IO row should list its queries, got:\n%s", view)
	}
	if strings.Contains(view, "UPDATE orders") {
		t.Error("queries of other events must not be listed")
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if updated.(WaitEventsModel).detailEvent != nil || !strings.Contains(updated.View(), "enter queries") {
		t.Error("q should return to the event list")
	}
}
