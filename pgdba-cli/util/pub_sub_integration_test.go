package util

import (
	"context"
	"testing"
)

func TestFetchPublications_Empty(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	pubs, err := FetchPublications(context.Background(), testDB)
	if err != nil {
		t.Fatalf("FetchPublications returned error: %v", err)
	}
	if pubs == nil {
		t.Error("expected non-nil slice, got nil")
	}
}

func TestFetchSubscriptions_Empty(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	subs, err := FetchSubscriptions(context.Background(), testDB)
	if err != nil {
		t.Fatalf("FetchSubscriptions returned error: %v", err)
	}
	if subs == nil {
		t.Error("expected non-nil slice, got nil")
	}
}

func TestFetchPublications_WithPublication(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	// Create a test table and publication; clean up afterward.
	_, err := testDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pubsub_test_tbl (id serial PRIMARY KEY, val text)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP TABLE IF EXISTS pubsub_test_tbl`)

	_, err = testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_test_pub FOR TABLE pubsub_test_tbl`)
	if err != nil {
		t.Fatalf("create publication: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_test_pub`)

	pubs, err := FetchPublications(ctx, testDB)
	if err != nil {
		t.Fatalf("FetchPublications returned error: %v", err)
	}

	var found bool
	for _, pub := range pubs {
		if pub.PubName == "pgdba_test_pub" {
			found = true
			if pub.AllTables {
				t.Error("expected AllTables=false for a table-specific publication")
			}
			if !pub.Insert {
				t.Error("expected Insert=true (default)")
			}
			if !pub.Update {
				t.Error("expected Update=true (default)")
			}
			if !pub.Delete {
				t.Error("expected Delete=true (default)")
			}
			if pub.TableCount != 1 {
				t.Errorf("expected TableCount=1, got %d", pub.TableCount)
			}
			break
		}
	}
	if !found {
		t.Error("pgdba_test_pub not found in FetchPublications result")
	}
}

func TestFetchPublications_AllTables(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	_, err := testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_all_pub FOR ALL TABLES`)
	if err != nil {
		t.Fatalf("create publication for all tables: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_all_pub`)

	pubs, err := FetchPublications(ctx, testDB)
	if err != nil {
		t.Fatalf("FetchPublications returned error: %v", err)
	}

	for _, pub := range pubs {
		if pub.PubName == "pgdba_all_pub" {
			if !pub.AllTables {
				t.Error("expected AllTables=true for a FOR ALL TABLES publication")
			}
			return
		}
	}
	t.Error("pgdba_all_pub not found in FetchPublications result")
}

func TestCheckPubSub_ReturnsModel(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckPubSub(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
	if _, ok := result.(PubSubModel); !ok {
		t.Errorf("expected PubSubModel, got %T", result)
	}
}

// TestCheckPubSub_BothPopulated verifies the screen model when the same instance has both
// publications and subscriptions — a valid real-world setup for cascading replication nodes.
// The subscription is created with connect=false / slot_name=NONE so no external publisher
// is required; the record still appears in pg_subscription and exercises the full layout path.
func TestCheckPubSub_BothPopulated(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pubsub_both_tbl (id serial PRIMARY KEY, val text)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP TABLE IF EXISTS pubsub_both_tbl`)

	_, err = testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_both_pub FOR TABLE pubsub_both_tbl`)
	if err != nil {
		t.Fatalf("create publication: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_both_pub`)

	// connect=false + slot_name=NONE: inserts the pg_subscription row without
	// dialling an external publisher or creating a replication slot.
	_, err = testDB.ExecContext(ctx, `
		CREATE SUBSCRIPTION pgdba_both_sub
		    CONNECTION 'host=localhost port=5432 user=postgres dbname=postgres'
		    PUBLICATION pgdba_both_pub
		    WITH (connect = false, slot_name = NONE)`)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP SUBSCRIPTION IF EXISTS pgdba_both_sub`)

	model := CheckPubSub(dummyInitialModel)
	m, ok := model.(PubSubModel)
	if !ok {
		t.Fatalf("expected PubSubModel, got %T", model)
	}
	if m.pubCount == 0 {
		t.Error("expected pubCount > 0")
	}
	if m.subCount == 0 {
		t.Error("expected subCount > 0")
	}

	// View() must not panic and must produce non-empty output with both sections.
	view := m.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
}

func TestFetchPublicationTables_WithPublication(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pubsub_detail_tbl (id serial PRIMARY KEY, val text, ts timestamptz)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP TABLE IF EXISTS pubsub_detail_tbl`)

	_, err = testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_detail_pub FOR TABLE pubsub_detail_tbl`)
	if err != nil {
		t.Fatalf("create publication: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_detail_pub`)

	tables, err := FetchPublicationTables(ctx, testDB, "pgdba_detail_pub")
	if err != nil {
		t.Fatalf("FetchPublicationTables returned error: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	got := tables[0]
	if got.TableName != "pubsub_detail_tbl" {
		t.Errorf("expected table pubsub_detail_tbl, got %q", got.TableName)
	}
	if got.Columns == "" {
		t.Error("Columns field should not be empty")
	}
	// LastVacuum is "never" for a freshly created table — just check it's non-empty.
	if got.LastVacuum == "" {
		t.Error("LastVacuum field should not be empty")
	}
}

func TestCheckPubTableDetail_ReturnsModel(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pubsub_model_tbl (id serial PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP TABLE IF EXISTS pubsub_model_tbl`)

	_, err = testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_model_pub FOR TABLE pubsub_model_tbl`)
	if err != nil {
		t.Fatalf("create publication: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_model_pub`)

	model := CheckPubTableDetail("pgdba_model_pub", dummyInitialModel)
	if _, ok := model.(ErrorModel); ok {
		t.Errorf("expected PubTableDetailModel, got ErrorModel")
	}
	if m, ok := model.(PubTableDetailModel); ok {
		view := m.View()
		if view == "" {
			t.Error("View() returned empty string")
		}
	} else {
		t.Errorf("expected PubTableDetailModel, got %T", model)
	}
}

func TestFetchSubscriptionTables_WithSubscription(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pubsub_sub_tbl (id serial PRIMARY KEY, val text)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP TABLE IF EXISTS pubsub_sub_tbl`)

	_, err = testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_sub_pub FOR TABLE pubsub_sub_tbl`)
	if err != nil {
		t.Fatalf("create publication: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_sub_pub`)

	_, err = testDB.ExecContext(ctx, `
		CREATE SUBSCRIPTION pgdba_sub_detail
		    CONNECTION 'host=localhost port=5432 user=postgres dbname=postgres'
		    PUBLICATION pgdba_sub_pub
		    WITH (connect = false, slot_name = NONE)`)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP SUBSCRIPTION IF EXISTS pgdba_sub_detail`)

	tables, err := FetchSubscriptionTables(ctx, testDB, "pgdba_sub_detail")
	if err != nil {
		t.Fatalf("FetchSubscriptionTables returned error: %v", err)
	}
	if len(tables) == 0 {
		t.Skip("no subscription_rel rows yet — worker not started (connect=false)")
	}
	for _, table := range tables {
		if table.SyncState == "" {
			t.Errorf("SyncState should not be empty for table %s.%s", table.SchemaName, table.TableName)
		}
	}
}

func TestCheckSubTableDetail_ReturnsModel(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_submodel_pub FOR ALL TABLES`)
	if err != nil {
		t.Fatalf("create publication: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_submodel_pub`)

	_, err = testDB.ExecContext(ctx, `
		CREATE SUBSCRIPTION pgdba_submodel_sub
		    CONNECTION 'host=localhost port=5432 user=postgres dbname=postgres'
		    PUBLICATION pgdba_submodel_pub
		    WITH (connect = false, slot_name = NONE)`)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP SUBSCRIPTION IF EXISTS pgdba_submodel_sub`)

	model := CheckSubTableDetail("pgdba_submodel_sub", dummyInitialModel)
	if _, ok := model.(ErrorModel); ok {
		t.Errorf("expected SubTableDetailModel, got ErrorModel")
	}
	if m, ok := model.(SubTableDetailModel); ok {
		view := m.View()
		if view == "" {
			t.Error("View() returned empty string")
		}
	} else {
		t.Errorf("expected SubTableDetailModel, got %T", model)
	}
}

func TestFetchPublications_TruncateFlag(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pubsub_truncate_tbl (id serial PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP TABLE IF EXISTS pubsub_truncate_tbl`)

	// Create publication without truncate
	_, err = testDB.ExecContext(ctx, `CREATE PUBLICATION pgdba_notrunc_pub FOR TABLE pubsub_truncate_tbl WITH (publish = 'insert, update, delete')`)
	if err != nil {
		t.Fatalf("create publication without truncate: %v", err)
	}
	defer testDB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS pgdba_notrunc_pub`)

	pubs, err := FetchPublications(ctx, testDB)
	if err != nil {
		t.Fatalf("FetchPublications returned error: %v", err)
	}

	for _, pub := range pubs {
		if pub.PubName == "pgdba_notrunc_pub" {
			if pub.Truncate {
				t.Error("expected Truncate=false when publication was created without truncate")
			}
			if !pub.Insert || !pub.Update || !pub.Delete {
				t.Error("expected Insert/Update/Delete=true")
			}
			return
		}
	}
	t.Error("pgdba_notrunc_pub not found in FetchPublications result")
}
