package util

import (
	"database/sql"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

// makePubSubModel builds a PubSubModel with one publication and one subscription,
// starting on the publications section (activeSection=0).
func makePubSubModel() PubSubModel {
	pubTbl := table.New(
		table.WithColumns(pubColumns()),
		table.WithRows([]table.Row{{"orders_pub", "1", "no", "yes", "yes", "yes", "no", "no"}}),
		table.WithFocused(true),
		table.WithStyles(DefaultTableStyles()),
	)
	subTbl := table.New(
		table.WithColumns(subColumns()),
		table.WithRows([]table.Row{{"orders_sub", "active", "orders_pub", "123", "0/1234", "2024-01-01 10:00", "0 / 0"}}),
		table.WithFocused(true),
		table.WithStyles(InfoTableStyles()),
	)
	return PubSubModel{
		pubTable:      pubTbl,
		subTable:      subTbl,
		pubCount:      1,
		subCount:      1,
		pubH:          3,
		subH:          10,
		activeSection: 0,
		width:         120,
		height:        40,
		initialModel:  func() tea.Model { return nil },
	}
}

// makePubSubModel_PubOnly has publications but no subscriptions.
func makePubSubModel_PubOnly() PubSubModel {
	m := makePubSubModel()
	m.subTable = table.New(table.WithColumns(subColumns()), table.WithRows(nil), table.WithFocused(true))
	m.subCount = 0
	return m
}

// makePubSubModel_SubOnly has subscriptions but no publications; starts on sub section.
func makePubSubModel_SubOnly() PubSubModel {
	m := makePubSubModel()
	m.pubTable = table.New(table.WithColumns(pubColumns()), table.WithRows(nil), table.WithFocused(true))
	m.pubCount = 0
	m.activeSection = 1
	m.pubTable.SetStyles(InfoTableStyles())
	m.subTable.SetStyles(DefaultTableStyles())
	return m
}

// fakeBadDB returns a *sql.DB that immediately fails on any query (connection refused
// on port 1), so Enter drill-down tests get an error model back rather than panicking.
func fakeBadDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", "host=localhost port=1 user=x password=x dbname=x sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	return db
}

// ── Tab tests ────────────────────────────────────────────────────────────────

func TestPubSubModel_Tab_SwitchesSectionBothWays(t *testing.T) {
	m := makePubSubModel()
	if m.activeSection != 0 {
		t.Fatal("expected to start on pub section (0)")
	}

	// Pub → Sub
	newModel, _ := m.Update(keyMsg("tab"))
	psm, ok := newModel.(PubSubModel)
	if !ok {
		t.Fatal("expected PubSubModel after tab")
	}
	if psm.activeSection != 1 {
		t.Errorf("expected activeSection=1 after tab from pub, got %d", psm.activeSection)
	}

	// Sub → Pub
	newModel2, _ := psm.Update(keyMsg("tab"))
	psm2, ok := newModel2.(PubSubModel)
	if !ok {
		t.Fatal("expected PubSubModel after second tab")
	}
	if psm2.activeSection != 0 {
		t.Errorf("expected activeSection=0 after tab back to pub, got %d", psm2.activeSection)
	}
}

func TestPubSubModel_Tab_DoesNothing_WhenSubEmpty(t *testing.T) {
	m := makePubSubModel_PubOnly()
	newModel, _ := m.Update(keyMsg("tab"))
	psm, ok := newModel.(PubSubModel)
	if !ok {
		t.Fatal("expected PubSubModel")
	}
	if psm.activeSection != 0 {
		t.Errorf("expected activeSection to remain 0 when sub is empty, got %d", psm.activeSection)
	}
}

func TestPubSubModel_Tab_DoesNothing_WhenPubEmpty(t *testing.T) {
	m := makePubSubModel_SubOnly()
	newModel, _ := m.Update(keyMsg("tab"))
	psm, ok := newModel.(PubSubModel)
	if !ok {
		t.Fatal("expected PubSubModel")
	}
	if psm.activeSection != 1 {
		t.Errorf("expected activeSection to remain 1 when pub is empty, got %d", psm.activeSection)
	}
}

// ── Q tests ──────────────────────────────────────────────────────────────────

func TestPubSubModel_Q_ReturnsMenu(t *testing.T) {
	m := makePubSubModel()
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

// ── Enter / drill-down tests ─────────────────────────────────────────────────
//
// Enter calls CheckPubTableDetail / CheckSubTableDetail, which hit the DB.
// We provide a DB that fails immediately on connect so no real PostgreSQL is
// needed; the Fetch* function returns an error and Update returns an ErrorModel.
// The assertion is simply that the model transitions away from PubSubModel.

func TestPubSubModel_Enter_OnActivePub_LeavesScreen(t *testing.T) {
	m := makePubSubModel()
	m.activeSection = 0

	oldDB := config.Config.DB
	db := fakeBadDB(t)
	config.Config.DB = db
	defer func() { config.Config.DB = oldDB; db.Close() }()

	newModel, _ := m.Update(keyMsg("enter"))
	if _, isPubSub := newModel.(PubSubModel); isPubSub {
		t.Error("expected Enter to navigate away from PubSubModel, but got PubSubModel back")
	}
}

func TestPubSubModel_Enter_OnActiveSub_LeavesScreen(t *testing.T) {
	m := makePubSubModel()
	m.activeSection = 1

	oldDB := config.Config.DB
	db := fakeBadDB(t)
	config.Config.DB = db
	defer func() { config.Config.DB = oldDB; db.Close() }()

	newModel, _ := m.Update(keyMsg("enter"))
	if _, isPubSub := newModel.(PubSubModel); isPubSub {
		t.Error("expected Enter to navigate away from PubSubModel, but got PubSubModel back")
	}
}

func TestPubSubModel_Enter_OnEmptySection_DoesNothing(t *testing.T) {
	m := makePubSubModel_PubOnly()
	m.activeSection = 1 // force to sub section even though it's empty

	newModel, _ := m.Update(keyMsg("enter"))
	if _, isPubSub := newModel.(PubSubModel); !isPubSub {
		t.Error("expected Enter on empty section to keep PubSubModel")
	}
}
