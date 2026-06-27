# Variables
BINARY_NAME=pgdba-cli
DOCKER_COMPOSE_FILE=docker-compose.yaml
COMPOSE := $(shell which podman-compose 2>/dev/null || which docker-compose 2>/dev/null)

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

# Help
help:
	@echo "Makefile commands:"
	@echo "  build        Build the Go project"
	@echo "  run          Run the Go project"
	@echo "  docker-up    Start Docker Compose"
	@echo "  docker-down  Stop Docker Compose"
	@echo "  clean        Clean the build"
	@echo "  help         Show this help message"

.PHONY: build run docker-up docker-down clean help
