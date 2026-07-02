package util

import (
	"context"
	"database/sql"
	"flag"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testDB *sql.DB
var skipIntegration bool

// testPostgresImage returns the PostgreSQL image tag to test against, driven by
// PGDBA_TEST_PG_VERSION (e.g. "17-alpine") so the compatibility matrix in
// pg-compat.yml and `make test-pg-matrix` can exercise every supported version.
// Defaults to today's fast local dev loop when unset.
func testPostgresImage() string {
	if v := os.Getenv("PGDBA_TEST_PG_VERSION"); v != "" {
		return "postgres:" + v
	}
	return "postgres:16-alpine"
}

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		skipIntegration = true
		os.Exit(m.Run())
	}

	ctx := context.Background()
	pgContainer, err := postgres.Run(ctx,
		testPostgresImage(),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		os.Stderr.WriteString("WARNING: skipping integration tests (Docker unavailable): " + err.Error() + "\n")
		skipIntegration = true
		os.Exit(m.Run())
	}
	defer pgContainer.Terminate(ctx)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		os.Stderr.WriteString("WARNING: skipping integration tests (connection string): " + err.Error() + "\n")
		skipIntegration = true
		os.Exit(m.Run())
	}

	testDB, err = sql.Open("postgres", connStr)
	if err != nil {
		os.Stderr.WriteString("WARNING: skipping integration tests (sql.Open): " + err.Error() + "\n")
		skipIntegration = true
		os.Exit(m.Run())
	}

	testDB.Exec("CREATE EXTENSION IF NOT EXISTS pg_stat_statements")

	config.Config.DB = testDB
	config.Config.Host = "localhost"
	config.Config.User = "postgres"
	config.Config.DBName = "testdb"

	// Read the real server version so pgMajorVersion()-gated code paths in Fetch*
	// functions are exercised correctly for whichever image testPostgresImage() picked,
	// instead of silently testing against a fake hardcoded version.
	if err := testDB.QueryRow("SHOW server_version;").Scan(&config.Config.Version); err != nil {
		os.Stderr.WriteString("WARNING: skipping integration tests (reading server_version): " + err.Error() + "\n")
		skipIntegration = true
		os.Exit(m.Run())
	}

	os.Exit(m.Run())
}
