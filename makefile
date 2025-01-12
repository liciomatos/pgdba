# Variables
BINARY_NAME=pgdba-cli
DOCKER_COMPOSE_FILE=docker-compose.yaml

# Build the Go project
build:
    @echo "Building the project..."
    GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME) ./$(BINARY_NAME)

# Run the Go project
run: build
    @echo "Running the project..."
    ./$(BINARY_NAME) --host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5432

# Run Docker Compose
docker-up:
    @echo "Starting Docker Compose..."
    docker-compose -f $(DOCKER_COMPOSE_FILE) up -d

# Stop Docker Compose
docker-down:
    @echo "Stopping Docker Compose..."
    docker-compose -f $(DOCKER_COMPOSE_FILE) down

# Clean the build
clean:
    @echo "Cleaning the build..."
    go clean
    rm -f $(BINARY_NAME)

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