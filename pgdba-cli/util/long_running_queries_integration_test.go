package util

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCheckLongRunningQueries_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckLongRunningQueries(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckLongRunningQueries_EmptyWhenIdle(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckLongRunningQueries(dummyInitialModel)
	m, ok := result.(LongRunningQueriesModel)
	if !ok {
		t.Fatalf("expected LongRunningQueriesModel, got %T", result)
	}
	if len(m.table.Rows()) != 0 {
		t.Errorf("expected 0 long running queries on idle server, got %d", len(m.table.Rows()))
	}
}

func TestFetchLongRunningQueries_ReportsClientSession(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	// A dedicated connection keeps pg_sleep from occupying the pool connection the
	// fetch itself uses.
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() {
		_, err := conn.ExecContext(ctx, `SELECT pg_sleep(3) /* long-running-test */`)
		done <- err
	}()
	defer func() {
		if err := <-done; err != nil {
			t.Errorf("pg_sleep session: %v", err)
		}
	}()
	time.Sleep(1500 * time.Millisecond)

	for _, includeBackground := range []bool{false, true} {
		queries, err := FetchLongRunningQueries(ctx, testDB, 1, NoRowLimit, includeBackground)
		if err != nil {
			t.Fatalf("includeBackground=%v: %v", includeBackground, err)
		}
		found := false
		for _, query := range queries {
			if !includeBackground && query.BackendType != "client backend" {
				t.Errorf("background process %q returned without include_background", query.BackendType)
			}
			if strings.Contains(query.Query, "long-running-test") {
				found = true
				if query.Username != "postgres" || query.State != "active" {
					t.Errorf("unexpected session details: %+v", query)
				}
			}
		}
		if !found {
			t.Errorf("includeBackground=%v: sleeping session not reported in %+v", includeBackground, queries)
		}
	}
}
