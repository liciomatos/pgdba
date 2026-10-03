package util

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// seedManyTables creates enough tables/indexes to overflow any terminal, so
// screens must rely on table scrolling instead of growing past the window.
func seedManyTables(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		stmt := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS scroll_t%[1]d (id int PRIMARY KEY, val text);
			CREATE INDEX IF NOT EXISTS scroll_t%[1]d_val_idx ON scroll_t%[1]d (val);
			INSERT INTO scroll_t%[1]d SELECT g, md5(g::text) FROM generate_series(1, 20) g ON CONFLICT DO NOTHING;
			DELETE FROM scroll_t%[1]d WHERE id <= 5;`, i)
		if _, err := testDB.Exec(stmt); err != nil {
			t.Fatalf("seeding scroll_t%d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		for i := 0; i < count; i++ {
			_, _ = testDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS scroll_t%d", i))
		}
	})
}

// TestScreens_FitTerminalHeight renders every top-level screen with more rows than
// fit on screen and checks the frame never exceeds the terminal height. Bubbletea
// drops the *top* lines of an oversized frame, so an overflow shows up to the user
// as a missing header instead of a scrollable list.
func TestScreens_FitTerminalHeight(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	seedManyTables(t, 70)

	screens := map[string]func() tea.Model{
		"Dashboard":           CheckDashboard,
		"SlowQueries":         func() tea.Model { return IdentifySlowQueries(dummyInitialModel) },
		"LongRunningQueries":  func() tea.Model { return CheckLongRunningQueries(dummyInitialModel) },
		"ReplicationSlots":    func() tea.Model { return CheckReplicationSlotsStatus(dummyInitialModel) },
		"RecordLocks":         func() tea.Model { return CheckRecordLocks(dummyInitialModel) },
		"Connections":         func() tea.Model { return CheckConnections(dummyInitialModel) },
		"Autovacuum":          func() tea.Model { return CheckAutovacuum(dummyInitialModel) },
		"AutovacuumDetail":    func() tea.Model { return CheckAutovacuumDetail("public", "scroll_t0", dummyInitialModel) },
		"IndexUsage":          func() tea.Model { return CheckIndexUsage(dummyInitialModel) },
		"IndexDetail":         func() tea.Model { return CheckIndexDetail("public", "scroll_t0_pkey", dummyInitialModel) },
		"CacheHit":            func() tea.Model { return CheckCacheHit(dummyInitialModel) },
		"Users":               func() tea.Model { return CheckUsers(dummyInitialModel) },
		"Roles":               func() tea.Model { return CheckRoles(dummyInitialModel) },
		"PgConfig":            func() tea.Model { return CheckPgConfig(dummyInitialModel) },
		"SchemaBrowser":       func() tea.Model { return CheckSchemaBrowser(dummyInitialModel) },
		"Extensions":          func() tea.Model { return CheckExtensions(dummyInitialModel) },
		"Databases":           func() tea.Model { return CheckDatabases(dummyInitialModel) },
		"QueryLoad":           func() tea.Model { return CheckQueryLoad(dummyInitialModel) },
		"WaitEvents":          func() tea.Model { return CheckWaitEvents(dummyInitialModel) },
		"FreezeMonitor":       func() tea.Model { return CheckFreezeMonitor(dummyInitialModel) },
		"DatabaseSizes":       func() tea.Model { return CheckDatabaseSizes(dummyInitialModel) },
		"TempFiles":           func() tea.Model { return CheckTempFiles(dummyInitialModel) },
		"MemoryStats":         func() tea.Model { return CheckMemoryStats(dummyInitialModel) },
		"PubSub":              func() tea.Model { return CheckPubSub(dummyInitialModel) },
		"ToastTables":         func() tea.Model { return CheckToastTables(dummyInitialModel) },
		"ReplicaIdentity":     func() tea.Model { return CheckReplicaIdentity(dummyInitialModel) },
		"ReplicationConfig":   func() tea.Model { return CheckReplicationConfig(dummyInitialModel) },
		"ReplicationStandbys": func() tea.Model { return CheckReplicationStandbys(dummyInitialModel) },
	}

	for _, height := range []int{24, 40} {
		for name, build := range screens {
			t.Run(fmt.Sprintf("%s/h%d", name, height), func(t *testing.T) {
				model := build()
				if _, isError := model.(ErrorModel); isError {
					t.Fatalf("screen returned ErrorModel: %s", model.View())
				}
				model, _ = model.Update(tea.WindowSizeMsg{Width: 140, Height: height})
				lines := strings.Count(model.View(), "\n") + 1
				if lines > height {
					t.Errorf("frame has %d lines, terminal has %d — header would be cut off", lines, height)
				}
			})
		}
	}
}

// TestIndexUsage_ListsAllIndexesAndScrolls checks that no index is dropped by a
// row limit and that moving past the visible window scrolls the single list.
func TestIndexUsage_ListsAllIndexesAndScrolls(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	seedManyTables(t, 70)

	var expected int
	if err := testDB.QueryRow(`SELECT count(*) FROM pg_stat_user_indexes`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	model := CheckIndexUsage(dummyInitialModel).(IndexUsageModel)
	if got := len(model.allRows); got != expected {
		t.Fatalf("Index Usage shows %d indexes, database has %d", got, expected)
	}

	var updated tea.Model = model
	updated, _ = updated.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	for i := 0; i < expected-1; i++ {
		updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	last := updated.(IndexUsageModel).table.SelectedRow()
	lastRow := model.allRows[expected-1]
	if last[2] != lastRow[2] {
		t.Fatalf("cursor at %q after scrolling, want last index %q", last[2], lastRow[2])
	}
	view := updated.View()
	if !strings.Contains(view, lastRow[2]) {
		t.Fatalf("last index %q not visible after scrolling to the bottom", lastRow[2])
	}
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Fatalf("scrolled frame has %d lines, want <= 24", lines)
	}
}
