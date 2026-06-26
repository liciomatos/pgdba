package util

import (
	"testing"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

func makeReplicationSlotsModel() ReplicationSlotsModel {
	cols := []table.Column{
		{Title: "Slot Name", Width: 20},
		{Title: "Size", Width: 10},
		{Title: "Active", Width: 10},
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows([]table.Row{{"slot1", "0 bytes", "false"}}),
		table.WithFocused(true),
	)
	return ReplicationSlotsModel{table: t, initialModel: func() tea.Model { return nil }}
}

func makeRecordLocksModel() RecordLocksModel {
	cols := []table.Column{
		{Title: "Blocked PID", Width: 15},
		{Title: "Blocked User", Width: 15},
		{Title: "Blocking PID", Width: 15},
		{Title: "Blocking User", Width: 15},
		{Title: "Blocked Statement", Width: 50},
		{Title: "Blocking Statement", Width: 50},
		{Title: "Blocked Application", Width: 20},
		{Title: "Blocking Application", Width: 20},
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows([]table.Row{{"100", "user1", "200", "user2", "SELECT 1", "UPDATE t", "app1", "app2"}}),
		table.WithFocused(true),
	)
	return RecordLocksModel{table: t, initialModel: func() tea.Model { return nil }}
}

func keyMsg(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func TestReplicationSlotsModel_PressD_ActivatesConfirm(t *testing.T) {
	m := makeReplicationSlotsModel()
	newModel, _ := m.Update(keyMsg("d"))
	rsm, ok := newModel.(ReplicationSlotsModel)
	if !ok {
		t.Fatal("expected ReplicationSlotsModel")
	}
	if !rsm.confirmDelete {
		t.Error("expected confirmDelete to be true after pressing d")
	}
	if rsm.slotToDelete != "slot1" {
		t.Errorf("expected slotToDelete=slot1, got %q", rsm.slotToDelete)
	}
}

func TestReplicationSlotsModel_PressN_CancelsConfirm(t *testing.T) {
	m := makeReplicationSlotsModel()
	m.confirmDelete = true
	m.slotToDelete = "slot1"
	newModel, _ := m.Update(keyMsg("n"))
	rsm, ok := newModel.(ReplicationSlotsModel)
	if !ok {
		t.Fatal("expected ReplicationSlotsModel")
	}
	if rsm.confirmDelete {
		t.Error("expected confirmDelete to be false after pressing n")
	}
}

func TestReplicationSlotsModel_PressD_WhenConfirming_Cancels(t *testing.T) {
	m := makeReplicationSlotsModel()
	m.confirmDelete = true
	newModel, _ := m.Update(keyMsg("d"))
	rsm, ok := newModel.(ReplicationSlotsModel)
	if !ok {
		t.Fatal("expected ReplicationSlotsModel")
	}
	if rsm.confirmDelete {
		t.Error("expected confirmDelete to be false when pressing d while confirming")
	}
}

func TestReplicationSlotsModel_PressQ_ReturnsMenu(t *testing.T) {
	m := makeReplicationSlotsModel()
	called := false
	m.initialModel = func() tea.Model {
		called = true
		return nil
	}
	m.Update(keyMsg("q"))
	if !called {
		t.Error("expected initialModel to be called on q")
	}
}

func TestReplicationSlotsModel_PressQ_WhenConfirming_CancelsNotMenu(t *testing.T) {
	m := makeReplicationSlotsModel()
	called := false
	m.initialModel = func() tea.Model {
		called = true
		return nil
	}
	m.confirmDelete = true
	m.Update(keyMsg("q"))
	if called {
		t.Error("expected initialModel NOT to be called when cancelling confirmation with q")
	}
}

func TestRecordLocksModel_PressT_ActivatesConfirm(t *testing.T) {
	m := makeRecordLocksModel()
	newModel, _ := m.Update(keyMsg("t"))
	rlm, ok := newModel.(RecordLocksModel)
	if !ok {
		t.Fatal("expected RecordLocksModel")
	}
	if !rlm.confirmTerminate {
		t.Error("expected confirmTerminate to be true after pressing t")
	}
	if rlm.pidToTerminate != 200 {
		t.Errorf("expected pidToTerminate=200 (blocking PID), got %d", rlm.pidToTerminate)
	}
}

func TestRecordLocksModel_PressA_ActivatesConfirmAllSessions(t *testing.T) {
	m := makeRecordLocksModel()
	newModel, _ := m.Update(keyMsg("a"))
	rlm, ok := newModel.(RecordLocksModel)
	if !ok {
		t.Fatal("expected RecordLocksModel")
	}
	if !rlm.confirmTerminate {
		t.Error("expected confirmTerminate to be true after pressing a")
	}
	if rlm.pidToTerminate != 0 {
		t.Errorf("expected pidToTerminate=0 (all sessions), got %d", rlm.pidToTerminate)
	}
}

func TestRecordLocksModel_PressN_CancelsConfirm(t *testing.T) {
	m := makeRecordLocksModel()
	m.confirmTerminate = true
	newModel, _ := m.Update(keyMsg("n"))
	rlm, ok := newModel.(RecordLocksModel)
	if !ok {
		t.Fatal("expected RecordLocksModel")
	}
	if rlm.confirmTerminate {
		t.Error("expected confirmTerminate to be false after pressing n")
	}
}

func TestErrorModel_PressQ_ReturnsMenu(t *testing.T) {
	called := false
	m := NewErrorModel(nil, "test", func() tea.Model {
		called = true
		return nil
	})
	m.Update(keyMsg("q"))
	if !called {
		t.Error("expected initialModel to be called on q")
	}
}

func TestErrorModel_PressEsc_ReturnsMenu(t *testing.T) {
	called := false
	m := NewErrorModel(nil, "test", func() tea.Model {
		called = true
		return nil
	})
	escMsg := tea.KeyMsg{Type: tea.KeyEsc}
	m.Update(escMsg)
	if !called {
		t.Error("expected initialModel to be called on esc")
	}
}
