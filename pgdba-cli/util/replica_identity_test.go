package util

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
)

func TestReplicaIdentityIssue_SuggestedFixQuotesIdentifiers(t *testing.T) {
	index := `Orders "code" key`
	issue := ReplicaIdentityIssue{SchemaName: "Sales", TableName: "order items", CandidateIndex: &index}
	if got, want := issue.SuggestedFix(), `ALTER TABLE "Sales"."order items" REPLICA IDENTITY USING INDEX "Orders ""code"" key";`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	issue.CandidateIndex = nil
	if got, want := issue.SuggestedFix(), `ALTER TABLE "Sales"."order items" REPLICA IDENTITY FULL;`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	issue.PglogicalSets = []string{"default"}
	if got := issue.SuggestedFix(); !strings.Contains(got, "ADD PRIMARY KEY") || strings.Contains(got, "IDENTITY FULL;") {
		t.Errorf("pglogical tables can't use FULL, got %s", got)
	}
	issue.HasPrimaryKey = true
	if got, want := issue.SuggestedFix(), `ALTER TABLE "Sales"."order items" REPLICA IDENTITY DEFAULT;`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestReplicaIdentityModel_PublishedOnlyToggle(t *testing.T) {
	rows := []table.Row{
		{"public", "audit_log", "default", "no PK", "orders_pub", "critical", "8 kB"},
		{"public", "events", "default", "no PK", "events_ins", "warning", "8 kB"}, // insert-only publication
		{"public", "staging", "default", "no PK", "—", "warning", "8 kB"},
	}
	model := ReplicaIdentityModel{
		table:   table.New(table.WithColumns(make([]table.Column, 7)), table.WithRows(rows)),
		allRows: rows,
		fixes:   map[string]string{},
	}
	if !model.ConsumesKey("p") {
		t.Fatal("p must be claimed over the global PgConfig shortcut")
	}

	updated, _ := model.Update(keyMsg("p"))
	model = updated.(ReplicaIdentityModel)
	visible := model.table.Rows()
	if len(visible) != 2 || visible[0][1] != "audit_log" || visible[1][1] != "events" {
		t.Fatalf("published only should keep every published table (insert-only too), got %v", visible)
	}

	model.filterText = "events"
	if got := model.visibleRows(); len(got) != 1 || got[0][1] != "events" {
		t.Fatalf("text filter should combine with published only, got %v", got)
	}
	model.filterText = ""

	updated, _ = model.Update(keyMsg("p"))
	if got := len(updated.(ReplicaIdentityModel).table.Rows()); got != 3 {
		t.Fatalf("second p should show all tables again, got %d rows", got)
	}
}
