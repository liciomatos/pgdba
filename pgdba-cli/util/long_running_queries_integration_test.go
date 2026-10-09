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
		_, err := conn.ExecContext(ctx, `SELECT pg_sleep(6) /* long-running-test */`)
		done <- err
	}()
	defer func() {
		if err := <-done; err != nil {
			t.Errorf("pg_sleep session: %v", err)
		}
	}()

	// Poll instead of sleeping a fixed time: how soon the goroutine's query starts
	// depends on scheduling, and it must have run for more than 1 s to be listed.
	for _, includeBackground := range []bool{false, true} {
		var queries []LongRunningQuery
		var found *LongRunningQuery
		for deadline := time.Now().Add(4 * time.Second); found == nil && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			queries, err = FetchLongRunningQueries(ctx, testDB, 1, NoRowLimit, includeBackground)
			if err != nil {
				t.Fatalf("includeBackground=%v: %v", includeBackground, err)
			}
			for i := range queries {
				if strings.Contains(queries[i].Query, "long-running-test") {
					found = &queries[i]
				}
			}
		}
		if found == nil {
			t.Errorf("includeBackground=%v: sleeping session not reported in %+v", includeBackground, queries)
			continue
		}
		if found.Username != "postgres" || found.State != "active" {
			t.Errorf("unexpected session details: %+v", found)
		}
		for _, query := range queries {
			if !includeBackground && query.BackendType != "client backend" {
				t.Errorf("background process %q returned without include_background", query.BackendType)
			}
		}
	}
}
