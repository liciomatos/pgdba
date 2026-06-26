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

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		skipIntegration = true
		os.Exit(m.Run())
	}

	ctx := context.Background()
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
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
	config.Config.Version = "16.0"

	os.Exit(m.Run())
}
