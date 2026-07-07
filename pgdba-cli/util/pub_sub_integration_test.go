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
