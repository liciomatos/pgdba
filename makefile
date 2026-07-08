# Variables
BINARY_NAME=pgdba-cli
DOCKER_COMPOSE_FILE=docker-compose.yaml
COMPOSE := $(shell which podman-compose 2>/dev/null || which docker-compose 2>/dev/null)
CONTAINER_NAME=pgdba_main
MCP_PORT=8811

# Build the Go project
build:
	@echo "Building the project..."
	cd pgdba-cli && go build -o ./$(BINARY_NAME) .

# Run the Go project against pg-main (port 5432)
run: build
	@echo "Running the project..."
	./pgdba-cli/$(BINARY_NAME) --host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5432

# Run the Go project against pg-replica (physical standby, port 5433)
run-replica: build
	@echo "Connecting to replica..."
	./pgdba-cli/$(BINARY_NAME) --host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5433

# Run the Go project against pg-sub (logical subscriber, port 5434)
run-sub: build
	@echo "Connecting to pg-sub..."
	./pgdba-cli/$(BINARY_NAME) --host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5434

# Full local dev setup: start all containers, seed data, and apply all scenarios.
# pg-replica starts automatically via pg_basebackup once pg-main is healthy.
dev-up:
	@echo "Starting containers..."
	$(COMPOSE) -f $(DOCKER_COMPOSE_FILE) up -d
	@echo "Waiting for pg-main to be ready..."
	@until podman exec $(CONTAINER_NAME) pg_isready -U postgres -q 2>/dev/null; do sleep 2; done
	@echo "Waiting for pg-sub to be ready..."
	@until podman exec pgdba_sub pg_isready -U postgres -q 2>/dev/null; do sleep 2; done
	$(MAKE) seed
	$(MAKE) scenario-all
	@echo "Dev environment ready. Run 'make run' to connect."

# Tear down the local dev environment and remove dynamic scenarios.
dev-down: scenarios-clean
	@echo "Stopping containers..."
	$(COMPOSE) -f $(DOCKER_COMPOSE_FILE) down -v

# Start Docker Compose (containers only, no seed/scenarios)
docker-up:
	@echo "Starting containers with $(COMPOSE)..."
	$(COMPOSE) -f $(DOCKER_COMPOSE_FILE) up -d

# Stop Docker Compose
docker-down:
	@echo "Stopping containers..."
	$(COMPOSE) -f $(DOCKER_COMPOSE_FILE) down

# Seed the database with schema, test data, and pub/sub subscriptions
seed:
	@echo "Applying schema..."
	podman exec -i $(CONTAINER_NAME) psql -U postgres -d mydb < init-db/01_schema.sql
	@echo "Seeding test data..."
	podman exec -i $(CONTAINER_NAME) psql -U postgres -d mydb < init-db/02_data.sql
	@echo "Creating pub/sub subscriptions..."
	@chmod +x scripts/init-subscriptions.sh && ./scripts/init-subscriptions.sh
	@echo "Seed complete."

# Clean the build
clean:
	@echo "Cleaning the build..."
	rm -f pgdba-cli/$(BINARY_NAME)

# Apply all dynamic scenarios at once (requires dev-up or docker-up + seed first)
scenario-all:
	@chmod +x scenarios/scenario-locks.sh scenarios/scenario-longrunning.sh
	@./scenarios/scenario-locks.sh
	@./scenarios/scenario-longrunning.sh

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

# Start the MCP server against the local dev database (requires dev-up first)
mcp-up: build
	@echo "Starting MCP server on port $(MCP_PORT) against local dev database (mydb)..."
	./pgdba-cli/$(BINARY_NAME) --mcp --mcp-port=$(MCP_PORT) \
		--host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5432

# Run the full integration suite locally against every supported PostgreSQL version
# (13-18). Requires Docker/Podman. Slow — use for pre-release validation.
test-pg-matrix:
	@for v in 13 14 15 16 17 18; do \
		echo "=== PostgreSQL $$v ==="; \
		(cd pgdba-cli && PGDBA_TEST_PG_VERSION=$$v-alpine go test ./... -v -timeout 180s) || exit 1; \
	done

# Help
help:
	@echo "Makefile commands:"
	@echo "  dev-up              Start all containers + seed + all scenarios (full local setup)"
	@echo "  dev-down            Stop all containers + volumes + remove dynamic scenarios"
	@echo "  run                 Connect to pg-main (port 5432)"
	@echo "  run-replica         Connect to pg-replica / physical standby (port 5433)"
	@echo "  run-sub             Connect to pg-sub / logical subscriber (port 5434)"
	@echo "  build               Build the Go project"
	@echo "  docker-up           Start containers only (no seed/scenarios)"
	@echo "  docker-down         Stop containers"
	@echo "  clean               Remove the built binary"
	@echo "  seed                Apply schema, seed data, and create pub/sub subscriptions"
	@echo "  scenario-all        Apply all scenarios at once (locks + longrunning + slots)"
	@echo "  scenario-locks      Simulate a blocked session"
	@echo "  scenario-longrunning Simulate a long-running query"
	@echo "  scenario-slots      Create a test replication slot"
	@echo "  scenarios-clean     Remove dynamic scenarios"
	@echo "  mcp-up              Start the MCP server against the local dev database (mydb)"
	@echo "  test-pg-matrix      Run integration tests against every supported PostgreSQL version (13-18)"
	@echo "  help                Show this help message"

.PHONY: build run run-replica run-sub docker-up docker-down dev-up dev-down clean seed scenario-all scenario-locks scenario-longrunning scenario-slots scenarios-clean mcp-up test-pg-matrix help
