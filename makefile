# Variables
BINARY_NAME=pgdba-cli
DOCKER_COMPOSE_FILE=docker-compose.yaml
COMPOSE := $(shell which podman-compose 2>/dev/null || which docker-compose 2>/dev/null)
CONTAINER_NAME=postgres_pgdba
MCP_PORT=8811

# Build the Go project
build:
	@echo "Building the project..."
	cd pgdba-cli && go build -o ./$(BINARY_NAME) .

# Run the Go project
run: build
	@echo "Running the project..."
	./pgdba-cli/$(BINARY_NAME) --host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5432

# Run Docker Compose
docker-up:
	@echo "Starting containers with $(COMPOSE)..."
	$(COMPOSE) -f $(DOCKER_COMPOSE_FILE) up -d

# Stop Docker Compose
docker-down:
	@echo "Stopping containers..."
	$(COMPOSE) -f $(DOCKER_COMPOSE_FILE) down

# Clean the build
clean:
	@echo "Cleaning the build..."
	rm -f pgdba-cli/$(BINARY_NAME)

# Seed the local database with test data (requires docker-up first)
seed:
	@echo "Applying test schema..."
	podman exec -i $(CONTAINER_NAME) psql -U postgres -d mydb < init-db/01_schema.sql
	@echo "Seeding test data..."
	podman exec -i $(CONTAINER_NAME) psql -U postgres -d mydb < init-db/02_data.sql
	@echo "Seed complete. Use 'make scenario-*' for dynamic scenarios."

# Simulate a blocked query scenario (shows in Blocked Queries screen)
scenario-locks:
	@chmod +x scenarios/scenario-locks.sh && ./scenarios/scenario-locks.sh

# Simulate a long-running query (shows in Long Running Queries screen)
scenario-longrunning:
	@chmod +x scenarios/scenario-longrunning.sh && ./scenarios/scenario-longrunning.sh

# Create a test replication slot (shows in Replication Slots screen)
scenario-slots:
	@chmod +x scenarios/scenario-slots.sh && ./scenarios/scenario-slots.sh

# Remove all dynamic scenarios (keeps seed data)
scenarios-clean:
	@chmod +x scenarios/cleanup.sh && ./scenarios/cleanup.sh

# Connect to the replication test environment primary (port 5432, testdb)
run-repl: build
	@echo "Connecting to replication primary..."
	./pgdba-cli/$(BINARY_NAME) --host=localhost --user=postgres --password=postgres --dbname=testdb --sslmode=disable --port=5432

# Start the streaming replication test environment (primary + replica)
replication-up:
	@echo "Starting replication test environment..."
	$(COMPOSE) -f docker/replication/docker-compose.yml up -d

# Stop and remove the streaming replication environment
replication-down:
	@echo "Stopping replication test environment..."
	$(COMPOSE) -f docker/replication/docker-compose.yml down -v

# Start the MCP server against the local dev database (requires docker-up first).
# Runs in the foreground on MCP_PORT so it can be pointed at from Claude Code.
mcp-up: build
	@echo "Starting MCP server on port $(MCP_PORT) against local dev database (mydb)..."
	./pgdba-cli/$(BINARY_NAME) --mcp --mcp-port=$(MCP_PORT) \
		--host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5432

# Start the MCP server against the replication test environment (requires replication-up first)
mcp-up-repl: build
	@echo "Starting MCP server on port $(MCP_PORT) against replication test environment (testdb)..."
	./pgdba-cli/$(BINARY_NAME) --mcp --mcp-port=$(MCP_PORT) \
		--host=localhost --user=postgres --password=postgres --dbname=testdb --sslmode=disable --port=5432

# Run the full integration suite locally against every supported PostgreSQL version
# (13-18). Requires Docker/Podman. Slow (pulls + boots 6 containers serially) — use for
# pre-release validation, not routine dev loops (CI's pg-compat.yml covers this on tag pushes).
test-pg-matrix:
	@for v in 13 14 15 16 17 18; do \
		echo "=== PostgreSQL $$v ==="; \
		(cd pgdba-cli && PGDBA_TEST_PG_VERSION=$$v-alpine go test ./... -v -timeout 180s) || exit 1; \
	done

# Help
help:
	@echo "Makefile commands:"
	@echo "  build               Build the Go project"
	@echo "  run                 Run the Go project"
	@echo "  docker-up           Start Docker Compose"
	@echo "  docker-down         Stop Docker Compose"
	@echo "  clean               Clean the build"
	@echo "  seed                Seed database with test data"
	@echo "  scenario-locks      Simulate a blocked session"
	@echo "  scenario-longrunning Simulate a long-running query"
	@echo "  scenario-slots      Create a test replication slot"
	@echo "  scenarios-clean     Remove dynamic scenarios"
	@echo "  run-repl            Connect to replication test environment (testdb)"
	@echo "  replication-up      Start streaming replication test environment"
	@echo "  replication-down    Stop and remove replication test environment"
	@echo "  mcp-up              Start the MCP server against the local dev database (mydb)"
	@echo "  mcp-up-repl         Start the MCP server against the replication test environment (testdb)"
	@echo "  test-pg-matrix      Run integration tests against every supported PostgreSQL version (13-18)"
	@echo "  help                Show this help message"

.PHONY: build run run-repl docker-up docker-down clean seed scenario-locks scenario-longrunning scenario-slots scenarios-clean replication-up replication-down mcp-up mcp-up-repl test-pg-matrix help
