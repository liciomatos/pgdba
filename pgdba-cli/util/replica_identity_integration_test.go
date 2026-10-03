package util

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFetchReplicaIdentityIssues_ClassifiesTables(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	setup := `
		CREATE SCHEMA ri_test;
		CREATE TABLE ri_test.with_pk        (id int PRIMARY KEY, v text);
		CREATE TABLE ri_test.no_pk          (id int, v text);
		CREATE TABLE ri_test.no_pk_full     (id int, v text);
		ALTER TABLE ri_test.no_pk_full REPLICA IDENTITY FULL;
		CREATE TABLE ri_test.nothing        (id int PRIMARY KEY, v text);
		ALTER TABLE ri_test.nothing REPLICA IDENTITY NOTHING;
		CREATE TABLE ri_test.unique_nn      (code text NOT NULL, v text);
		CREATE UNIQUE INDEX unique_nn_code_key ON ri_test.unique_nn (code);
		CREATE TABLE ri_test.unique_nullable (code text, v text);
		CREATE UNIQUE INDEX unique_nullable_code_key ON ri_test.unique_nullable (code);
		CREATE TABLE ri_test.using_index    (code text NOT NULL);
		CREATE UNIQUE INDEX using_index_code_key ON ri_test.using_index (code);
		ALTER TABLE ri_test.using_index REPLICA IDENTITY USING INDEX using_index_code_key;
		CREATE TABLE ri_test.insert_only    (id int, v text);
		CREATE TABLE ri_test.parted (id int, v text) PARTITION BY RANGE (id);
		CREATE TABLE ri_test.parted_1 PARTITION OF ri_test.parted FOR VALUES FROM (0) TO (100);
		CREATE UNLOGGED TABLE ri_test.unlogged_no_pk (id int);
		CREATE PUBLICATION ri_pub_changes FOR TABLE ri_test.no_pk, ri_test.no_pk_full, ri_test.nothing, ri_test.with_pk;
		CREATE PUBLICATION ri_pub_insert  FOR TABLE ri_test.insert_only WITH (publish = 'insert');
		CREATE PUBLICATION ri_pub_root    FOR TABLE ri_test.parted WITH (publish_via_partition_root = true);`
	if _, err := testDB.ExecContext(ctx, setup); err != nil {
		t.Fatal(err)
	}
	defer testDB.ExecContext(ctx, `
		DROP PUBLICATION IF EXISTS ri_pub_changes, ri_pub_insert, ri_pub_root;
		DROP SCHEMA IF EXISTS ri_test CASCADE;`)

	issues, err := FetchReplicaIdentityIssues(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	byTable := map[string]ReplicaIdentityIssue{}
	for _, issue := range issues {
		if issue.SchemaName == "ri_test" {
			byTable[issue.TableName] = issue
		}
	}

	for _, fine := range []string{"with_pk", "no_pk_full", "using_index", "unlogged_no_pk", "parted"} {
		if _, listed := byTable[fine]; listed {
			t.Errorf("%s has a usable identity (or can't be published) but was listed", fine)
		}
	}

	expect := []struct {
		table        string
		identity     string
		critical     bool
		publications string
		fixContains  string
	}{
		{"no_pk", "default", true, "ri_pub_changes", "REPLICA IDENTITY FULL"},
		{"nothing", "nothing", true, "ri_pub_changes", "REPLICA IDENTITY DEFAULT"}, // has a PK
		{"insert_only", "default", false, "ri_pub_insert", "REPLICA IDENTITY FULL"},
		{"unique_nn", "default", false, "", `USING INDEX "unique_nn_code_key"`},
		{"unique_nullable", "default", false, "", "REPLICA IDENTITY FULL"},    // nullable column isn't eligible
		{"parted_1", "default", true, "ri_pub_root", "REPLICA IDENTITY FULL"}, // published through its root
	}
	for _, want := range expect {
		issue, listed := byTable[want.table]
		if !listed {
			t.Errorf("%s should be listed", want.table)
			continue
		}
		if issue.ReplicaIdentity != want.identity || issue.IsCritical() != want.critical {
			t.Errorf("%s: identity=%q critical=%v, want %q/%v", want.table, issue.ReplicaIdentity, issue.IsCritical(), want.identity, want.critical)
		}
		if got := strings.Join(issue.Publications, ","); got != want.publications {
			t.Errorf("%s: publications=%q, want %q", want.table, got, want.publications)
		}
		if !strings.Contains(issue.SuggestedFix(), want.fixContains) {
			t.Errorf("%s: fix %q should contain %q", want.table, issue.SuggestedFix(), want.fixContains)
		}
	}

	// Critical (failing today) tables are listed before warnings.
	seenWarning := false
	for _, issue := range issues {
		if !issue.IsCritical() {
			seenWarning = true
		} else if seenWarning {
			t.Fatalf("critical %s.%s listed after a warning", issue.SchemaName, issue.TableName)
		}
	}

	// The failure the screen predicts really happens on the publisher (an UPDATE that
	// matches no rows doesn't error, so insert one first).
	if _, err := testDB.ExecContext(ctx, `INSERT INTO ri_test.no_pk VALUES (1, 'a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.ExecContext(ctx, `UPDATE ri_test.no_pk SET v = 'b'`); err == nil ||
		!strings.Contains(err.Error(), "replica identity") {
		t.Fatalf("expected PostgreSQL to reject UPDATE on a critical table, got %v", err)
	}
	if _, err := testDB.ExecContext(ctx, byTable["no_pk"].SuggestedFix()); err != nil {
		t.Fatalf("suggested fix failed: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, `UPDATE ri_test.no_pk SET v = 'b'`); err != nil {
		t.Fatalf("UPDATE still fails after applying the suggested fix: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, byTable["unique_nn"].SuggestedFix()); err != nil {
		t.Fatalf("suggested USING INDEX fix rejected by PostgreSQL: %v", err)
	}
}

func TestPubSub_ShowsReplicaIdentityAlert(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.ExecContext(ctx, `
		CREATE TABLE ri_alert_no_pk (id int);
		CREATE PUBLICATION ri_alert_pub FOR TABLE ri_alert_no_pk;`); err != nil {
		t.Fatal(err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS ri_alert_pub; DROP TABLE IF EXISTS ri_alert_no_pk;`)

	var model tea.Model = CheckPubSub(dummyInitialModel)
	model, _ = model.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	view := stripAnsi(model.View())
	if !strings.Contains(view, "without usable replica identity") || !strings.Contains(view, "press I") {
		t.Fatalf("Pub/Sub should alert about published tables without replica identity:\n%s", view)
	}
}

// TestFetchReplicaIdentityIssues_PglogicalSets recreates the three pglogical catalogs
// the query reads (same columns as pglogical 2.x) so the pglogical path runs on every
// PostgreSQL version in the matrix without the extension being installed.
func TestFetchReplicaIdentityIssues_PglogicalSets(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.ExecContext(ctx, `
		CREATE SCHEMA pglogical;
		CREATE TABLE pglogical.local_node (node_id oid PRIMARY KEY, node_local_interface oid NOT NULL);
		CREATE TABLE pglogical.replication_set (
			set_id oid PRIMARY KEY, set_nodeid oid NOT NULL, set_name name NOT NULL,
			replicate_insert boolean NOT NULL DEFAULT true, replicate_update boolean NOT NULL DEFAULT true,
			replicate_delete boolean NOT NULL DEFAULT true, replicate_truncate boolean NOT NULL DEFAULT true);
		CREATE TABLE pglogical.replication_set_table (
			set_id oid NOT NULL, set_reloid regclass NOT NULL, set_att_list text[], set_row_filter pg_node_tree,
			PRIMARY KEY (set_id, set_reloid));
		INSERT INTO pglogical.local_node VALUES (1, 1);
		INSERT INTO pglogical.replication_set (set_id, set_nodeid, set_name) VALUES (10, 1, 'default'), (30, 2, 'other_node');
		INSERT INTO pglogical.replication_set (set_id, set_nodeid, set_name, replicate_update, replicate_delete)
			VALUES (20, 1, 'default_insert_only', false, false);

		CREATE SCHEMA plg_test;
		CREATE TABLE plg_test.full_no_pk   (id int, v text);
		ALTER TABLE plg_test.full_no_pk REPLICA IDENTITY FULL;
		CREATE TABLE plg_test.full_with_pk (id int PRIMARY KEY);
		ALTER TABLE plg_test.full_with_pk REPLICA IDENTITY FULL;
		CREATE TABLE plg_test.full_unique  (code text NOT NULL);
		CREATE UNIQUE INDEX full_unique_code_key ON plg_test.full_unique (code);
		ALTER TABLE plg_test.full_unique REPLICA IDENTITY FULL;
		CREATE TABLE plg_test.pk_ok        (id int PRIMARY KEY);
		CREATE TABLE plg_test.insert_only  (id int);
		CREATE TABLE plg_test.remote_set   (id int);
		ALTER TABLE plg_test.remote_set REPLICA IDENTITY FULL;
		CREATE TABLE plg_test.full_unreplicated (id int);
		ALTER TABLE plg_test.full_unreplicated REPLICA IDENTITY FULL;
		INSERT INTO pglogical.replication_set_table (set_id, set_reloid) VALUES
			(10, 'plg_test.full_no_pk'), (10, 'plg_test.full_with_pk'), (10, 'plg_test.full_unique'),
			(10, 'plg_test.pk_ok'), (20, 'plg_test.insert_only'), (30, 'plg_test.remote_set');`); err != nil {
		t.Fatal(err)
	}
	defer testDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS plg_test CASCADE; DROP SCHEMA IF EXISTS pglogical CASCADE;`)

	issues, err := FetchReplicaIdentityIssues(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	byTable := map[string]ReplicaIdentityIssue{}
	for _, issue := range issues {
		if issue.SchemaName == "plg_test" {
			byTable[issue.TableName] = issue
		}
		if issue.SchemaName == "pglogical" {
			t.Errorf("pglogical's own catalog tables must not be listed: %s", issue.TableName)
		}
	}
	// FULL is fine natively, and remote_set belongs to another node's set.
	for _, fine := range []string{"pk_ok", "full_unreplicated", "remote_set"} {
		if _, listed := byTable[fine]; listed {
			t.Errorf("%s should not be listed", fine)
		}
	}
	expect := []struct {
		table, sets, fixContains string
		critical                 bool
	}{
		{"full_no_pk", "default", "ADD PRIMARY KEY", true},
		{"full_with_pk", "default", "REPLICA IDENTITY DEFAULT", true},
		{"full_unique", "default", `USING INDEX "full_unique_code_key"`, true},
		{"insert_only", "default_insert_only", "ADD PRIMARY KEY", false},
	}
	for _, want := range expect {
		issue, listed := byTable[want.table]
		if !listed {
			t.Errorf("%s should be listed", want.table)
			continue
		}
		if got := strings.Join(issue.PglogicalSets, ","); got != want.sets {
			t.Errorf("%s: pglogical sets=%q, want %q", want.table, got, want.sets)
		}
		if issue.IsCritical() != want.critical || len(issue.Publications) != 0 {
			t.Errorf("%s: critical=%v publications=%v", want.table, issue.IsCritical(), issue.Publications)
		}
		if !strings.Contains(issue.SuggestedFix(), want.fixContains) {
			t.Errorf("%s: fix %q should contain %q", want.table, issue.SuggestedFix(), want.fixContains)
		}
	}
}
